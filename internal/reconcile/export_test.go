package reconcile

// SetDisplayBatch changes the recompute batch size and returns a function that restores it.
func SetDisplayBatch(n int) func() {
	old := displayBatch
	displayBatch = n
	return func() { displayBatch = old }
}
