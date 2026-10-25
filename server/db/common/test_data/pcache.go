package test_data

import (
	"fmt"
	"sync"
	"testing"

	db "github.com/tinode/chat/server/db"
	"github.com/tinode/chat/server/store/types"
)

// CheckPCacheAtomicOperations verifies conditional cache writes against a live adapter.
func CheckPCacheAtomicOperations(t *testing.T, adapter db.Adapter) {
	t.Helper()
	const key, original = "atomic_cache_test", "CaseSensitiveValue"
	if err := adapter.PCacheDelete(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := adapter.PCacheDelete(key); err != nil {
			t.Error(err)
		}
	})
	if changed, err := adapter.PCacheCompareAndSwap(key, original, "replacement"); err != nil || changed {
		t.Fatalf("swap created a missing record: %v, %v", changed, err)
	}
	if changed, err := adapter.PCacheCompareAndDelete(key, original); err != nil || changed {
		t.Fatalf("delete matched a missing record: %v, %v", changed, err)
	}
	if err := adapter.PCacheUpsert(key, original, true); err != nil {
		t.Fatal(err)
	}
	// Values contain case-sensitive user IDs, even with case-insensitive SQL collations.
	if changed, err := adapter.PCacheCompareAndSwap(key, "casesensitivevalue", "wrong"); err != nil || changed {
		t.Fatalf("swap ignored value case: %v, %v", changed, err)
	}
	if changed, err := adapter.PCacheCompareAndDelete(key, "casesensitivevalue"); err != nil || changed {
		t.Fatalf("delete ignored value case: %v, %v", changed, err)
	}

	type result struct {
		value   string
		changed bool
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 16)
	var workers sync.WaitGroup
	for i := range cap(results) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			value := fmt.Sprintf("winner-%d", i)
			changed, err := adapter.PCacheCompareAndSwap(key, original, value)
			results <- result{value, changed, err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	var winner string
	winners := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.changed {
			winners++
			winner = result.value
		}
	}
	if winners != 1 {
		t.Fatalf("got %d winners, want 1", winners)
	}
	if value, err := adapter.PCacheGet(key); err != nil || value != winner {
		t.Fatalf("winning value lost: %q, %v", value, err)
	}
	if changed, err := adapter.PCacheCompareAndDelete(key, original); err != nil || changed {
		t.Fatalf("stale delete removed the winner: %v, %v", changed, err)
	}
	if changed, err := adapter.PCacheCompareAndDelete(key, winner); err != nil || !changed {
		t.Fatalf("matching delete failed: %v, %v", changed, err)
	}
	if _, err := adapter.PCacheGet(key); err != types.ErrNotFound {
		t.Fatalf("deleted record still readable: %v", err)
	}
}
