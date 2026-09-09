package expreval_test

import (
	"fmt"
	"testing"

	"github.com/expr-lang/expr/builtin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/internal/expreval"
)

// TestBuiltinsAreNotShadowableByEnvKeys guards a security barrier that this
// package's COMPILE CONFIGURATION provides and that one option would delete.
//
// Process variables are attacker-influenced — key names included — and the engine
// evaluates definition-authored predicates over them. The barrier is that a
// caller-chosen key cannot change what a definition's program MEANS: expr resolves
// a name in call position to a builtin from the source token alone, and the only
// override is conf.Config.IsOverridden, which consults the map handed to
// expr.Env(...). This package passes no expr.Env, so that override is permanently
// false.
//
// ⚠ THIS IS WHY THE GUARD EXISTS. Adding expr.Env(env) is the idiomatic way to get
// expr's type checking, and it would silently delete the barrier — measured:
//
//	expr.Compile(`len(xs)`, AllowUndefinedVariables())            -> 2
//	expr.Compile(`len(xs)`, Env(vars), AllowUndefinedVariables()) -> "PWNED"
//
// with an identical env at Run time in both. Nothing else in the tree would fail,
// so without this test the change is invisible. (It would also make compile's
// cache unsound, since programs are keyed by the code string alone.)
//
// The property asserted is INVARIANCE UNDER PLANTING, not success: a compile
// error, a run error and a value all count as outcomes, and planting a key must
// not change which one you get. Scope is CALL and OPERATOR position, which is
// exactly the claim documented on env["_error"] in engine/step_errors.go. BARE
// identifier position is deliberately NOT asserted — most names resolve from the
// environment there, and that is ordinary caller-writable data, not shadowing.
func TestBuiltinsAreNotShadowableByEnvKeys(t *testing.T) {
	t.Parallel()

	require.NotEmpty(t, builtin.Builtins, "the domain must come from expr, not from a list typed here")

	e := expreval.New(expreval.WithTimeout(0))
	outcome := func(code string, env map[string]any) string {
		out, err := e.EvalBool(code, env)
		if err != nil {
			return "err"
		}
		return fmt.Sprintf("val:%v", out)
	}

	base := map[string]any{"xs": []any{"a", "bb", "ccc"}, "ys": []any{"a"}}

	var shadowed []string
	for _, b := range builtin.Builtins {
		for _, code := range []string{
			b.Name + `(xs) == "x"`,     // call position
			`xs ` + b.Name + ` ys`,     // operator position
			b.Name + `(xs, ys) == "x"`, // 2-arg call position
		} {
			planted := map[string]any{"xs": base["xs"], "ys": base["ys"], b.Name: "PWNED"}
			if outcome(code, base) != outcome(code, planted) {
				shadowed = append(shadowed, code)
			}
		}
	}

	assert.Empty(t, shadowed,
		"a process variable named after a builtin changed what a definition's "+
			"predicate does. If expr.Env(...) was just added to compile(), that is "+
			"the cause and it removes a documented security barrier; see the "+
			"comment on env[\"_error\"] in engine/step_errors.go")
	t.Logf("swept %d builtins x 3 syntactic positions; %d shadowable", len(builtin.Builtins), len(shadowed))
}
