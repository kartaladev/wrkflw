// Package kindreg holds the capability token that gates node-kind registration.
//
// model.RegisterKind takes a [Token], and a Token can only be obtained from
// [Grant]. Because this package sits under definition/internal, the Go build
// system refuses the import from anything outside definition/ — so no consumer
// of this module can register a node kind. Node is a closed set; this is the
// compile-time half of keeping it closed. The run-time half is
// model.ErrForeignNodeType, which rejects a foreign concrete type presented
// under a registered kind.
//
// Where the boundary actually sits: Go scopes the internal rule to the PARENT of
// internal/, which here is definition/ — so every package under definition/ may
// import this one, not only the three node-family leaf packages that do. In
// practice definition/activity, definition/event and definition/gateway are the
// only registrants. The line this seal draws is between the module's definition
// subtree and everyone outside it, and that is the line that matters: a foreign
// kind can only be introduced by editing this repository, not by importing it.
//
// The token is deliberately not the registry itself. Moving nodeRegistry into
// this package is not buildable: NodeSpec is typed in terms of model.Node,
// model.Base, model.NodeWire, model.NodeKind and validate.ValidationStrategy, so
// kindreg would have to import model, while model must import kindreg to keep
// specFor for node_wire.go, validation_wire.go and yaml.go — an import cycle.
// A token type that imports nothing has no cycle and closes the same door: the
// only thing that ever leaked was the exported function RegisterKind.
//
// Known limit, documented rather than fixed because it fails closed: reflect can
// still reach RegisterKind by constructing a zero Token from the function's own
// parameter type. Reflection defeats any seal of this shape. It is not reachable
// from ordinary consumer code, and model.ErrForeignNodeType is an independent
// second door on the path that matters.
package kindreg

// Token is the unforgeable capability to register a node kind. Its sole field is
// an unexported zero-width struct, so a package that cannot name this type
// cannot construct the value either — a composite literal of the structurally
// identical struct{ _ struct{} } is not assignable to Token.
type Token struct{ _ struct{} }

// Grant returns the registration token. Callable only from inside definition/,
// which is the point.
func Grant() Token { return Token{} }
