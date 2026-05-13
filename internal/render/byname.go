package render

// AdapterByName pairs a named adapter with its implementation for use in sync operations.
type AdapterByName struct {
	Name    string
	Adapter Adapter
}
