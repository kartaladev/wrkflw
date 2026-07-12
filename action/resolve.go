package action

// Resolve looks name up in the scoped catalog first, then the global catalog.
// Either catalog may be nil. It returns the first match and true, or nil and
// false when neither resolves the name. This is the scoped→global tier shared
// by every action reference at execution time.
func Resolve(scoped, global Catalog, name string) (Action, bool) {
	if scoped != nil {
		if a, ok := scoped.Resolve(name); ok {
			return a, true
		}
	}
	if global != nil {
		return global.Resolve(name)
	}
	return nil, false
}
