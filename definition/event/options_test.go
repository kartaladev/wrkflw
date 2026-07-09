package event_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zakyalvan/krtlwrkflw/definition/event"
	"github.com/zakyalvan/krtlwrkflw/definition/model/validate"
)

// stubStrategy is a no-op validate.ValidationStrategy used to exercise the
// InputValidation/PayloadValidation option wiring without depending on any
// concrete adapter package.
type stubStrategy struct{}

func (stubStrategy) NewValidator() (validate.Validator, error) {
	return stubValidator{}, nil
}

type stubValidator struct{}

func (stubValidator) Validate(context.Context, map[string]any) error { return nil }

func TestWithInputValidation_Start_SetsSlot(t *testing.T) {
	t.Parallel()
	n := event.NewStart("s", event.WithInputValidation(stubStrategy{}))
	se, ok := n.(event.StartEvent)
	if !ok {
		t.Fatalf("node kind = %T", n)
	}
	if se.InputValidation == nil {
		t.Fatal("InputValidation not set")
	}
}

func TestWithPayloadValidation_Catch_SetsSlot(t *testing.T) {
	t.Parallel()
	n := event.NewIntermediateCatch("wait", event.WithPayloadValidation(stubStrategy{}))
	ce, ok := n.(event.IntermediateCatchEvent)
	if !ok {
		t.Fatalf("node kind = %T", n)
	}
	if ce.PayloadValidation == nil {
		t.Fatal("PayloadValidation not set")
	}
}

func TestWithMessageCorrelator_AllKinds(t *testing.T) {
	t.Parallel()

	t.Run("catch", func(t *testing.T) {
		t.Parallel()
		n := event.NewIntermediateCatch("c", event.WithMessageCorrelator("m", "k"))
		c, ok := n.(event.IntermediateCatchEvent)
		require.Truef(t, ok, "node kind = %T", n)
		assert.Equal(t, "m", c.MessageName)
		assert.Equal(t, "k", c.CorrelationKey)
	})

	t.Run("start", func(t *testing.T) {
		t.Parallel()
		n := event.NewStart("s", event.WithMessageCorrelator("m", "k"))
		s, ok := n.(event.StartEvent)
		require.Truef(t, ok, "node kind = %T", n)
		assert.Equal(t, "m", s.MessageName)
		assert.Equal(t, "k", s.CorrelationKey)
	})

	t.Run("boundary", func(t *testing.T) {
		t.Parallel()
		n := event.NewBoundary("b", "host", event.WithMessageCorrelator("m", "k"))
		b, ok := n.(event.BoundaryEvent)
		require.Truef(t, ok, "node kind = %T", n)
		assert.Equal(t, "m", b.MessageName)
		assert.Equal(t, "k", b.CorrelationKey)
	})
}

func TestWithSignalName_ListenKinds(t *testing.T) {
	t.Parallel()

	t.Run("catch", func(t *testing.T) {
		t.Parallel()
		n := event.NewIntermediateCatch("c", event.WithSignalName("s"))
		c, ok := n.(event.IntermediateCatchEvent)
		require.Truef(t, ok, "node kind = %T", n)
		assert.Equal(t, "s", c.SignalName)
	})

	t.Run("start", func(t *testing.T) {
		t.Parallel()
		n := event.NewStart("s", event.WithSignalName("go"))
		se, ok := n.(event.StartEvent)
		require.Truef(t, ok, "node kind = %T", n)
		assert.Equal(t, "go", se.SignalName)
	})

	t.Run("boundary", func(t *testing.T) {
		t.Parallel()
		n := event.NewBoundary("b", "host", event.WithSignalName("s"))
		b, ok := n.(event.BoundaryEvent)
		require.Truef(t, ok, "node kind = %T", n)
		assert.Equal(t, "s", b.SignalName)
	})
}
