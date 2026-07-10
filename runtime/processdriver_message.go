package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/zakyalvan/krtlwrkflw/engine"
	"github.com/zakyalvan/krtlwrkflw/runtime/kernel"
)

// ErrAmbiguousMessageStart is returned by DeliverMessage when a message name
// matches a message-start event on more than one registered definition, so the
// runtime cannot decide which process to start. Register a single message-start
// per message name (or correlate to an already-running instance) to avoid it.
var ErrAmbiguousMessageStart = errors.New("workflow-runtime: ambiguous message start")

// DeliverMessage delivers a message identified by (name, correlationKey) to the
// engine. It first tries to correlate the message to a RUNNING instance parked
// on it (intermediate catch / message boundary / event-based-gateway arm); the
// matched instance's definition is resolved from its own snapshot, so the caller
// no longer supplies one. When no running waiter matches, the message may START
// a new instance from a unique message-start event (ADR-0121).
//
// Message-start dedup is deterministic: the created instance's id is a pure
// function of (name, correlationKey), and Store.Create's ErrInstanceExists is the
// authoritative, multi-replica- and restart-safe dedup — a redelivered or
// concurrent message for the same correlation computes the same id, so exactly
// one instance is created and the rest are clean no-ops. A correlation key is
// therefore single-use per instance lifetime: re-delivering it after the
// instance has completed is a no-op, not a fresh start.
//
// It is a clean no-op (nil) when the message matches neither a running waiter
// nor any message-start definition. It returns ErrAmbiguousMessageStart when the
// name matches a message-start on more than one definition.
func (driver *ProcessDriver) DeliverMessage(ctx context.Context, name, correlationKey string, payload map[string]any) error {
	// 1. Correlate to a running waiter parked on this message. The instance's
	//    definition is resolved from its own snapshot (DefID/DefVersion).
	if instanceID, found := driver.findMessageWaiter(name, correlationKey); found {
		def, err := driver.resolveInstanceDef(ctx, instanceID)
		if err != nil {
			return err
		}
		trg := engine.NewMessageReceived(driver.clk.Now(), name, correlationKey, payload)
		_, err = driver.ApplyTrigger(ctx, def, instanceID, trg)
		return err
	}

	// 2. No running waiter → try a message-start create.
	def, nodeID, matches := uniqueMessageStartDef(driver.listDefinitions(ctx), name)
	switch {
	case matches == 0:
		return nil // genuinely unmatched — clean no-op
	case matches > 1:
		return fmt.Errorf("%w: %q", ErrAmbiguousMessageStart, name)
	}

	// Deterministic id: concurrent/duplicate/redelivered messages for the same
	// (name, correlationKey) all compute the same id; Store.Create lets exactly
	// one win, the rest get ErrInstanceExists.
	id := messageStartInstanceID(name, correlationKey)
	switch _, err := driver.createAtNode(ctx, def, nodeID, id, payload); {
	case errors.Is(err, kernel.ErrInstanceExists):
		return nil // an instance already exists for this key (running or completed) — no-op dedup
	case err != nil:
		return err
	}
	return nil
}

// messageStartInstanceID is the deterministic, collision-safe id of the instance
// a message-start creates for a given (name, correlationKey). It hashes the two
// fields with a NUL separator so distinct pairs can never collide (the separator
// cannot appear in either field's UTF-8 bytes without also appearing in the
// hashed input, so e.g. ("ab","c") and ("a","bc") hash different byte strings).
func messageStartInstanceID(name, correlationKey string) string {
	sum := sha256.Sum256([]byte(name + "\x00" + correlationKey))
	return "msgstart-" + hex.EncodeToString(sum[:])
}
