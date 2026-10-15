package auth

import (
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimiter is a token bucket per key: each key may burst up to Burst requests, refilled
// at Rate per second.
type RateLimiter struct {
	rate  float64
	burst float64
	// Now is the clock; time.Now when nil.
	Now func() time.Time

	mu        sync.Mutex
	buckets   map[string]*bucket
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// sweepEvery is how often idle buckets are dropped, so the map cannot grow without bound.
const sweepEvery = time.Minute

// NewRateLimiter allows burst requests at once and rate requests per second sustained, per
// key. A non-positive rate or burst gives a limiter that refuses everything.
func NewRateLimiter(rate float64, burst int) *RateLimiter {
	return &RateLimiter{rate: rate, burst: float64(burst), buckets: map[string]*bucket{}}
}

func (l *RateLimiter) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Allow takes one token from key's bucket. When there is none it returns how long to wait.
func (l *RateLimiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	l.sweep(now)
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens = math.Min(l.burst, b.tokens+elapsed*l.rate)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	if l.rate <= 0 {
		return false, time.Hour
	}
	return false, time.Duration((1 - b.tokens) / l.rate * float64(time.Second))
}

// sweep drops the buckets that have refilled completely: they are indistinguishable from new.
func (l *RateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < sweepEvery {
		return
	}
	l.lastSweep = now
	for k, b := range l.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*l.rate >= l.burst {
			delete(l.buckets, k)
		}
	}
}

// Size returns the number of tracked keys.
func (l *RateLimiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// limitKey identifies the caller: the principal when authenticated, else the peer address. The
// forwarded-for headers are not trusted here; behind a proxy, mount the limiter after the
// authentication middleware so that principals, not addresses, are limited.
func limitKey(r *http.Request) string {
	if p, ok := FromContext(r.Context()); ok {
		return p.Subject
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "ip:" + host
}

// Middleware answers 429 with a Retry-After header (whole seconds, at least 1) when the
// caller's bucket is empty. Mount it after Middleware to limit per principal.
func (l *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok, wait := l.Allow(limitKey(r))
		if ok {
			next.ServeHTTP(w, r)
			return
		}
		secs := max(int(math.Ceil(wait.Seconds())), 1)
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		RecordReason(r.Context(), "rate limit exceeded")
		writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
	})
}
