package transform_test

import (
	"context"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/action/transform"
)

func ExampleNewTransform() {
	a, _ := transform.NewTransform(
		transform.WithExpr("total", "price * qty"),
		transform.WithExpr("vip", "total > 1000"),
	)
	out, _ := a.Do(context.Background(), map[string]any{"price": 500, "qty": 3})
	fmt.Println(out["total"], out["vip"])
	// Output: 1500 true
}

func ExampleWithMapper() {
	db := map[string]map[string]any{
		"C001": {"tier": "gold", "region": "EU"},
	}
	a, _ := transform.NewTransform(
		transform.WithMapper(func(ctx context.Context, vars map[string]any) (map[string]any, error) {
			row := db[vars["customerID"].(string)]
			return row, nil
		}),
		transform.WithExpr("vip", "tier == 'gold'"),
	)
	out, _ := a.Do(context.Background(), map[string]any{"customerID": "C001"})
	// Only the projected WithExpr value is returned; mapper scratch is not persisted.
	fmt.Println(out["vip"])
	// Output: true
}
