package mediautil

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestCacheHitWithinTTL(t *testing.T) {
	c := NewCache(time.Minute)
	var calls atomic.Int32
	fetch := func() ([]byte, error) {
		calls.Add(1)
		return []byte("value"), nil
	}

	for i := 0; i < 3; i++ {
		got, err := c.Get("k", fetch)
		if err != nil {
			t.Fatalf("Get returned error: %v", err)
		}
		if string(got) != "value" {
			t.Fatalf("Get = %q, want %q", got, "value")
		}
	}
	if n := calls.Load(); n != 1 {
		t.Fatalf("fetch ran %d times, want 1", n)
	}
}

func TestCacheExpiry(t *testing.T) {
	c := NewCache(20 * time.Millisecond)
	var calls atomic.Int32
	fetch := func() ([]byte, error) {
		calls.Add(1)
		return []byte("v"), nil
	}

	if _, err := c.Get("k", fetch); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	if _, err := c.Get("k", fetch); err != nil {
		t.Fatal(err)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("fetch ran %d times, want 2 (expired entry should refetch)", n)
	}
}

func TestCacheSingleflight(t *testing.T) {
	c := NewCache(time.Minute)
	var calls atomic.Int32
	start := make(chan struct{})
	fetch := func() ([]byte, error) {
		calls.Add(1)
		// Hold the fetch so all goroutines pile up on the same in-flight call.
		time.Sleep(50 * time.Millisecond)
		return []byte("shared"), nil
	}

	const n = 10
	var wg sync.WaitGroup
	results := make([][]byte, n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			results[idx], errs[idx] = c.Get("same", fetch)
		}(i)
	}
	close(start)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Fatalf("concurrent Get on same key ran fetch %d times, want 1", got)
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("Get[%d] error: %v", i, errs[i])
		}
		if string(results[i]) != "shared" {
			t.Fatalf("Get[%d] = %q, want %q", i, results[i], "shared")
		}
	}
}

func TestCacheDifferentKeysParallel(t *testing.T) {
	c := NewCache(time.Minute)
	var calls atomic.Int32
	fetch := func() ([]byte, error) {
		calls.Add(1)
		return []byte("v"), nil
	}
	if _, err := c.Get("a", fetch); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get("b", fetch); err != nil {
		t.Fatal(err)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("fetch ran %d times, want 2 (distinct keys)", n)
	}
}

func TestCacheDoesNotCacheFailures(t *testing.T) {
	c := NewCache(time.Minute)
	var calls atomic.Int32
	wantErr := errors.New("boom")
	failing := func() ([]byte, error) {
		calls.Add(1)
		return nil, wantErr
	}

	if _, err := c.Get("k", failing); !errors.Is(err, wantErr) {
		t.Fatalf("first Get err = %v, want %v", err, wantErr)
	}
	// A failed fetch must not be cached: the next call refetches.
	if _, err := c.Get("k", failing); !errors.Is(err, wantErr) {
		t.Fatalf("second Get err = %v, want %v", err, wantErr)
	}
	if n := calls.Load(); n != 2 {
		t.Fatalf("fetch ran %d times, want 2 (failures must not be cached)", n)
	}

	// After a failure, a successful fetch should populate the cache.
	var okCalls atomic.Int32
	ok := func() ([]byte, error) {
		okCalls.Add(1)
		return []byte("good"), nil
	}
	for i := 0; i < 2; i++ {
		got, err := c.Get("k", ok)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "good" {
			t.Fatalf("Get = %q, want %q", got, "good")
		}
	}
	if n := okCalls.Load(); n != 1 {
		t.Fatalf("successful fetch ran %d times, want 1", n)
	}
}
