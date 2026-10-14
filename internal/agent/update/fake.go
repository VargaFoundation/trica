package update

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
)

// FakeSource is an in-memory server for tests and the simulated lab. Like the
// real server, it decides per agent: Desired answers only agents that are in
// the wave of the release.
type FakeSource struct {
	mu sync.Mutex
	// Version and Percent are the release on offer; Version "" offers nothing.
	Version string
	Percent float64
	// Files maps a file name to its content, for every version.
	Files map[string][]byte
	// OfferToAll skips the server-side wave decision, to prove the agent
	// checks the wave itself.
	OfferToAll bool
	// Queries and Opened record what the agent asked, in order.
	Queries []Query
	Opened  []string
}

var _ Source = (*FakeSource)(nil)

// Desired implements Source.
func (f *FakeSource) Desired(_ context.Context, q Query) (*Offer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Queries = append(f.Queries, q)
	if f.Version == "" {
		return nil, nil
	}
	if !f.OfferToAll && !InWave(q.Identity, f.Version, f.Percent) {
		return nil, nil
	}
	return &Offer{Version: f.Version, Percent: f.Percent}, nil
}

// Open implements Source.
func (f *FakeSource) Open(_ context.Context, version, name string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Opened = append(f.Opened, name)
	b, ok := f.Files[name]
	if !ok {
		return nil, fmt.Errorf("fake source: no file %s for %s", name, version)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}
