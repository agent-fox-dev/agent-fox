package ttl

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// clock is a clock that only the test moves.
type clock struct{ nanos atomic.Int64 }

func newClock() *clock {
	c := &clock{}
	c.nanos.Store(time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC).UnixNano())
	return c
}

func (c *clock) Now() time.Time          { return time.Unix(0, c.nanos.Load()).UTC() }
func (c *clock) Advance(d time.Duration) { c.nanos.Add(int64(d)) }

const capacity = 10_000

// has reports whether the key is a hit.
func has[V any](c *Cache[string, V], key string) bool {
	_, ok := c.Get(key)
	return ok
}

// TestEntriesExpireByTheInjectedClockOldestGoFirst_TS_11_45 verifies TS-11-45:
// The TTL cache expires by an injected clock, drops expired entries first and
// then the oldest when full.
// Verifies: 11-REQ-6.8
func TestEntriesExpireByTheInjectedClockOldestGoFirst_TS_11_45(t *testing.T) {
	t.Run("an entry is a hit until its TTL and a miss after", func(t *testing.T) {
		clk := newClock()
		c := New[string, int](capacity, 60*time.Second, clk.Now)
		c.Put("k", 7)
		if v, ok := c.Get("k"); !ok || v != 7 {
			t.Fatalf("Get right after Put = %d, %v", v, ok)
		}
		clk.Advance(59*time.Second + 999*time.Millisecond)
		if !has(c, "k") {
			t.Error("a miss 1ms before the TTL")
		}
		clk.Advance(time.Millisecond)
		if has(c, "k") {
			t.Error("a hit at the TTL, want a miss")
		}
		if c.Len() != 0 {
			t.Errorf("Len = %d after the expired entry was found, want it purged", c.Len())
		}
	})

	t.Run("putting again renews the entry", func(t *testing.T) {
		clk := newClock()
		c := New[string, int](capacity, time.Minute, clk.Now)
		c.Put("k", 1)
		clk.Advance(40 * time.Second)
		c.Put("k", 2)
		clk.Advance(40 * time.Second)
		if v, ok := c.Get("k"); !ok || v != 2 {
			t.Errorf("Get = %d, %v, want the renewed value 2 80s after the first Put", v, ok)
		}
		if c.Len() != 1 {
			t.Errorf("Len = %d, want 1", c.Len())
		}
	})

	t.Run("expired entries go before any live one, then the oldest", func(t *testing.T) {
		clk := newClock()
		c := New[string, int](capacity, 60*time.Second, clk.Now)
		for i := range capacity {
			c.Put(fmt.Sprintf("old-%d", i), i)
		}
		if c.Len() != capacity {
			t.Fatalf("Len = %d, want a full cache", c.Len())
		}
		clk.Advance(30 * time.Second)
		c.Put("fresh", 1) // full, nothing has expired: the oldest live entry goes
		if c.Len() != capacity {
			t.Errorf("Len = %d after a Put into a full cache, want %d", c.Len(), capacity)
		}
		if has(c, "old-0") {
			t.Error("the oldest live entry survived a Put into a full cache")
		}
		if !has(c, "old-1") || !has(c, "fresh") {
			t.Error("an entry other than the oldest was evicted")
		}

		clk.Advance(31 * time.Second) // the old ones are 61s old, fresh is 31s
		c.Put("new", 2)
		if !has(c, "fresh") || !has(c, "new") {
			t.Error("a live entry was evicted while expired ones were there")
		}
		if has(c, fmt.Sprintf("old-%d", capacity-1)) {
			t.Error("an expired entry is a hit")
		}
		if c.Len() > capacity {
			t.Errorf("Len = %d, over the capacity %d", c.Len(), capacity)
		}
		// The expired ones were purged to make room, not one by one.
		if c.Len() > 3 {
			t.Errorf("Len = %d, want the expired entries purged when room was needed", c.Len())
		}
	})

	t.Run("a small cache keeps the newest", func(t *testing.T) {
		clk := newClock()
		c := New[string, int](3, time.Hour, clk.Now)
		for i, k := range []string{"a", "b", "c", "d", "e"} {
			c.Put(k, i)
			clk.Advance(time.Second)
		}
		for k, want := range map[string]bool{"a": false, "b": false, "c": true, "d": true, "e": true} {
			if has(c, k) != want {
				t.Errorf("has(%s) = %v, want %v", k, !want, want)
			}
		}
	})

	t.Run("Delete and DeleteIf", func(t *testing.T) {
		clk := newClock()
		c := New[string, int](capacity, time.Hour, clk.Now)
		for i, k := range []string{"x:1", "x:2", "y:1", "y:2", "z"} {
			c.Put(k, i)
		}
		c.Delete("z")
		c.Delete("absent")
		if n := c.DeleteIf(func(k string, v int) bool { return k[0] == 'x' }); n != 2 {
			t.Errorf("DeleteIf removed %d, want 2", n)
		}
		if n := c.DeleteIf(func(k string, v int) bool { return v == 3 }); n != 1 || has(c, "y:2") {
			t.Errorf("DeleteIf by value removed %d", n)
		}
		if c.Len() != 1 || !has(c, "y:1") {
			t.Errorf("Len = %d, want only y:1 left", c.Len())
		}
		// The deleted slots are free: the capacity is not used up by them.
		small := New[string, int](2, time.Hour, clk.Now)
		small.Put("a", 1)
		small.Put("b", 2)
		small.Delete("a")
		small.Put("c", 3)
		if !has(small, "b") || !has(small, "c") {
			t.Error("a Put after a Delete evicted a live entry")
		}
	})
}

// TestTheCacheStaysWithinItsBoundAndIsRaceFree_TS_11_46 verifies TS-11-46:
// The TTL cache stays within its bound and is race-free under concurrent use.
// Verifies: 11-REQ-6.8
func TestTheCacheStaysWithinItsBoundAndIsRaceFree_TS_11_46(t *testing.T) {
	t.Run("against a model, one goroutine", func(t *testing.T) {
		rng := rand.New(rand.NewPCG(11, 46))
		t.Logf("seed: PCG(11, 46)")
		const ttl = 10 * time.Second
		clk := newClock()
		c := New[string, int](1000, ttl, clk.Now) // more than the keys: nothing is evicted for room
		type stored struct {
			value int
			at    time.Time
		}
		model := map[string]stored{}
		for step := range 20_000 {
			key := fmt.Sprintf("k%d", rng.IntN(200))
			switch op := rng.IntN(10); {
			case op < 4:
				c.Put(key, step)
				model[key] = stored{step, clk.Now()}
			case op < 8:
				got, ok := c.Get(key)
				m, in := model[key]
				live := in && clk.Now().Sub(m.at) < ttl
				if ok != live || ok && got != m.value {
					t.Fatalf("step %d: Get(%s) = %d, %v; the model says %v (stored %v at %v, now %v)", step, key, got, ok, live, m.value, m.at, clk.Now())
				}
			case op == 8:
				c.Delete(key)
				delete(model, key)
			default:
				clk.Advance(time.Duration(rng.IntN(4000)) * time.Millisecond)
			}
		}
	})

	t.Run("sixteen goroutines over more keys than the capacity", func(t *testing.T) {
		clk := newClock()
		const bound, ttl = 500, 5 * time.Second
		c := New[string, int](bound, ttl, clk.Now)
		var wg sync.WaitGroup
		var over atomic.Int64
		for g := range 16 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				rng := rand.New(rand.NewPCG(11, uint64(1000+g)))
				// Each goroutine has keys of its own, so that it knows when each was
				// last stored: the clock after Put returned is never earlier than the
				// one the cache stored. A hit that is older than the TTL by the clock
				// from before the Get, and the clock only moves forward, is a bug.
				storedBy := map[string]time.Time{}
				for range 5000 {
					key := fmt.Sprintf("g%d-k%d", g, rng.IntN(200))
					switch op := rng.IntN(20); {
					case op < 8:
						c.Put(key, g)
						storedBy[key] = clk.Now()
					case op < 14:
						before := clk.Now()
						if _, ok := c.Get(key); ok {
							if at, known := storedBy[key]; !known || before.Sub(at) >= ttl {
								t.Errorf("Get(%s) hit an entry stored %v before a clock reading (stored by this goroutine: %v)", key, before.Sub(at), known)
							}
						}
					case op == 14:
						c.Delete(key)
						delete(storedBy, key)
					case op == 15:
						c.DeleteIf(func(k string, _ int) bool { return k == key })
						delete(storedBy, key)
					case op < 18:
						clk.Advance(time.Duration(rng.IntN(300)) * time.Millisecond)
					default:
						if n := c.Len(); n > bound {
							over.Store(int64(n))
						}
					}
				}
			}()
		}
		wg.Wait()
		if n := over.Load(); n != 0 || c.Len() > bound {
			t.Errorf("the cache held %d entries (final %d), want at most %d", n, c.Len(), bound)
		}
		clk.Advance(ttl)
		for g := range 16 {
			for k := range 200 {
				if key := fmt.Sprintf("g%d-k%d", g, k); has(c, key) {
					t.Fatalf("%s is a hit a full TTL after the last operation", key)
				}
			}
		}
	})
}
