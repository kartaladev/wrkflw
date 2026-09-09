package model

import (
	"errors"
	"reflect"

	"github.com/kartaladev/wrkflw/definition/internal/kindreg"
	"github.com/kartaladev/wrkflw/definition/model/validate"
)

// ErrKindNotRegistered is returned by the deserializer when a node's kind has no
// registered spec — almost always because the leaf package that owns the kind was
// not imported. Blank-import github.com/kartaladev/wrkflw/definition/kinds (or
// the specific leaf) to register every kind.
var ErrKindNotRegistered = errors.New("workflow-definition: node kind not registered (blank-import .../definition/kinds)")

// NodeSpec is the per-kind serialization driver. Each node-family leaf package
// (event, gateway, activity) registers one spec per kind it owns via RegisterKind
// in an init function, so this package can (de)serialize and name a kind without
// importing the leaf — breaking what would otherwise be an import cycle.
type NodeSpec struct {
	// Name is the stable lowerCamelCase JSON discriminator (e.g. "serviceTask").
	Name string
	// FromWire reconstructs the concrete node from its flat wire form.
	FromWire func(Base, NodeWire) Node
	// ToWire projects the concrete node into the shared wire union.
	ToWire func(Node, *NodeWire)
	// ValidationGet, for kinds with a single validation-strategy field (e.g.
	// StartEvent.InputValidation, UserTask.CompletionValidation), returns the
	// node's current strategy (nil if unset). Left nil for kinds without such a
	// slot. Used by the central fail-closed MarshalJSON check
	// (ValidationStrategyFor) and by Build's pending-descriptor reconciliation
	// (reconcileNodeValidation) — both in validation_wire.go.
	ValidationGet func(Node) validate.ValidationStrategy
	// ValidationSet, paired with ValidationGet, returns a copy of n with the slot
	// replaced by s.
	ValidationSet func(n Node, s validate.ValidationStrategy) Node
}

// nodeRegistry maps each registered kind to its spec. Populated at init time by
// RegisterKind; read (never written) at runtime.
var nodeRegistry = map[NodeKind]NodeSpec{}

// nodeTypes maps each registered kind to the concrete type that owns it, derived
// at registration time from what the kind's FromWire returns. It is the lookup
// behind [ErrForeignNodeType]: a node reporting a kind whose entry here does not
// match the node's dynamic type is a counterfeit, and Validate refuses it.
//
// A kind with a nil FromWire has no entry — definition/model/registry_test.go
// registers synthetic kinds with only a ToWire, and recording unconditionally
// would nil-panic the test binary at that call. A missing entry means the gate
// stays silent for that kind, which is also the right answer for a kind that was
// never registered at all: that is ErrKindNotRegistered's diagnosis, not this
// one's.
var nodeTypes = map[NodeKind]reflect.Type{}

// RegisterKind registers the serialization spec for a node kind. It is called
// from leaf-package init functions; calling it twice for the same kind, or with
// an empty name, is a programmer error and panics.
//
// The [kindreg.Token] first parameter closes registration to consumers: Node is
// a closed set (see the Node doc comment), and kindreg lives under
// definition/internal, so nothing outside definition/ can name the token type or
// call this function at all. Pass kindreg.Grant().
//
// Inside definition/ the token is obtainable by any package, since Go scopes the
// internal rule to definition/ as a whole rather than to the four packages that
// happen to use it. The seal is against consumers of this module, not against
// this module's own subtree.
func RegisterKind(_ kindreg.Token, k NodeKind, s NodeSpec) {
	if s.Name == "" {
		panic("workflow-definition: RegisterKind with empty Name")
	}
	if _, dup := nodeRegistry[k]; dup {
		panic("workflow-definition: RegisterKind called twice for " + s.Name)
	}
	nodeRegistry[k] = s
	nodeKindNames[k] = s.Name
	nodeKindByName[s.Name] = k
	if s.FromWire != nil {
		// FromWire on a zero Base and a zero NodeWire is the cheapest way to name
		// the concrete type without every leaf having to declare it a second
		// time. All 17 real kinds return their own type from a zero wire without
		// panicking.
		//
		// Note what does NOT protect that, since an earlier version of this
		// comment claimed it did: a future FromWire that panics on a zero wire
		// takes down package initialization for every binary importing the leaf,
		// and nodetype_guard_test.go cannot catch it — that guard lives in
		// package kinds_test and imports definition/kinds, so the offending
		// init() runs while the guard's own test binary is initializing and
		// aborts it before any assertion executes. The failure is loud and
		// immediate (nothing that imports the leaf will start), but it is caught
		// by everything breaking at once, not by that test.
		//
		// A FromWire returning a nil Node would make reflect.TypeOf nil, and
		// storing that would put a PRESENT key with a nil value in the map —
		// which reads as "this kind recorded a type" while matching no node at
		// all, rejecting every genuine node of the kind. No current kind does it;
		// recording nothing keeps the failure in the same shape as a kind with no
		// FromWire (silent gate) instead of inventing a third one.
		if t := reflect.TypeOf(s.FromWire(Base{}, NodeWire{})); t != nil {
			nodeTypes[k] = t
		}
	}
}

// nodeTypeFor returns the concrete type recorded for a kind, and whether the
// kind recorded one at all.
func nodeTypeFor(k NodeKind) (reflect.Type, bool) {
	t, ok := nodeTypes[k]
	return t, ok
}

// specFor returns the registered spec for a kind.
func specFor(k NodeKind) (NodeSpec, bool) {
	s, ok := nodeRegistry[k]
	return s, ok
}
