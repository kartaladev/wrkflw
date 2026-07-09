// Package main demonstrates the four built-in service actions wired into a single
// process definition and executed end-to-end:
//
//		start → enrich[transform] → call-api[httpcall] → notify[email] → audit[logaction] → end
//
//	  - transform enriches with a mapper (customer-DB lookup; scratch, not persisted) then
//	    computes a persisted "priority" variable via WithExpr.
//	  - httpcall calls a local httptest server that returns JSON; maps status + body into vars.
//	  - email uses WithRecipientResolver with two in-memory recipients carrying per-recipient
//	    Data for personalization; WithSender(email.SenderFunc(...)) prints to stdout (no SMTP).
//	  - logaction logs the final variables to slog.
//
// Run:  go run ./examples/builtin_actions
//
// This is a reference wiring example — not a shipped binary.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/smtp"

	"github.com/zakyalvan/krtlwrkflw/action"
	"github.com/zakyalvan/krtlwrkflw/action/email"
	"github.com/zakyalvan/krtlwrkflw/action/httpcall"
	"github.com/zakyalvan/krtlwrkflw/action/logaction"
	"github.com/zakyalvan/krtlwrkflw/action/transform"
	"github.com/zakyalvan/krtlwrkflw/definition"
	"github.com/zakyalvan/krtlwrkflw/definition/activity"
	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// customerDB is an in-memory stand-in for a real data store.
// Mapper enrichment from this map is SCRATCH — it flows through the transform
// pipeline but is never written to the returned process variables (only WithExpr
// results are persisted).
var customerDB = map[string]map[string]any{
	"cust-42": {"tier": "gold", "region": "APAC"},
	"cust-99": {"tier": "silver", "region": "EU"},
}

// notifyList is an in-memory stand-in for a subscription database.
// WithRecipientResolver returns Recipient values carrying per-recipient Data
// so the email templates can be personalized independently.
var notifyList = []email.Recipient{
	{Address: "alice@example.com", Data: map[string]any{"name": "Alice"}},
	{Address: "bob@example.com", Data: map[string]any{"name": "Bob"}},
}

func main() {
	ctx := context.Background()

	// --- 1. In-process HTTP server (httptest) —————————————————————————————
	// The httpcall action needs a real endpoint; httptest.NewServer gives us one
	// without any external dependency.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"recommendation": "expedite",
			"eta_hours":      2,
		})
	}))
	defer srv.Close()

	// --- 2. Build action implementations ————————————————————————————————————

	// transform: enriches vars via a mapper (scratch) then projects two persisted vars.
	//
	//   WithMapper — looks up the customer in customerDB and adds tier+region to the
	//     internal env. These are NOT written to out and are NOT persisted as process
	//     variables; they are scratch available to subsequent stages in this action only.
	//   WithExpr  — each WithExpr call projects one output variable that IS written to
	//     out and therefore becomes a persistent process variable. The expressions can
	//     reference mapper-enriched keys (tier, region) because stages share the env.
	enrichAction, err := transform.NewTransform(
		transform.WithMapper(func(_ context.Context, vars map[string]any) (map[string]any, error) {
			id, _ := vars["customerID"].(string)
			row, ok := customerDB[id]
			if !ok {
				// Unknown customer: return empty enrichment (non-fatal scratch miss).
				return map[string]any{"tier": "unknown", "region": "unknown"}, nil
			}
			// tier and region go into env only; NOT persisted as process vars.
			return row, nil
		}),
		// Both WithExpr outputs (priority, region) are persisted as process variables.
		// They can reference mapper scratch keys (tier, region) because stages share env.
		transform.WithExpr("priority", `tier == "gold" ? "high" : "standard"`),
		transform.WithExpr("region", "region"), // project the scratch region into a process var
	)
	if err != nil {
		log.Fatal("build transform:", err)
	}

	// httpcall: GET the local httptest server; output variables default to
	//   httpStatus (int), httpBody (decoded JSON map), httpHeaders (map[string]string).
	callAction := httpcall.NewHTTPCall(
		httpcall.WithBaseURL(srv.URL+"/api/recommendation"),
		httpcall.WithMethod(http.MethodGet),
		httpcall.WithHeader("Accept", "application/json"),
	)

	// email: resolves recipients from the in-memory notifyList (WithRecipientResolver);
	// each recipient carries its own Data map that is overlaid over instance vars
	// before rendering the templates — enabling per-recipient personalization without
	// a shared To list.
	//
	// WithSender injects a SenderFunc that prints to stdout so the example runs
	// hermetically without an SMTP server.
	notifyAction := email.NewEmail(
		email.WithFrom("workflow@example.com"),
		email.WithSubjectTemplate("Order {{.orderID}} — priority {{.priority}}"),
		email.WithBodyTemplate(
			"Hi {{.name}},\n\nYour order {{.orderID}} has been assessed as priority="+
				"{{.priority}} (region {{.region}}).\nRecommendation: {{index .httpBody \"recommendation\"}}.\n",
		),
		email.WithRecipientResolver(func(_ context.Context, _ map[string]any) ([]email.Recipient, error) {
			// Returning the in-memory list; a real implementation would query a DB.
			return notifyList, nil
		}),
		// SenderFunc is the test seam: print each personalized message to stdout.
		email.WithSender(email.SenderFunc(func(_ string, _ smtp.Auth, from string, to []string, msg []byte) error {
			fmt.Printf("\n--- email to %s (from %s) ---\n%s\n", to[0], from, msg)
			return nil
		})),
	)

	// logaction: structured slog of selected final variables.
	auditAction := logaction.NewLog(
		logaction.WithLogger(slog.Default()),
		logaction.WithLevel(slog.LevelInfo),
		logaction.WithMessage("builtin-actions example completed"),
		logaction.WithKeys("orderID", "priority", "httpStatus"),
	)

	// --- 3. Process definition ——————————————————————————————————————————————
	def, err := definition.NewBuilder("builtin-actions-demo", 1).
		Add(event.NewStart("start")).
		Add(activity.NewServiceTask("enrich", activity.WithTaskAction("enrich"))).
		Add(activity.NewServiceTask("call-api", activity.WithTaskAction("call-api"))).
		Add(activity.NewServiceTask("notify", activity.WithTaskAction("notify"))).
		Add(activity.NewServiceTask("audit", activity.WithTaskAction("audit"))).
		Add(event.NewEnd("end")).
		Connect("start", "enrich").
		Connect("enrich", "call-api").
		Connect("call-api", "notify").
		Connect("notify", "audit").
		Connect("audit", "end").
		Build()
	if err != nil {
		log.Fatal("build definition:", err)
	}
	fmt.Printf("defined %q v%d with %d nodes\n", def.ID, def.Version, len(def.Nodes))

	// --- 4. Catalog + runner ————————————————————————————————————————————————
	cat := action.NewCatalog(map[string]action.Action{
		"enrich":   enrichAction,
		"call-api": callAction,
		"notify":   notifyAction,
		"audit":    auditAction,
	})

	store, err := kernel.NewMemInstanceStore()
	if err != nil {
		log.Fatal("memstore:", err)
	}
	driver, err := runtime.NewProcessDriver(runtime.WithActionCatalog(cat), runtime.WithInstanceStore(store))
	if err != nil {
		log.Fatal("runner:", err)
	}

	// --- 5. Run ————————————————————————————————————————————————————————————
	fmt.Println("\n--- Running instance demo-001 ---")
	state, err := driver.Drive(ctx, def, "demo-001", map[string]any{
		"orderID":    "ORD-2026-001",
		"customerID": "cust-42",
		// Neither tier nor region is pre-set. The transform mapper resolves both from
		// customerDB into scratch; WithExpr("priority", …) derives priority from the
		// scratch tier, and WithExpr("region", "region") projects region out. Only those
		// two WithExpr outputs (priority, region) become process variables — the raw tier
		// stays scratch — and the email step then reads priority + region from process vars.
	})
	if err != nil {
		log.Fatal("run:", err)
	}

	// --- 6. Print outcome ——————————————————————————————————————————————————
	fmt.Printf("\n--- Instance outcome: status=%v ---\n", state.Status)
	if state.Status == engine.StatusCompleted {
		fmt.Printf("  priority  = %v\n", state.Variables["priority"])
		fmt.Printf("  httpStatus= %v\n", state.Variables["httpStatus"])
		if body, ok := state.Variables["httpBody"].(map[string]any); ok {
			fmt.Printf("  recommendation = %v\n", body["recommendation"])
			fmt.Printf("  eta_hours      = %v\n", body["eta_hours"])
		}
		fmt.Printf("  emailSent = %v (recipientCount=%v)\n",
			state.Variables["emailSent"],
			state.Variables["recipientCount"],
		)
	}
}
