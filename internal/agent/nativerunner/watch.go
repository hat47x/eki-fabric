package nativerunner

import (
	"sync/atomic"
	"time"
)

// resourceWatch polls a running process and stops it when a resource limit is
// exceeded. The post-exit checks remain authoritative; this watch closes the
// gap where a process would otherwise run far past its budget.
type resourceWatch struct {
	done           chan struct{}
	memoryExceeded atomic.Bool
	cpuExceeded    atomic.Bool
}

func watchResources(pid int, memoryLimit, cpuLimit uint64, kill func()) *resourceWatch {
	watch := &resourceWatch{done: make(chan struct{})}
	go func() {
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-watch.done:
				return
			case <-ticker.C:
				if memoryLimit != 0 {
					if rss, ok := sampleRSS(pid); ok && rss > memoryLimit {
						watch.memoryExceeded.Store(true)
						kill()
						return
					}
				}
				if cpuLimit != 0 {
					if cpuMillis, ok := sampleCPUMillis(pid); ok && cpuMillis > cpuLimit {
						watch.cpuExceeded.Store(true)
						kill()
						return
					}
				}
			}
		}
	}()
	return watch
}

func (w *resourceWatch) stop() {
	if w != nil {
		close(w.done)
	}
}

func (w *resourceWatch) memoryWasExceeded() bool {
	return w != nil && w.memoryExceeded.Load()
}

func (w *resourceWatch) cpuWasExceeded() bool {
	return w != nil && w.cpuExceeded.Load()
}
