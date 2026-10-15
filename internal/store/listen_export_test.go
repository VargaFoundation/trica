package store

import "time"

// SetListenRetry shortens the reconnect wait and returns a function restoring
// it.
func SetListenRetry(d time.Duration) func() {
	old, oldMax := listenRetry, listenRetryMax
	listenRetry, listenRetryMax = d, d
	return func() { listenRetry, listenRetryMax = old, oldMax }
}
