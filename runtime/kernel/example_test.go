package kernel_test

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kartaladev/wrkflw/definition/schedule"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime/kernel"
)

// ExampleMemTimerStore_ArmedTimer shows the bounded point lookup a timer-fire
// path uses to decide whether the timer that just fired is recurring.
//
// The question is about one timer, so the read is about one timer: ArmedTimer
// keys on the exact (instanceID, timerID) pair rather than enumerating the whole
// armed set with ListArmed. A missing row is (zero, false, nil) — not-found is a
// normal outcome, never an error — so a caller must distinguish found == false
// ("the timer is gone, treat it as one-shot") from err != nil ("recurrence is
// undeterminable; do not guess").
func ExampleMemTimerStore_ArmedTimer() {
	ctx := context.Background()
	store := kernel.NewMemTimerStore()

	fireAt := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)
	store.Arm(kernel.ArmedTimer{
		InstanceID: "order-42",
		DefID:      "order-fulfillment",
		DefVersion: 3,
		TimerID:    "order-42-tm1",
		NextRun:    fireAt,
		Kind:       engine.TimerIntermediate,
		Trigger:    schedule.Every(15 * time.Minute),
	})

	armed, found, err := store.ArmedTimer(ctx, "order-42", "order-42-tm1")
	if err != nil {
		panic(err)
	}
	fmt.Println("found:", found, "recurring:", armed.Trigger.Recurring(), "next:", armed.NextRun.Format(time.RFC3339))

	// The composite key is the pair. The same timer id under another instance is
	// a different timer, and an unarmed pair is simply absent.
	_, found, err = store.ArmedTimer(ctx, "order-99", "order-42-tm1")
	if err != nil {
		panic(err)
	}
	fmt.Println("other instance found:", found)

	// Output:
	// found: true recurring: true next: 2026-06-22T09:00:00Z
	// other instance found: false
}

// ExampleEncodeArmedTimerCursor shows the keyset cursor seam behind paged armed-timer
// listing. The cursor carries the full ORDER BY key — (next_run, instance_id,
// timer_id) — so the next page resumes at exactly the row after the last one
// seen, even when many timers share a next_run.
//
// The cursor is opaque: callers pass it back verbatim and never parse it. The
// empty string is the first-page sentinel, and a malformed cursor is reported as
// [kernel.ErrBadArmedTimerCursor] rather than silently restarting at page one — an
// operator paging a large table would otherwise loop without noticing.
func ExampleEncodeArmedTimerCursor() {
	nextRun := time.Date(2026, 6, 22, 15, 0, 0, 0, time.UTC)
	cursor, err := kernel.EncodeArmedTimerCursor(nextRun, "order-42", "order-42-tm3")
	if err != nil {
		panic(err)
	}

	gotRun, gotInstance, gotTimer, err := kernel.DecodeArmedTimerCursor(cursor)
	if err != nil {
		panic(err)
	}
	fmt.Println(gotRun.Format(time.RFC3339), gotInstance, gotTimer)

	_, _, _, err = kernel.DecodeArmedTimerCursor("not-a-cursor")
	fmt.Println("malformed reported:", errors.Is(err, kernel.ErrBadArmedTimerCursor))

	// Output:
	// 2026-06-22T15:00:00Z order-42 order-42-tm3
	// malformed reported: true
}

// ExampleEncodeCursor shows the keyset cursor seam behind paged instance
// listing. The cursor carries the full ORDER BY key — (started_at, instance_id)
// — so the next page resumes at exactly the row after the last one seen, even
// when several instances share a started_at.
//
// Consumers implementing [kernel.InstanceLister] themselves use this pair to
// mint and parse the token. It is opaque: callers pass it back verbatim and
// never parse it.
//
// Two guarantees are worth noting, because both were once absent.
// A cursor from another family — an armed-timer cursor, say — is REJECTED with
// [kernel.ErrBadCursor] rather than silently reinterpreted; instance listing is
// DESC, so a foreign cursor used to yield an empty page with a 200 and no
// diagnostic. And encoding returns an error rather than the empty string, which
// is the first-page sentinel: a page must never answer HasMore with a cursor a
// client would follow back to page one.
func ExampleEncodeCursor() {
	startedAt := time.Date(2026, 6, 22, 15, 0, 0, 0, time.UTC)
	cursor, err := kernel.EncodeCursor(startedAt, "order-42")
	if err != nil {
		panic(err)
	}

	gotStarted, gotInstance, err := kernel.DecodeCursor(cursor)
	if err != nil {
		panic(err)
	}
	fmt.Println(gotStarted.Format(time.RFC3339), gotInstance)

	foreign, err := kernel.EncodeArmedTimerCursor(startedAt, "order-42", "order-42-tm3")
	if err != nil {
		panic(err)
	}
	_, _, err = kernel.DecodeCursor(foreign)
	fmt.Println("foreign cursor rejected:", errors.Is(err, kernel.ErrBadCursor))

	// Output:
	// 2026-06-22T15:00:00Z order-42
	// foreign cursor rejected: true
}

// ExampleNormalizeLimit shows how a requested page size is treated. An
// out-of-range limit is CLAMPED, never rejected: a paged admin listing should
// degrade to a sane page rather than fail an operator's request.
func ExampleNormalizeLimit() {
	fmt.Println("unset:", kernel.NormalizeLimit(0))
	fmt.Println("negative:", kernel.NormalizeLimit(-1))
	fmt.Println("in range:", kernel.NormalizeLimit(25))
	fmt.Println("over max:", kernel.NormalizeLimit(5000))

	// Output:
	// unset: 50
	// negative: 50
	// in range: 25
	// over max: 200
}

// ExampleArmedTimerFilter drives a complete paging loop over armed timers the way
// an admin surface would: request a bounded page, render it, then feed
// [kernel.ArmedTimerPage.NextCursor] back in as [kernel.ArmedTimerFilter.Cursor]
// until HasMore is false.
//
// IncludeTotal is requested only on the first page. It costs a separate count
// query, and the total is a table total that deliberately does NOT equal
// len(Items) — asking for it on every page would pay for the same number twice.
//
// The pager here is a local stand-in (see armedTimerPager). The real
// implementation is the SQL TimerStore's ListArmedPage, which satisfies
// service.TimerAdmin; [kernel.MemTimerStore] deliberately does not implement
// paging.
func ExampleArmedTimerFilter() {
	ctx := context.Background()
	base := time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC)

	// Already in (NextRun, InstanceID, TimerID) order — the same total order
	// TimerStore.ListArmed uses, which is what makes the cursor a valid resume
	// point. Note the tie at 09:30 broken by InstanceID.
	pager := armedTimerPager{
		{InstanceID: "order-42", TimerID: "order-42-tm1", NextRun: base},
		{InstanceID: "order-07", TimerID: "order-07-tm1", NextRun: base.Add(30 * time.Minute)},
		{InstanceID: "order-42", TimerID: "order-42-tm2", NextRun: base.Add(30 * time.Minute)},
		{InstanceID: "order-11", TimerID: "order-11-tm1", NextRun: base.Add(time.Hour)},
		{InstanceID: "order-11", TimerID: "order-11-tm2", NextRun: base.Add(2 * time.Hour)},
	}

	filter := kernel.ArmedTimerFilter{Limit: 2, IncludeTotal: true}
	for pageNum := 1; ; pageNum++ {
		page, err := pager.ListArmedPage(ctx, filter)
		if err != nil {
			panic(err)
		}
		if pageNum == 1 {
			fmt.Println("total armed:", page.TotalCount)
		}
		fmt.Printf("page %d:\n", pageNum)
		for _, timer := range page.Items {
			fmt.Printf("  %s %s/%s\n", timer.NextRun.Format("15:04"), timer.InstanceID, timer.TimerID)
		}
		if !page.HasMore {
			break
		}
		filter.Cursor, filter.IncludeTotal = page.NextCursor, false
	}

	// Output:
	// total armed: 5
	// page 1:
	//   09:00 order-42/order-42-tm1
	//   09:30 order-07/order-07-tm1
	// page 2:
	//   09:30 order-42/order-42-tm2
	//   10:00 order-11/order-11-tm1
	// page 3:
	//   11:00 order-11/order-11-tm2
}

// armedTimerPager is an in-example stand-in for the SQL TimerStore's
// ListArmedPage. It exists so ExampleArmedTimerFilter can show the caller-side
// paging contract — cursor continuation, HasMore, clamped Limit, opt-in
// TotalCount — without a database: kernel.MemTimerStore is a test helper and
// implements neither Stats nor ListArmedPage, so it is deliberately not a
// service.TimerAdmin.
//
// Items must already be in (NextRun, InstanceID, TimerID) order.
type armedTimerPager []kernel.ArmedTimer

func (p armedTimerPager) ListArmedPage(_ context.Context, filter kernel.ArmedTimerFilter) (kernel.ArmedTimerPage, error) {
	limit := kernel.NormalizeLimit(filter.Limit)

	start := 0
	if filter.Cursor != "" {
		nextRun, instanceID, timerID, err := kernel.DecodeArmedTimerCursor(filter.Cursor)
		if err != nil {
			return kernel.ArmedTimerPage{}, err
		}
		// Skip everything up to and including the cursor row: the keyset
		// predicate is strictly-greater on the whole triple, so a tie on
		// NextRun neither repeats nor skips a row.
		for start < len(p) && compareArmedKey(p[start], nextRun, instanceID, timerID) <= 0 {
			start++
		}
	}

	end := min(start+limit, len(p))
	page := kernel.ArmedTimerPage{Items: p[start:end], HasMore: end < len(p)}
	if page.HasMore && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		cursor, err := kernel.EncodeArmedTimerCursor(last.NextRun, last.InstanceID, last.TimerID)
		if err != nil {
			return kernel.ArmedTimerPage{}, err
		}
		page.NextCursor = cursor
	}
	if filter.IncludeTotal {
		page.TotalCount = len(p)
	}
	return page, nil
}

// compareArmedKey orders one armed timer against a cursor triple using the same
// (NextRun, InstanceID, TimerID) total order the store's ORDER BY uses.
func compareArmedKey(a kernel.ArmedTimer, nextRun time.Time, instanceID, timerID string) int {
	return cmp.Or(
		a.NextRun.Compare(nextRun),
		cmp.Compare(a.InstanceID, instanceID),
		cmp.Compare(a.TimerID, timerID),
	)
}
