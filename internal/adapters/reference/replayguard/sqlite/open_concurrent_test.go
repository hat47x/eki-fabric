package sqlite

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Several openers racing on a brand-new file must all succeed, and the
// shared database must still grant a nonce to exactly one of them.
func TestConcurrentOpenOfNewDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent-open.db")
	const openers = 8
	guards := make([]*Guard, openers)
	errs := make([]error, openers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < openers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			guards[i], errs[i] = Open(context.Background(), path, clockFunc(func() time.Time { return baseTime }), 1)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("opener %d: %v", i, err)
		}
		defer guards[i].Close()
	}
	winners := 0
	for _, g := range guards {
		claimed, err := g.Claim(context.Background(), nonce(7), baseTime.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if claimed {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("%d winners", winners)
	}
}
