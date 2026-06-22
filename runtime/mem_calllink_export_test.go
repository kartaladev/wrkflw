package runtime

// SeedCallLink inserts a call link into a MemCallLinkStore for test use.
// It exposes the unexported record method to black-box test packages.
func SeedCallLink(s *MemCallLinkStore, link CallLink) {
	s.record(link)
}

// SeedTerminal marks a child instance's link as terminal with the given
// outcome, for test use.
func SeedTerminal(s *MemCallLinkStore, childID string, out CallOutcome) {
	s.markTerminal(childID, out)
}
