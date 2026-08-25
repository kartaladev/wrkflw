package humantask_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kartaladev/wrkflw/authz"
	"github.com/kartaladev/wrkflw/humantask"
)

// nonZeroElem returns a non-zero value of type t, for seeding a slice whose
// aliasing we want to detect.
//
// ⚠ It FAILS the test on an unsupported kind rather than returning the zero
// value. Seeding with a zero value would make the mutation below a no-op and
// the whole guard vacuous — this repo has shipped twelve tests that could not
// fail, and a silent fallback here would be the thirteenth.
func nonZeroElem(t *testing.T, ty reflect.Type) reflect.Value {
	t.Helper()

	switch ty.Kind() {
	case reflect.String:
		return reflect.ValueOf("seed").Convert(ty)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return reflect.ValueOf(int64(7)).Convert(ty)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return reflect.ValueOf(uint64(7)).Convert(ty)
	case reflect.Bool:
		return reflect.ValueOf(true).Convert(ty)
	default:
		t.Fatalf("authz.AuthzSpec gained a slice of %s, which this guard cannot seed with a "+
			"non-zero value — extend nonZeroElem rather than letting the guard pass vacuously", ty)
		return reflect.Value{}
	}
}

// TestCloneIsolatesEveryEligibilityReference is a GUARD, so it passes on the current
// tree by design. Its falsifier is a mutation: delete either slices.Clone call
// from HumanTask.Clone (humantask.go) and it goes RED naming the field.
//
// Why it exists: Clone's doc comment claims "a newly added mutable field is
// isolated everywhere at once", but Clone hand-enumerates AuthzSpec's slice
// fields, so the claim held only for the two that happened to be listed. This
// test makes it true by DERIVING the field set reflectively — a slice field
// added to authz.AuthzSpec is covered automatically and fails here until Clone
// copies it.
//
// It is value-based on purpose: it mutates the clone and asserts the original is
// untouched. A presence check ("the field is non-nil") would pass on an aliased
// slice, which is precisely the bug.
func TestCloneIsolatesEveryEligibilityReference(t *testing.T) {
	t.Parallel()

	specType := reflect.TypeOf(authz.AuthzSpec{})

	// Every REFERENCE-typed field is in scope, not just slices: a map or pointer
	// field aliases exactly as a slice does, and Clone enumerates by hand.
	// ⚠ An unhandled reference kind FAILS below rather than being skipped —
	// silently narrowing the scan is how a guard starts promising more than it
	// checks, which is the defect this file exists to correct.
	var mutableFields []string
	for i := range specType.NumField() {
		f := specType.Field(i)
		if !f.IsExported() {
			continue
		}
		switch f.Type.Kind() {
		case reflect.Slice, reflect.Map, reflect.Pointer:
			mutableFields = append(mutableFields, f.Name)
		}
	}
	require.NotEmpty(t, mutableFields,
		"authz.AuthzSpec has no reference-typed fields — this guard would be vacuous; if the "+
			"type genuinely lost them, delete this test rather than leaving it passing on nothing")

	for _, name := range mutableFields {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			original := humantask.HumanTask{TaskID: "t1"}
			originalSpec := reflect.ValueOf(&original.Eligibility).Elem()
			field := originalSpec.FieldByName(name)
			require.True(t, field.CanSet(), "field %s is not settable", name)

			var read func() string
			var mutateClone func(reflect.Value)

			switch field.Kind() {
			case reflect.Slice:
				seeded := reflect.MakeSlice(field.Type(), 1, 1)
				seeded.Index(0).Set(nonZeroElem(t, field.Type().Elem()))
				field.Set(seeded)
				read = func() string {
					return fmt.Sprintf("%v", originalSpec.FieldByName(name).Index(0).Interface())
				}
				mutateClone = func(cf reflect.Value) {
					require.Equal(t, 1, cf.Len(), "clone lost %s entirely", name)
					cf.Index(0).Set(reflect.Zero(cf.Type().Elem()))
				}
			case reflect.Map:
				key := nonZeroElem(t, field.Type().Key())
				seeded := reflect.MakeMap(field.Type())
				seeded.SetMapIndex(key, nonZeroElem(t, field.Type().Elem()))
				field.Set(seeded)
				read = func() string {
					return fmt.Sprintf("%v", originalSpec.FieldByName(name).MapIndex(key).Interface())
				}
				mutateClone = func(cf reflect.Value) {
					require.Equal(t, 1, cf.Len(), "clone lost %s entirely", name)
					cf.SetMapIndex(key, reflect.Zero(cf.Type().Elem()))
				}
			default:
				t.Fatalf("authz.AuthzSpec.%s is a %s — a reference kind this guard cannot yet "+
					"seed and mutate. Extend this switch (and HumanTask.Clone) rather than "+
					"letting the field go unchecked", name, field.Kind())
			}

			before := read()

			clone := original.Clone()
			mutateClone(reflect.ValueOf(&clone.Eligibility).Elem().FieldByName(name))

			after := read()
			assert.Equal(t, before, after,
				"HumanTask.Clone aliases AuthzSpec.%s: mutating the clone changed the original. "+
					"Clone hand-enumerates its reference fields, so a newly added one is NOT "+
					"isolated until it is copied there", name)
		})
	}
}
