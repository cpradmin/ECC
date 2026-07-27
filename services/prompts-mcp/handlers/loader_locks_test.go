package handlers

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"
)

// Tests for PromptLoader write lock management

func TestWriteLockAcquireRelease(t *testing.T) {
	loader := NewPromptLoader(t.TempDir())
	ctx := context.Background()

	// Acquire lock
	if err := loader.acquireWrite(ctx); err != nil {
		t.Fatalf("failed to acquire lock: %v", err)
	}

	// Lock should be held (non-blocking acquire should fail)
	acquired := false
	select {
	case loader.writeLock() <- struct{}{}:
		acquired = true
	default:
	}

	if acquired {
		t.Fatal("expected lock to be held after acquire")
	}

	// Release lock
	loader.releaseWrite()

	// Now lock should be available
	acquired = false
	select {
	case loader.writeLock() <- struct{}{}:
		acquired = true
	default:
	}

	if !acquired {
		t.Fatal("expected lock to be available after release")
	}

	// Clean up
	<-loader.writeLock()
}

func TestWriteLockContextTimeout(t *testing.T) {
	loader := NewPromptLoader(t.TempDir())

	// Acquire lock first
	if err := loader.acquireWrite(context.Background()); err != nil {
		t.Fatalf("failed to acquire initial lock: %v", err)
	}

	// Try to acquire with timeout while lock is held
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := loader.acquireWrite(ctx)

	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("expected timeout error message, got: %v", err)
	}

	// Clean up
	loader.releaseWrite()
}

func TestWriteLockSerialization(t *testing.T) {
	loader := NewPromptLoader(t.TempDir())
	var orders []int
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Launch multiple goroutines trying to acquire the lock
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if err := loader.acquireWrite(context.Background()); err != nil {
				t.Errorf("failed to acquire lock: %v", err)
				return
			}
			defer loader.releaseWrite()

			mu.Lock()
			orders = append(orders, id)
			mu.Unlock()

			time.Sleep(10 * time.Millisecond) // Hold lock briefly
		}(i)
	}

	wg.Wait() // Wait for all goroutines to complete

	// All should have acquired the lock in order
	mu.Lock()
	orderLen := len(orders)
	mu.Unlock()

	if orderLen != 3 {
		t.Errorf("expected 3 acquisitions, got %d", orderLen)
	}
}

func TestWriteLockConcurrentFromTwoLoaders(t *testing.T) {
	tmpDir := t.TempDir()
	loader1 := NewPromptLoader(tmpDir)
	loader2 := NewPromptLoader(tmpDir)

	if err := loader1.acquireWrite(context.Background()); err != nil {
		t.Fatalf("loader1 failed to acquire: %v", err)
	}

	// loader2 should also block (same baseDir = same lock)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	err := loader2.acquireWrite(ctx)
	if err == nil {
		t.Fatal("expected loader2 to timeout (same lock)")
	}

	loader1.releaseWrite()
}
