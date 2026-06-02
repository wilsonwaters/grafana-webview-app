// Package security provides the hardcoded and configurable security building
// blocks shared by every proxying endpoint.
//
// This file adds the rate limiter and concurrency cap: the request-pacing
// block. It enforces two token-bucket tiers per request — one keyed per
// Grafana instance and one keyed per target domain — and a separate cap on the
// number of in-flight requests. Limits are expressed per minute and supplied
// by the caller as primitive integers; this package reads no settings and
// imports no project packages, exactly like the IP blocklist (SF1) and URL
// validator (SF2). The caller (a proxy endpoint) resolves the configured
// values from plugin settings and passes them in.
//
// Like the other blocks it leans fail-closed: a non-positive instance or
// domain limit disables that tier entirely (every request of that tier is
// denied), and a non-positive concurrency cap rejects every acquire. The
// limiter wires to nothing on its own.
package security

import (
	"sync"
	"time"
)

// Reason tokens returned when a request is rejected by the rate limiter or
// concurrency cap. These are short, stable, machine readable identifiers
// suitable for use as metric and audit-log labels. They must remain stable
// because consumers may key alerts and dashboards on them.
const (
	// ReasonRateInstance indicates the per-Grafana-instance rate limit was
	// exhausted.
	ReasonRateInstance = "rate-instance"
	// ReasonRateDomain indicates the per-target-domain rate limit was
	// exhausted (either the global per-domain default or a domain override).
	ReasonRateDomain = "rate-domain"
	// ReasonConcurrency indicates the maximum number of in-flight requests was
	// already reached.
	ReasonConcurrency = "concurrency"
)

// secondsPerMinute converts the caller's per-minute limits into the per-second
// refill rate used by the token buckets.
const secondsPerMinute = 60.0

// Clock abstracts the source of time so tests can advance it deterministically
// instead of sleeping. Production callers use the real monotonic clock via
// NewRateLimiter, which defaults to time.Now.
type Clock func() time.Time

// tokenBucket is a single classic token bucket. It refills continuously at
// refillPerSec tokens per second up to a maximum of burst tokens, and an
// allowed request consumes exactly one token. A bucket with refillPerSec <= 0
// or burst <= 0 is a "disabled" bucket that never has tokens and therefore
// denies every request (fail-closed for a non-positive configured limit).
//
// tokenBucket is not safe for concurrent use on its own; the owning
// RateLimiter serialises access under its mutex.
type tokenBucket struct {
	refillPerSec float64
	burst        float64
	tokens       float64
	last         time.Time
}

// newTokenBucket builds a bucket that starts full (tokens == burst) as of now.
func newTokenBucket(refillPerSec, burst float64, now time.Time) *tokenBucket {
	return &tokenBucket{
		refillPerSec: refillPerSec,
		burst:        burst,
		tokens:       burst,
		last:         now,
	}
}

// allow refills the bucket based on elapsed time since the last call and, if at
// least one whole token is available, consumes one and returns true. A bucket
// with a non-positive refill rate or burst always returns false.
func (b *tokenBucket) allow(now time.Time) bool {
	if b.refillPerSec <= 0 || b.burst <= 0 {
		return false
	}
	// Refill for the elapsed interval. A non-monotonic or backwards clock would
	// produce a negative delta; clamp it to zero so time never removes tokens.
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens += elapsed * b.refillPerSec
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// RateLimiter enforces two token-bucket tiers (per instance and per domain)
// plus an in-flight concurrency cap. It is safe for concurrent use by multiple
// goroutines.
//
// Buckets are created lazily on first use for each distinct key and retained
// for the lifetime of the limiter. The key space is bounded in practice by the
// number of Grafana instances and the configured set of allowed domains, so
// unbounded growth is not a concern for the intended use; full eviction of idle
// buckets is intentionally out of scope.
type RateLimiter struct {
	mu sync.Mutex

	// instancePerSec / instanceBurst configure the per-instance tier.
	instancePerSec float64
	instanceBurst  float64
	// domainPerSec / domainBurst configure the global per-domain tier (used
	// when a domain has no override).
	domainPerSec float64
	domainBurst  float64

	// domainOverridePerMin maps a normalised domain to a positive per-minute
	// override. A domain present here uses its own rate instead of the global
	// per-domain default. A 0 override means "use the global default" and is
	// dropped at construction.
	domainOverridePerMin map[string]int

	instanceBuckets map[string]*tokenBucket
	domainBuckets   map[string]*tokenBucket

	// maxConcurrent / inFlight implement the concurrency cap.
	maxConcurrent int
	inFlight      int

	now Clock
}

// NewRateLimiter constructs a limiter from per-minute limits.
//
//   - instancePerMin: maximum requests per minute across the whole Grafana
//     instance (e.g. RateLimitPerInstancePerMin, default 60).
//   - domainPerMin: maximum requests per minute to any single target domain
//     when that domain has no override (e.g. RateLimitPerDomainPerMin,
//     default 30).
//   - maxConcurrent: maximum in-flight requests (e.g. MaxConcurrentRequests,
//     default 10).
//   - domainOverridesPerMin: optional per-domain overrides keyed by normalised
//     hostname; a value of 0 (or a missing key) means "use domainPerMin". The
//     limiter copies the positive entries it needs, so the caller may reuse or
//     mutate the map afterwards.
//
// Limits are interpreted as both the sustained rate and the burst: a fresh
// bucket starts full, so up to instancePerMin/domainPerMin requests may pass
// immediately, after which the bucket refills at limit/60 tokens per second.
//
// Safe behaviour for non-positive inputs (fail-closed): a non-positive
// instancePerMin or domainPerMin disables that tier so every request of that
// tier is denied (with ReasonRateInstance / ReasonRateDomain); a non-positive
// per-domain override is ignored and the global per-domain default applies; a
// non-positive maxConcurrent causes every Acquire to be rejected. Supplying
// sensible positive defaults is the caller's responsibility.
func NewRateLimiter(instancePerMin, domainPerMin, maxConcurrent int, domainOverridesPerMin map[string]int) *RateLimiter {
	rl := &RateLimiter{
		instancePerSec:       float64(instancePerMin) / secondsPerMinute,
		instanceBurst:        float64(instancePerMin),
		domainPerSec:         float64(domainPerMin) / secondsPerMinute,
		domainBurst:          float64(domainPerMin),
		domainOverridePerMin: make(map[string]int),
		instanceBuckets:      make(map[string]*tokenBucket),
		domainBuckets:        make(map[string]*tokenBucket),
		maxConcurrent:        maxConcurrent,
		now:                  time.Now,
	}
	for domain, perMin := range domainOverridesPerMin {
		// A 0 (or negative) override means "use the global default"; only
		// positive overrides change behaviour, so drop the rest.
		if perMin > 0 {
			rl.domainOverridePerMin[domain] = perMin
		}
	}
	return rl
}

// WithClock overrides the time source. It is intended for deterministic tests;
// production code uses the default time.Now. It returns the receiver for
// chaining and must be called before the limiter is shared across goroutines.
func (rl *RateLimiter) WithClock(c Clock) *RateLimiter {
	if c != nil {
		rl.now = c
	}
	return rl
}

// Allow reports whether a request from the given Grafana instance to the given
// target domain may proceed under both rate-limit tiers. It checks the
// per-instance bucket first, then the per-domain bucket, and consumes a token
// from each tier only when that tier permits the request.
//
// On success it returns (true, ""). On rejection it returns (false, reason)
// where reason is a stable token identifying which tier denied the request:
// ReasonRateInstance or ReasonRateDomain. The instance tier is evaluated first,
// so if both tiers are exhausted the instance reason wins and no token is taken
// from the domain bucket (the request is rejected before it reaches that tier).
//
// Both keys are used verbatim; the caller is expected to pass a stable instance
// identifier and the already-normalised hostname produced by ValidateURL so
// that domain buckets line up with the allowlist's canonical form.
func (rl *RateLimiter) Allow(instanceID, domain string) (allowed bool, reason string) {
	now := rl.now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Per-instance tier first.
	ib := rl.instanceBuckets[instanceID]
	if ib == nil {
		ib = newTokenBucket(rl.instancePerSec, rl.instanceBurst, now)
		rl.instanceBuckets[instanceID] = ib
	}
	if !ib.allow(now) {
		return false, ReasonRateInstance
	}

	// Per-domain tier. A token has already been consumed from the instance
	// bucket above; that is acceptable because the instance limit is the
	// coarser, higher cap and a domain-limited request still counts against the
	// instance over the window.
	db := rl.domainBuckets[domain]
	if db == nil {
		perSec, burst := rl.domainPerSec, rl.domainBurst
		if override, ok := rl.domainOverridePerMin[domain]; ok {
			perSec = float64(override) / secondsPerMinute
			burst = float64(override)
		}
		db = newTokenBucket(perSec, burst, now)
		rl.domainBuckets[domain] = db
	}
	if !db.allow(now) {
		return false, ReasonRateDomain
	}

	return true, ""
}

// Acquire attempts to reserve one in-flight slot for a request. When fewer than
// maxConcurrent requests are in flight it increments the count and returns a
// release function (which must be called exactly once when the request
// completes) and ok == true. When the cap is already reached, or maxConcurrent
// is non-positive, it returns (nil, false) and the caller must reject the
// request (the stable reason token is ReasonConcurrency).
//
// The returned release function is idempotent: calling it more than once
// decrements the in-flight count only on the first call, guarding against
// double-release bugs from corrupting the count.
func (rl *RateLimiter) Acquire() (release func(), ok bool) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	if rl.maxConcurrent <= 0 || rl.inFlight >= rl.maxConcurrent {
		return nil, false
	}
	rl.inFlight++

	var once sync.Once
	return func() {
		once.Do(func() {
			rl.mu.Lock()
			defer rl.mu.Unlock()
			if rl.inFlight > 0 {
				rl.inFlight--
			}
		})
	}, true
}

// InFlight returns the current number of acquired-but-not-released in-flight
// slots. It is primarily a test and introspection helper.
func (rl *RateLimiter) InFlight() int {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	return rl.inFlight
}
