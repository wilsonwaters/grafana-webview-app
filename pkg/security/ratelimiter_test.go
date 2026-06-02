package security

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a deterministic Clock for tests. advance moves the reported time
// forward; it is safe for concurrent use so the race test can read it from many
// goroutines while another advances it.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(0, 0)}
}

// clock returns the Clock func bound to this fakeClock.
func (c *fakeClock) clock() Clock {
	return func() time.Time {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.now
	}
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// newTestLimiter builds a limiter wired to a fake clock for deterministic tests.
func newTestLimiter(instancePerMin, domainPerMin, maxConcurrent int, overrides map[string]int, clk *fakeClock) *RateLimiter {
	return NewRateLimiter(instancePerMin, domainPerMin, maxConcurrent, overrides).WithClock(clk.clock())
}

// TestInstanceRateLimitBurstThenDeny covers completion criterion "Token bucket
// enforces requests-per-second limits per instance": the per-instance bucket
// allows up to its per-minute burst then denies with ReasonRateInstance.
func TestInstanceRateLimitBurstThenDeny(t *testing.T) {
	clk := newFakeClock()
	// Instance budget 5/min; domain budget large so it never interferes.
	rl := newTestLimiter(5, 1000, 0, nil, clk)

	for i := 0; i < 5; i++ {
		allowed, reason := rl.Allow("inst-1", "example.com")
		if !allowed {
			t.Fatalf("request %d: expected allow within burst, got deny reason=%q", i, reason)
		}
	}
	allowed, reason := rl.Allow("inst-1", "example.com")
	if allowed {
		t.Fatalf("expected deny after burst exhausted")
	}
	if reason != ReasonRateInstance {
		t.Fatalf("expected reason %q, got %q", ReasonRateInstance, reason)
	}
}

// TestInstanceRateRefill verifies sustained-rate refill: after the burst is
// spent, advancing the clock replenishes tokens at limit/60 per second.
func TestInstanceRateRefill(t *testing.T) {
	clk := newFakeClock()
	rl := newTestLimiter(60, 1000, 0, nil, clk) // 60/min == 1 token/sec

	// Drain the full 60-token burst.
	for i := 0; i < 60; i++ {
		if allowed, _ := rl.Allow("inst-1", "d"); !allowed {
			t.Fatalf("request %d unexpectedly denied during burst", i)
		}
	}
	if allowed, _ := rl.Allow("inst-1", "d"); allowed {
		t.Fatalf("expected deny with empty bucket")
	}

	// One second at 1 token/sec refills exactly one token.
	clk.advance(time.Second)
	if allowed, _ := rl.Allow("inst-1", "d"); !allowed {
		t.Fatalf("expected allow after 1s refill")
	}
	if allowed, _ := rl.Allow("inst-1", "d"); allowed {
		t.Fatalf("expected deny: only one token should have refilled")
	}
}

// TestDomainRateLimitIndependent covers "enforces limits per domain": the
// per-domain bucket denies with ReasonRateDomain, and distinct domains have
// independent budgets.
func TestDomainRateLimitIndependent(t *testing.T) {
	clk := newFakeClock()
	// Instance budget large; domain budget 3/min.
	rl := newTestLimiter(1000, 3, 0, nil, clk)

	for i := 0; i < 3; i++ {
		if allowed, _ := rl.Allow("inst-1", "a.com"); !allowed {
			t.Fatalf("a.com request %d unexpectedly denied", i)
		}
	}
	allowed, reason := rl.Allow("inst-1", "a.com")
	if allowed || reason != ReasonRateDomain {
		t.Fatalf("expected deny ReasonRateDomain, got allowed=%v reason=%q", allowed, reason)
	}

	// A different domain has its own fresh bucket.
	if allowed, _ := rl.Allow("inst-1", "b.com"); !allowed {
		t.Fatalf("b.com should have an independent bucket and be allowed")
	}
}

// TestSeparateInstancesIndependent confirms distinct instance keys do not share
// a bucket.
func TestSeparateInstancesIndependent(t *testing.T) {
	clk := newFakeClock()
	rl := newTestLimiter(2, 1000, 0, nil, clk)

	for i := 0; i < 2; i++ {
		if allowed, _ := rl.Allow("inst-A", "d"); !allowed {
			t.Fatalf("inst-A request %d denied", i)
		}
	}
	if allowed, _ := rl.Allow("inst-A", "d"); allowed {
		t.Fatalf("inst-A should be exhausted")
	}
	// inst-B is independent and still has its full budget.
	if allowed, _ := rl.Allow("inst-B", "d"); !allowed {
		t.Fatalf("inst-B should have an independent bucket")
	}
}

// TestDomainOverrideApplied covers "Settings values override defaults": a
// per-domain override replaces the global per-domain budget for just that
// domain, while other domains keep the default.
func TestDomainOverrideApplied(t *testing.T) {
	clk := newFakeClock()
	// Default domain budget 2/min; override d-big to 5/min (and a 0 override
	// for d-zero which must be ignored, falling back to the default of 2).
	overrides := map[string]int{"d-big": 5, "d-zero": 0}
	rl := newTestLimiter(1000, 2, 0, overrides, clk)

	// Overridden domain allows 5 before denying.
	for i := 0; i < 5; i++ {
		if allowed, _ := rl.Allow("inst-1", "d-big"); !allowed {
			t.Fatalf("overridden domain request %d denied", i)
		}
	}
	if allowed, reason := rl.Allow("inst-1", "d-big"); allowed || reason != ReasonRateDomain {
		t.Fatalf("expected deny ReasonRateDomain after override budget spent, got allowed=%v reason=%q", allowed, reason)
	}

	// A domain using the default still gets only 2.
	for i := 0; i < 2; i++ {
		if allowed, _ := rl.Allow("inst-1", "d-default"); !allowed {
			t.Fatalf("default domain request %d denied", i)
		}
	}
	if allowed, _ := rl.Allow("inst-1", "d-default"); allowed {
		t.Fatalf("default domain should be limited to 2")
	}

	// The 0-valued override is dropped, so d-zero also uses the default of 2.
	for i := 0; i < 2; i++ {
		if allowed, _ := rl.Allow("inst-1", "d-zero"); !allowed {
			t.Fatalf("d-zero request %d denied; 0 override should fall back to default", i)
		}
	}
	if allowed, _ := rl.Allow("inst-1", "d-zero"); allowed {
		t.Fatalf("d-zero should use the default budget of 2")
	}
}

// TestNonPositiveLimitsFailClosed documents the safe behaviour: a non-positive
// instance or domain limit disables that tier and denies every request.
func TestNonPositiveLimitsFailClosed(t *testing.T) {
	clk := newFakeClock()

	// Instance tier disabled -> instance reason, denied immediately.
	rlInst := newTestLimiter(0, 1000, 0, nil, clk)
	if allowed, reason := rlInst.Allow("i", "d"); allowed || reason != ReasonRateInstance {
		t.Fatalf("disabled instance tier should deny with ReasonRateInstance, got allowed=%v reason=%q", allowed, reason)
	}

	// Domain tier disabled -> instance passes, domain denies.
	rlDom := newTestLimiter(1000, 0, 0, nil, clk)
	if allowed, reason := rlDom.Allow("i", "d"); allowed || reason != ReasonRateDomain {
		t.Fatalf("disabled domain tier should deny with ReasonRateDomain, got allowed=%v reason=%q", allowed, reason)
	}
}

// TestConcurrencyCapRejectsAndRecovers covers "Concurrency cap rejects requests
// beyond the configured maximum": Acquire succeeds up to max, rejects beyond,
// then recovers after a release.
func TestConcurrencyCapRejectsAndRecovers(t *testing.T) {
	clk := newFakeClock()
	rl := newTestLimiter(1000, 1000, 2, nil, clk)

	r1, ok1 := rl.Acquire()
	r2, ok2 := rl.Acquire()
	if !ok1 || !ok2 {
		t.Fatalf("first two acquires should succeed, got %v %v", ok1, ok2)
	}
	if rl.InFlight() != 2 {
		t.Fatalf("expected 2 in flight, got %d", rl.InFlight())
	}
	if _, ok3 := rl.Acquire(); ok3 {
		t.Fatalf("third acquire should be rejected at the cap")
	}

	// Release one slot; a new acquire should now succeed.
	r1()
	if rl.InFlight() != 1 {
		t.Fatalf("expected 1 in flight after release, got %d", rl.InFlight())
	}
	r3, ok3 := rl.Acquire()
	if !ok3 {
		t.Fatalf("acquire should succeed after a slot was freed")
	}
	r2()
	r3()
	if rl.InFlight() != 0 {
		t.Fatalf("expected 0 in flight after all released, got %d", rl.InFlight())
	}
}

// TestReleaseIdempotent ensures calling release twice frees only one slot.
func TestReleaseIdempotent(t *testing.T) {
	clk := newFakeClock()
	rl := newTestLimiter(1000, 1000, 1, nil, clk)

	release, ok := rl.Acquire()
	if !ok {
		t.Fatalf("acquire should succeed")
	}
	release()
	release() // second call must be a no-op
	if rl.InFlight() != 0 {
		t.Fatalf("expected 0 in flight, got %d", rl.InFlight())
	}
	// Capacity is restored to exactly 1, not 2.
	r1, ok1 := rl.Acquire()
	if !ok1 {
		t.Fatalf("acquire should succeed after release")
	}
	if _, ok2 := rl.Acquire(); ok2 {
		t.Fatalf("double-release must not have inflated capacity beyond 1")
	}
	r1()
}

// TestConcurrencyCapNonPositiveRejects documents that a non-positive max rejects
// every acquire (fail-closed).
func TestConcurrencyCapNonPositiveRejects(t *testing.T) {
	clk := newFakeClock()
	rl := newTestLimiter(1000, 1000, 0, nil, clk)
	if _, ok := rl.Acquire(); ok {
		t.Fatalf("non-positive max should reject every acquire")
	}
}

// TestConfigOverridesDefaults covers "Settings values override defaults
// correctly" at the constructor level: a limiter built with explicit values
// enforces exactly those values rather than any built-in default.
func TestConfigOverridesDefaults(t *testing.T) {
	clk := newFakeClock()
	// Deliberately small, non-default values.
	rl := newTestLimiter(1, 1, 1, nil, clk)

	if allowed, _ := rl.Allow("i", "d"); !allowed {
		t.Fatalf("first request within budget should pass")
	}
	if allowed, _ := rl.Allow("i", "d"); allowed {
		t.Fatalf("instance budget of 1 should now be exhausted")
	}

	r, ok := rl.Acquire()
	if !ok {
		t.Fatalf("first acquire should succeed for max=1")
	}
	if _, ok2 := rl.Acquire(); ok2 {
		t.Fatalf("max=1 should reject the second concurrent acquire")
	}
	r()
}

// TestConcurrentAccessRaceFree exercises Allow and Acquire from many goroutines
// to surface data races under `go test -race`, and asserts the concurrency cap
// is never exceeded.
func TestConcurrentAccessRaceFree(t *testing.T) {
	clk := newFakeClock()
	const maxConc = 8
	rl := newTestLimiter(100000, 100000, maxConc, map[string]int{"dom": 100000}, clk)

	var wg sync.WaitGroup
	var peak int64
	var current int64

	for g := 0; g < 50; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				rl.Allow("inst", "dom")
				if release, ok := rl.Acquire(); ok {
					n := atomic.AddInt64(&current, 1)
					for {
						p := atomic.LoadInt64(&peak)
						if n <= p || atomic.CompareAndSwapInt64(&peak, p, n) {
							break
						}
					}
					atomic.AddInt64(&current, -1)
					release()
				}
				clk.advance(time.Millisecond)
			}
		}()
	}
	wg.Wait()

	if p := atomic.LoadInt64(&peak); p > maxConc {
		t.Fatalf("concurrency cap exceeded: peak %d > max %d", p, maxConc)
	}
	if rl.InFlight() != 0 {
		t.Fatalf("expected 0 in flight after all goroutines done, got %d", rl.InFlight())
	}
}
