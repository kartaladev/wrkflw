// Package kinds registers every node kind with the definition package by
// blank-importing all node-family leaf packages. Any code that deserializes a
// ProcessDefinition (from JSON/JSONB or YAML) without otherwise importing the
// leaves — for example a persistence layer or a transport decoder — must
// blank-import this package so the registry is fully populated:
//
//	import _ "github.com/zakyalvan/krtlwrkflw/definition/kinds"
//
// Code that constructs definitions in Go already imports the specific leaf
// packages it uses and does not strictly need this bundle, but importing it is
// harmless and guarantees completeness.
package kinds

import (
	_ "github.com/zakyalvan/krtlwrkflw/definition/activity"
	_ "github.com/zakyalvan/krtlwrkflw/definition/event"
	_ "github.com/zakyalvan/krtlwrkflw/definition/gateway"
)
