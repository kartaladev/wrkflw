package model

// TerminationOutcomes exposes the value gate's vocabulary to this package's
// EXTERNAL test package.
//
// It exists because the drift risk it insures against is only visible from
// there. definition/event owns the TerminationOutcome type and its names and
// imports this package, so this package can never import event to read them
// off it — which is why node_wire_vocabulary.go spells the two names by hand at
// all. The external test package CAN import event, so it is the only place
// where the hand-written set and the type that owns it are both in scope and
// can be compared. Exporting through export_test.go keeps that comparison
// possible without putting the variable in the production build.
var TerminationOutcomes = terminationOutcomes
