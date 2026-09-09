package action_test

import (
	"context"
	"fmt"

	"github.com/kartaladev/wrkflw/action"
)

// ExampleTyped registers an action written against the consumer's own Go types.
// The catalog, engine, runtime and persistence layers still see only the string
// name and the map envelope on either side.
func ExampleTyped() {
	type approveOrderIn struct {
		Ref    string `json:"ref"`
		Amount int    `json:"amount"`
	}
	type approveOrderOut struct {
		Approved bool `json:"approved"`
	}

	approveOrder := func(_ context.Context, in approveOrderIn) (approveOrderOut, error) {
		return approveOrderOut{Approved: in.Amount <= 1000}, nil
	}

	reg := action.NewRegistry()
	reg.MustRegister("approve-order", action.Typed(approveOrder))

	a, ok := reg.Resolve("approve-order")
	if !ok {
		panic("approve-order not registered")
	}

	// A service task is invoked with the whole variables map plus the engine's
	// own _idempotencyKey stamp. Lenient decoding (the default) ignores keys that
	// match nothing — "region" and "_idempotencyKey" here. It does NOT ignore a
	// case-variant of a declared name: encoding/json folds case, so "orderid"
	// would bind to OrderID. Only WithStrictInput rejects that.
	out, err := a.Do(context.Background(), map[string]any{
		"ref":             "ord-42",
		"amount":          250,
		"region":          "eu-west",
		"_idempotencyKey": "idem-7",
	})

	fmt.Println(out["approved"], err)
	// Output:
	// true <nil>
}
