package nativerunner

import (
	"sync/atomic"
	"time"
)

// memoryWatch polls the resident set size of a running process and kills it
// when the limit is exceeded. It complements the post-exit check: the post-exit
// check is authoritative, but a process that allocates far beyond its limit may
// never reach that check.
type memoryWatch struct {
	done     chan struct{}
	exceeded atomic.Bool
}

func watchMemory(pid int, limit uint64, kill func()) *memoryWatch {
	watch := &memoryWatch{done: make(chan struct{})}
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watch.done:
				return
			case <-ticker.C:
				rss, ok := sampleRSS(pid)
				if !ok {
					continue
				}
				if rss > limit {
					watch.exceeded.Store(true)
					kill()
					return
				}
			}
		}
	}()
	return watch
}

func (w *memoryWatch) stop() {
	if w != nil {
		close(w.done)
	}
}

func (w *memoryWatch) wasExceeded() bool {
	return w != nil && w.exceeded.Load()
}
