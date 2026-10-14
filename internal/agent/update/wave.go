package update

import (
	"crypto/sha256"
	"encoding/binary"
	"math"
)

// buckets is the resolution of a rollout: 10 000 buckets, so 0.01 % steps.
const buckets = 10000

// Bucket places an agent in 0..9999 for one release. It is a pure function of
// the agent's identity and the version: the same agent gets the same bucket on
// every call, on the server and on the agent, with no state to keep.
// The version is part of the hash so that the first agents of one release are
// not the first agents of the next: a canary is not always the same host.
func Bucket(identity, version string) int {
	h := sha256.New()
	h.Write([]byte(version))
	h.Write([]byte{0})
	h.Write([]byte(identity))
	sum := h.Sum(nil)
	return int(binary.BigEndian.Uint64(sum[:8]) % buckets)
}

// InWave reports whether an agent is inside the current wave of a release
// rolled out to percent of the fleet. Raising the percentage only ever adds
// agents: an agent in a 5 % wave is still in the 20 % one. 0 (or below, or NaN)
// selects nobody and 100 everybody.
func InWave(identity, version string, percent float64) bool {
	if math.IsNaN(percent) || percent <= 0 {
		return false
	}
	if percent >= 100 {
		return true
	}
	return Bucket(identity, version) < int(percent*buckets/100)
}
