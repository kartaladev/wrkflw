package service

import (
	"github.com/kartaladev/wrkflw/engine"
)

// ProjectFor returns a [ProcessInstance] that renders proj(pi.State()) instead of
// pi.State(), optionally also withholding the embedded definition.
//
// It exists for the HTTP transport's disclosure posture (ADR-0190): a caller the transport
// could not identify receives a projection of the instance rather than the whole of it. The
// transport owns that decision — this function only carries it into the marshalled document,
// which `service` alone can construct.
//
// # ⚠ It may ADD an omission, never remove one
//
// withholdDefinition=true drops the `definition` embed. withholdDefinition=false leaves the
// engine's own [WithoutEmbeddedDefinition] setting exactly as it was — it does NOT re-embed.
//
// That asymmetry is the whole reason this function exists rather than the transport calling
// [NewProcessInstance] with projected state. NewProcessInstance always embeds (its own godoc
// says so), so rebuilding through it silently RE-EMBEDDED the template for every consumer
// running WithoutEmbeddedDefinition — carrying every node's eligibility spec, on the one
// route a disclosure change was meant to narrow. Measured at the time: 781 → 1068 bytes.
//
// # Foreign implementations
//
// pi is normally the concrete instance this package returns, whose marshalling policy is
// preserved. A ProcessInstance from anywhere else has no policy this package can read, so
// its projection ALWAYS withholds the definition — withholdDefinition=false does not
// override that. This is the fail-closed reading of "we do not know what this thing was
// configured to emit", and it is deliberately stricter than the concrete path.
func ProjectFor(
	pi ProcessInstance,
	proj func(engine.InstanceState) engine.InstanceState,
	withholdDefinition bool,
) ProcessInstance {
	if pi == nil {
		return nil
	}

	st := pi.State()
	if proj != nil {
		st = proj(st)
	}

	def := pi.Definition()
	if withholdDefinition {
		def = nil
	}

	if concrete, ok := pi.(processInstance); ok {
		// Preserve the engine's setting; OR in the caller's withholding.
		return processInstance{
			def:            def,
			st:             st,
			omitDefinition: concrete.omitDefinition || withholdDefinition,
		}
	}

	// Unknown implementation: we cannot read its policy, so withhold rather than guess.
	return processInstance{def: def, st: st, omitDefinition: true}
}
