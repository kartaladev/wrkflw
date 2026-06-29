package transform_test

import (
	"context"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/action/transform"
)

func ExampleNewTransform() {
	a, _ := transform.NewTransform(
		transform.Set("total", "price * qty"),
		transform.Set("vip", "total > 1000"),
	)
	out, _ := a.Do(context.Background(), map[string]any{"price": 500, "qty": 3})
	fmt.Println(out["total"], out["vip"])
	// Output: 1500 true
}
