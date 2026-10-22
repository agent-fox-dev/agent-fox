// Package ttl is a bounded cache whose entries expire.
//
// Every entry of a cache lives for the same time, so the entries in the order
// they were stored are also in the order they expire, and an entry that has
// expired is always found at the front. That is what keeps a full cache cheap:
// to make room, it drops the expired entries from the front, and, if it is still
// full, the oldest live one. Nothing scans the whole cache.
//
// A cache is safe for concurrent use. It takes its clock as a function, so that
// a test moves time by moving the clock and never sleeps.
package ttl

import (
	"container/list"
	"sync"
	"time"
)

// Cache holds values by key for a time to live.
type Cache[K comparable, V any] struct {
	mu       sync.Mutex
	capacity int
	ttl      time.Duration
	now      func() time.Time
	items    map[K]*list.Element
	order    *list.List // of *item, the oldest stored first
}

// item is an entry.
type item[K comparable, V any] struct {
	key    K
	value  V
	stored time.Time
}

// New makes a cache of at most capacity entries (one, if capacity is less) that
// live for ttl by the clock now (time.Now when it is nil).
func New[K comparable, V any](capacity int, ttl time.Duration, now func() time.Time) *Cache[K, V] {
	if now == nil {
		now = time.Now
	}
	return &Cache[K, V]{
		capacity: max(capacity, 1),
		ttl:      ttl,
		now:      now,
		items:    map[K]*list.Element{},
		order:    list.New(),
	}
}

// Get is the value of the key, if it is there and has lived less than the TTL.
// An entry that has expired is removed.
func (c *Cache[K, V]) Get(k K) (v V, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, found := c.items[k]
	if !found {
		return v, false
	}
	it := el.Value.(*item[K, V])
	if c.expired(it, c.now()) {
		c.remove(el)
		return v, false
	}
	return it.value, true
}

// Put stores the value under the key, with a TTL that starts now. When the cache
// is full and the key is new, room is made first: the expired entries go, and
// if there are none, the oldest.
func (c *Cache[K, V]) Put(k K, v V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if el, found := c.items[k]; found {
		it := el.Value.(*item[K, V])
		it.value, it.stored = v, now
		c.order.MoveToBack(el)
		return
	}
	if len(c.items) >= c.capacity {
		for front := c.order.Front(); front != nil && c.expired(front.Value.(*item[K, V]), now); front = c.order.Front() {
			c.remove(front)
		}
		if len(c.items) >= c.capacity {
			c.remove(c.order.Front())
		}
	}
	c.items[k] = c.order.PushBack(&item[K, V]{key: k, value: v, stored: now})
}

// Delete removes the key.
func (c *Cache[K, V]) Delete(k K) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, found := c.items[k]; found {
		c.remove(el)
	}
}

// DeleteIf removes every entry that the predicate says to, expired or not, and
// returns how many it removed. The predicate runs with the cache locked, so it
// must not call the cache.
func (c *Cache[K, V]) DeleteIf(pred func(K, V) bool) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	removed := 0
	for el := c.order.Front(); el != nil; {
		next := el.Next()
		if it := el.Value.(*item[K, V]); pred(it.key, it.value) {
			c.remove(el)
			removed++
		}
		el = next
	}
	return removed
}

// Len is the number of entries held. An entry that has expired and has not been
// found or made room for yet is still held.
func (c *Cache[K, V]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// expired reports whether the entry has lived its time. The caller holds the lock.
func (c *Cache[K, V]) expired(it *item[K, V], now time.Time) bool {
	return now.Sub(it.stored) >= c.ttl
}

// remove drops an entry. The caller holds the lock.
func (c *Cache[K, V]) remove(el *list.Element) {
	delete(c.items, el.Value.(*item[K, V]).key)
	c.order.Remove(el)
}
