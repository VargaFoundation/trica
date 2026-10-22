package wiring

import "time"

// SetCollectRetry shortens the lock retry interval for a test and returns the
// function that restores it.
func SetCollectRetry(d time.Duration) (restore func()) {
	old := collectRetry
	collectRetry = d
	return func() { collectRetry = old }
}

// CollectLockKey is the advisory lock key of in-server collection.
const CollectLockKey = collectLockKey

// SetMapMaxStale shortens how stale the topology map may be after a commit.
func SetMapMaxStale(d time.Duration) (restore func()) {
	old := mapMaxStale
	mapMaxStale = d
	return func() { mapMaxStale = old }
}
