package expr

import "github.com/zakyalvan/krtlwrkflw/definition/model/validate"

// init self-registers the expr kind in the process-global DefaultRegistry so
// durably-persisted definitions carrying an expr `validation` descriptor
// reconstruct their live strategy on reload (ProcessDefinition.UnmarshalJSON).
func init() { validate.Register(Kind, Factory) }
