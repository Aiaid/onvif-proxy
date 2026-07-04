package mediautil

import (
	"sync"
	"time"
)

// entry is a cached value with its expiry time.
type entry struct {
	data    []byte
	expires time.Time
}

// call tracks an in-flight fetch so that concurrent Get calls for the same key
// share a single execution (singleflight semantics).
type call struct {
	wg   sync.WaitGroup
	data []byte
	err  error
}

// Cache is a TTL cache with singleflight de-duplication: concurrent Get calls
// for the same key trigger only one fetch and share its result, while calls for
// different keys proceed in parallel. Failed fetches are not cached. Expired
// entries are purged lazily on access.
type Cache struct {
	ttl   time.Duration
	mu    sync.Mutex
	items map[string]*entry
	calls map[string]*call
}

// NewCache returns a Cache whose successful entries live for ttl.
func NewCache(ttl time.Duration) *Cache {
	return &Cache{
		ttl:   ttl,
		items: make(map[string]*entry),
		calls: make(map[string]*call),
	}
}

// Get returns the cached value for key if present and unexpired. Otherwise it
// runs fetch (de-duplicated across concurrent callers) and, on success, caches
// the result for the configured TTL. Fetch errors are returned to every waiter
// but are not cached.
func (c *Cache) Get(key string, fetch func() ([]byte, error)) ([]byte, error) {
	c.mu.Lock()

	// Serve a live cache hit.
	if e, ok := c.items[key]; ok {
		if time.Now().Before(e.expires) {
			data := e.data
			c.mu.Unlock()
			return data, nil
		}
		// Expired: purge lazily.
		delete(c.items, key)
	}

	// Join an in-flight fetch for the same key.
	if cl, ok := c.calls[key]; ok {
		c.mu.Unlock()
		cl.wg.Wait()
		return cl.data, cl.err
	}

	// Become the leader for this key.
	cl := &call{}
	cl.wg.Add(1)
	c.calls[key] = cl
	c.mu.Unlock()

	data, err := fetch()

	c.mu.Lock()
	cl.data, cl.err = data, err
	delete(c.calls, key)
	if err == nil {
		c.items[key] = &entry{data: data, expires: time.Now().Add(c.ttl)}
	}
	cl.wg.Done()
	c.mu.Unlock()

	return data, err
}
