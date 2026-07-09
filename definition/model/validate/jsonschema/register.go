package jsonschema

import "github.com/zakyalvan/krtlwrkflw/definition/model/validate"

// init self-registers the json-schema kind in the process-global DefaultRegistry
// so durably-persisted definitions carrying a json-schema `validation` descriptor
// reconstruct their live strategy on reload (ProcessDefinition.UnmarshalJSON).
func init() { validate.Register(Kind, Factory) }
