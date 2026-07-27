package handlers

import (
	"context"
	"fmt"
	"sync"
)

// promptWriteLocks maps a prompt baseDir to the semaphore serialising writes.
//
// B1 DESIGN DECISION — one lock per baseDir, not per file:
//
//	Every mutation starts with a full corpus load (LoadAll walks the whole
//	tree), so per-file locking would not make the read-modify-write cycle
//	atomic: two writers could each snapshot the corpus, mutate their own copy
//	of the same prompt, and the second SavePrompt would silently discard the
//	first writer's delta. The unit that needs to be atomic is the *cycle*, and
//	the cycle spans the whole directory.
//
//	Cost is negligible: the corpus is tens-to-hundreds of small YAML files and
//	writes are rare next to reads, which stay lock-free via the TTL cache.
//
//	Keyed by baseDir rather than stored on the struct so that independently
//	constructed PromptLoaders over the same directory still serialise — which
//	is exactly the situation FeedbackManager created by building its own
//	loader on every call.
//
//	A channel is used instead of sync.Mutex because it is the only way to
//	acquire a lock under a context deadline; sync.Mutex.Lock cannot be
//	cancelled, so a queue of blocked writers could outlive the request that
//	created them.
//
//	Scope is in-process. Cross-process writers are still protected from torn
//	files by the temp-file+rename in savePromptLocked, but not from lost
//	updates; that would need an flock and is out of scope here.
var promptWriteLocks sync.Map // map[string]chan struct{}

func (pl *PromptLoader) writeLock() chan struct{} {
	if existing, ok := promptWriteLocks.Load(pl.baseDir); ok {
		return existing.(chan struct{})
	}
	actual, _ := promptWriteLocks.LoadOrStore(pl.baseDir, make(chan struct{}, 1))
	return actual.(chan struct{})
}

// acquireWrite takes the corpus write lock, honouring the context deadline.
func (pl *PromptLoader) acquireWrite(ctx context.Context) error {
	select {
	case pl.writeLock() <- struct{}{}:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("timeout acquiring prompt write lock: %w", ctx.Err())
	}
}

func (pl *PromptLoader) releaseWrite() { <-pl.writeLock() }
