package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/kartaladev/wrkflw/definition/event"
	"github.com/kartaladev/wrkflw/definition/model"
	"github.com/kartaladev/wrkflw/engine"
	"github.com/kartaladev/wrkflw/runtime/kernel"
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
// Message-start dedup breadth depends on how the start is configured (ADR-0121):
//
//   - KEYED (correlationKey != ""): the created instance's id is a pure function
//     of (name, correlationKey), and Store.Create's ErrInstanceExists is the
//     authoritative, multi-replica- and restart-safe dedup — a redelivered or
//     concurrent message for the same correlation computes the same id, so
//     exactly one instance is created and the rest are clean no-ops. A
//     correlation key is single-use per instance lifetime: re-delivering it after
//     the instance completed is a no-op, not a fresh start.
//   - KEYLESS + WithMessageStartSingleton: at most one instance ever for the
//     message name (name-only deterministic id + ErrInstanceExists no-op).
//   - KEYLESS, default: each message mints a FRESH instance via the id generator
//     (BPMN message fan-in) — no dedup.
//
// An empty name is a clean no-op (nil): it is meaningless and must never match a
// manual (trigger-less) start. It is likewise a clean no-op when the message
// matches neither a running waiter nor any message-start definition. It returns
// ErrAmbiguousMessageStart when the name matches a message-start on more than one
// (latest-version) definition.
func (driver *ProcessDriver) DeliverMessage(ctx context.Context, name, correlationKey string, payload map[string]any) error {
	// An empty message name is meaningless and must never match a manual
	// (trigger-less) start, whose MessageName is also "" — a clean no-op.
	if name == "" {
		return nil
	}

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

	// Choose the instance id, which decides dedup breadth (ADR-0121 review):
	//   - KEYED (correlationKey != "")     → deterministic id from (name, key);
	//     concurrent/duplicate/redelivered messages for the same key dedup to one.
	//   - keyless + MessageStartSingleton  → name-only deterministic id; at most
	//     one instance ever for this message name.
	//   - keyless, default                 → id == "" → createAtNode mints a FRESH
	//     instance per message (BPMN message fan-in).
	id := ""
	switch {
	case correlationKey != "":
		id = messageStartInstanceID(name, correlationKey)
	case messageStartIsSingleton(def, nodeID):
		id = messageStartInstanceID(name, "")
	}

	switch _, err := driver.createAtNode(ctx, def, nodeID, id, payload); {
	case id != "" && errors.Is(err, kernel.ErrInstanceExists):
		// A deterministic-id create (keyed or singleton) whose instance already
		// exists (running or completed) is a clean no-op dedup. A fresh-idgen
		// create (id == "") must never swallow this — it surfaces below.
		return nil
	case err != nil:
		return err
	}
	return nil
}

// messageStartIsSingleton reports whether def's start node nodeID is a message
// StartEvent marked MessageStartSingleton. A missing node or a non-StartEvent is
// treated as not-singleton (default keyless fan-in).
func messageStartIsSingleton(def *model.ProcessDefinition, nodeID string) bool {
	n, ok := def.Node(nodeID)
	if !ok {
		return false
	}
	se, ok := n.(event.StartEvent)
	return ok && se.MessageStartSingleton
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
