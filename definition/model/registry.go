package model

import "errors"

// ErrKindNotRegistered is returned by the deserializer when a node's kind has no
// registered spec — almost always because the leaf package that owns the kind was
// not imported. Blank-import github.com/zakyalvan/krtlwrkflw/definition/kinds (or
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
}

// nodeRegistry maps each registered kind to its spec. Populated at init time by
// RegisterKind; read (never written) at runtime.
var nodeRegistry = map[NodeKind]NodeSpec{}

// RegisterKind registers the serialization spec for a node kind. It is called
// from leaf-package init functions; calling it twice for the same kind, or with
// an empty name, is a programmer error and panics.
func RegisterKind(k NodeKind, s NodeSpec) {
	if s.Name == "" {
		panic("workflow-definition: RegisterKind with empty Name")
	}
	if _, dup := nodeRegistry[k]; dup {
		panic("workflow-definition: RegisterKind called twice for " + s.Name)
	}
	nodeRegistry[k] = s
	nodeKindNames[k] = s.Name
	nodeKindByName[s.Name] = k
}

// specFor returns the registered spec for a kind.
func specFor(k NodeKind) (NodeSpec, bool) {
	s, ok := nodeRegistry[k]
	return s, ok
}
