package ttl_test

import (
	"fmt"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/service/ttl"
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

func has[V any](c *ttl.Cache[string, V], key string) bool {
	_, ok := c.Get(key)
	return ok
}

func TestGet_HitsUntilTheTTLAndRemovesTheExpiredEntry(t *testing.T) {
	clk := newClock()
	c := ttl.New[string, int](10, time.Minute, clk.Now)
	c.Put("k", 7)
	if v, ok := c.Get("k"); !ok || v != 7 {
		t.Fatalf("Get right after Put = %d, %v", v, ok)
	}
	clk.Advance(time.Minute - time.Millisecond)
	if !has(c, "k") {
		t.Error("a miss 1ms before the TTL")
	}
	clk.Advance(time.Millisecond)
	if has(c, "k") || c.Len() != 0 {
		t.Errorf("at the TTL: want a miss and the entry removed (Len %d)", c.Len())
	}
	if has(c, "never") {
		t.Error("a key never stored is a hit")
	}
}

func TestPut_AgainRenewsTheEntry(t *testing.T) {
	clk := newClock()
	c := ttl.New[string, int](10, time.Minute, clk.Now)
	c.Put("k", 1)
	clk.Advance(40 * time.Second)
	c.Put("k", 2)
	clk.Advance(40 * time.Second)
	if v, ok := c.Get("k"); !ok || v != 2 || c.Len() != 1 {
		t.Errorf("Get = %d, %v (Len %d), want the renewed value 2 80s after the first Put", v, ok, c.Len())
	}
}

func TestPut_IntoAFullCacheDropsTheExpiredThenTheOldest(t *testing.T) {
	clk := newClock()
	const capacity = 1000
	c := ttl.New[string, int](capacity, time.Minute, clk.Now)
	for i := range capacity {
		c.Put(fmt.Sprintf("old-%d", i), i)
	}
	clk.Advance(30 * time.Second)
	c.Put("fresh", 1) // full, nothing has expired: the oldest goes
	if c.Len() != capacity || has(c, "old-0") || !has(c, "old-1") || !has(c, "fresh") {
		t.Errorf("Len %d; want old-0 alone evicted", c.Len())
	}
	clk.Advance(31 * time.Second) // the old ones are 61s old, fresh is 31s
	c.Put("new", 2)
	if !has(c, "fresh") || !has(c, "new") || c.Len() != 2 {
		t.Errorf("Len %d; want every expired entry dropped to make room and the live ones kept", c.Len())
	}
}

func TestNew_HoldsAtLeastOneEntryAndDefaultsToTheSystemClock(t *testing.T) {
	c := ttl.New[string, int](0, time.Hour, nil)
	c.Put("a", 1)
	c.Put("b", 2)
	if c.Len() != 1 || !has(c, "b") {
		t.Errorf("Len %d; want a capacity of one holding the newest", c.Len())
	}
}

func TestDelete_RemovesTheKeyAndFreesItsSlot(t *testing.T) {
	c := ttl.New[string, int](2, time.Hour, newClock().Now)
	c.Put("a", 1)
	c.Put("b", 2)
	c.Delete("a")
	c.Delete("absent")
	c.Put("c", 3)
	if has(c, "a") || !has(c, "b") || !has(c, "c") {
		t.Error("want a gone, and b and c held in the slot a freed")
	}
}

func TestDeleteIf_RemovesWhatThePredicateSaysAndCountsIt(t *testing.T) {
	c := ttl.New[string, int](10, time.Hour, newClock().Now)
	for i, k := range []string{"x:1", "x:2", "y:1", "y:2"} {
		c.Put(k, i)
	}
	if n := c.DeleteIf(func(k string, _ int) bool { return k[0] == 'x' }); n != 2 {
		t.Errorf("DeleteIf by key removed %d, want 2", n)
	}
	if n := c.DeleteIf(func(_ string, v int) bool { return v == 3 }); n != 1 {
		t.Errorf("DeleteIf by value removed %d, want 1", n)
	}
	if c.Len() != 1 || !has(c, "y:1") {
		t.Errorf("Len = %d, want only y:1 left", c.Len())
	}
}

// TestCache_AgreesWithAMapOfStoreTimes runs random operations against a model of what each key should answer.
func TestCache_AgreesWithAMapOfStoreTimes(t *testing.T) {
	rng := rand.New(rand.NewPCG(11, 45))
	const life = 10 * time.Second
	clk := newClock()
	c := ttl.New[string, int](1000, life, clk.Now) // more than the keys: nothing is evicted for room
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
			live := in && clk.Now().Sub(m.at) < life
			if ok != live || ok && got != m.value {
				t.Fatalf("step %d: Get(%s) = %d, %v; the model says %d, %v", step, key, got, ok, m.value, live)
			}
		case op == 8:
			c.Delete(key)
			delete(model, key)
		default:
			clk.Advance(time.Duration(rng.IntN(4000)) * time.Millisecond)
		}
	}
}

func TestCache_StaysWithinItsBoundUnderConcurrentUse(t *testing.T) {
	clk := newClock()
	const bound, life = 500, 5 * time.Second
	c := ttl.New[string, int](bound, life, clk.Now)
	var wg sync.WaitGroup
	var over atomic.Int64
	for g := range 16 {
		wg.Go(func() {
			rng := rand.New(rand.NewPCG(11, uint64(1000+g)))
			for range 5000 {
				key := fmt.Sprintf("g%d-k%d", g, rng.IntN(200))
				switch op := rng.IntN(20); {
				case op < 8:
					c.Put(key, g)
				case op < 14:
					c.Get(key)
				case op == 14:
					c.Delete(key)
				case op == 15:
					c.DeleteIf(func(k string, _ int) bool { return k == key })
				case op < 18:
					clk.Advance(time.Duration(rng.IntN(300)) * time.Millisecond)
				default:
					if n := c.Len(); n > bound {
						over.Store(int64(n))
					}
				}
			}
		})
	}
	wg.Wait()
	if n := over.Load(); n != 0 || c.Len() > bound {
		t.Errorf("the cache held %d entries (final %d), want at most %d", n, c.Len(), bound)
	}
	clk.Advance(life)
	for g := range 16 {
		for k := range 200 {
			if key := fmt.Sprintf("g%d-k%d", g, k); has(c, key) {
				t.Fatalf("%s is a hit a full TTL after the last operation", key)
			}
		}
	}
}
