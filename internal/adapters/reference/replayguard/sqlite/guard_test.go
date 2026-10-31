package sqlite

import (
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

var baseTime = time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)

type clockFunc func() time.Time

func (f clockFunc) Now() time.Time { return f() }

func nonce(seed byte) []byte {
	value := make([]byte, 16)
	for i := range value {
		value[i] = seed + byte(i)
	}
	return value
}

func openGuard(t testing.TB, path string, clock Clock, max int) *Guard {
	t.Helper()
	g, err := Open(context.Background(), path, clock, max)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	return g
}

func assertClaim(t testing.TB, g *Guard, value []byte, until time.Time, want bool) {
	t.Helper()
	got, err := g.Claim(context.Background(), value, until)
	if err != nil || got != want {
		t.Fatalf("Claim = %v, %v; want %v, nil", got, err, want)
	}
}

func TestReplayAndCapacitySurviveReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonces.db")
	now := baseTime
	clock := clockFunc(func() time.Time { return now })
	g := openGuard(t, path, clock, 1)
	assertClaim(t, g, nonce(1), now.Add(time.Minute), true)
	assertClaim(t, g, nonce(1), now.Add(2*time.Minute), false)
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	g = openGuard(t, path, clock, 1)
	assertClaim(t, g, nonce(1), now.Add(time.Minute), false)
	if ok, err := g.Claim(context.Background(), nonce(2), now.Add(time.Minute)); ok || !errors.Is(err, ErrGuardCapacity) {
		t.Fatalf("capacity after reopen = %v, %v", ok, err)
	}
	if other, err := Open(context.Background(), path, clock, 2); other != nil || !errors.Is(err, ErrInvalidGuard) {
		t.Fatalf("capacity change = %v, %v", other, err)
	}
	// A duplicate must not extend a live record's original expiry.
	now = baseTime.Add(time.Minute)
	assertClaim(t, g, nonce(1), now.Add(time.Minute), true)
}

func TestExpiredEntryReleasesCapacityForAnotherNonce(t *testing.T) {
	now := baseTime
	g := openGuard(t, filepath.Join(t.TempDir(), "expiry.db"), clockFunc(func() time.Time { return now }), 1)
	assertClaim(t, g, nonce(1), now.Add(time.Minute), true)
	now = now.Add(time.Minute)
	assertClaim(t, g, nonce(2), now.Add(time.Minute), true)
}

func TestConcurrentConnectionsHaveOneWinnerAndShareCapacity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	clock := clockFunc(func() time.Time { return baseTime })
	guards := []*Guard{openGuard(t, path, clock, 1), openGuard(t, path, clock, 1)}
	var wg sync.WaitGroup
	results := make(chan bool, 32)
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func(g *Guard) {
			defer wg.Done()
			ok, err := g.Claim(context.Background(), nonce(3), baseTime.Add(time.Minute))
			if err != nil {
				t.Error(err)
			}
			results <- ok
		}(guards[i%len(guards)])
	}
	wg.Wait()
	close(results)
	winners := 0
	for won := range results {
		if won {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d", winners)
	}
	if ok, err := guards[1].Claim(context.Background(), nonce(4), baseTime.Add(time.Minute)); ok || !errors.Is(err, ErrGuardCapacity) {
		t.Fatalf("shared capacity = %v, %v", ok, err)
	}
}

func TestFailedTransactionDoesNotBurnNonce(t *testing.T) {
	g := openGuard(t, filepath.Join(t.TempDir(), "failure.db"), clockFunc(func() time.Time { return baseTime }), 2)
	if _, err := g.db.Exec(`CREATE TRIGGER fail_claim BEFORE INSERT ON nonce_claims BEGIN SELECT RAISE(ABORT, 'injected storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	if ok, err := g.Claim(context.Background(), nonce(5), baseTime.Add(time.Minute)); ok || err == nil {
		t.Fatalf("failed insert = %v, %v", ok, err)
	}
	if _, err := g.db.Exec(`DROP TRIGGER fail_claim`); err != nil {
		t.Fatal(err)
	}
	assertClaim(t, g, nonce(5), baseTime.Add(time.Minute), true)
	assertClaim(t, g, nonce(5), baseTime.Add(time.Minute), false)
}

func TestConcurrentDistinctNoncesCannotOverspendCapacity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quota.db")
	clock := clockFunc(func() time.Time { return baseTime })
	guards := []*Guard{openGuard(t, path, clock, 1), openGuard(t, path, clock, 1)}
	var wg sync.WaitGroup
	results := make(chan bool, 16)
	for i := 0; i < cap(results); i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ok, err := guards[i%2].Claim(context.Background(), nonce(byte(i)), baseTime.Add(time.Minute))
			if err != nil && !errors.Is(err, ErrGuardCapacity) {
				t.Error(err)
			}
			results <- ok
		}(i)
	}
	wg.Wait()
	close(results)
	winners := 0
	for ok := range results {
		if ok {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("distinct nonce capacity winners = %d", winners)
	}
}

func TestUnknownSchemaIsRejectedWithoutDeletingClaims(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema.db")
	clock := clockFunc(func() time.Time { return baseTime })
	g := openGuard(t, path, clock, 1)
	assertClaim(t, g, make([]byte, 32), baseTime.Add(time.Minute), true)
	if _, err := g.db.Exec(`UPDATE replay_guard_config SET schema_version = 2`); err != nil {
		t.Fatal(err)
	}
	if next, err := Open(context.Background(), path, clock, 1); next != nil || !errors.Is(err, ErrInvalidGuard) {
		t.Fatalf("unknown schema = %v, %v", next, err)
	}
	var count int
	if err := g.db.QueryRow(`SELECT COUNT(*) FROM nonce_claims`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("unknown schema changed claim count = %d, %v", count, err)
	}
}

func TestCancellationAfterLockDoesNotBurnNonce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := openGuard(t, filepath.Join(t.TempDir(), "cancel.db"), clockFunc(func() time.Time { cancel(); return baseTime }), 2)
	if ok, err := g.Claim(ctx, nonce(6), baseTime.Add(time.Minute)); ok || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel during claim = %v, %v", ok, err)
	}
	g.clock = clockFunc(func() time.Time { return baseTime })
	assertClaim(t, g, nonce(6), baseTime.Add(time.Minute), true)
}

func TestOnlyNonceDigestIsPersistedAndPathsKeepReservedCharacters(t *testing.T) {
	name := "nonce#1?.db"
	if runtime.GOOS == "windows" {
		name = "nonce#1%.db"
	}
	g := openGuard(t, filepath.Join(t.TempDir(), name), clockFunc(func() time.Time { return baseTime }), 2)
	value := nonce(7)
	assertClaim(t, g, value, baseTime.Add(time.Minute), true)
	var stored []byte
	if err := g.db.QueryRow(`SELECT nonce_hash FROM nonce_claims`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(value)
	if string(stored) != string(want[:]) {
		t.Fatal("stored key is not the nonce digest")
	}
}

func TestInvalidClaimsAndClosedDatabaseFailClosed(t *testing.T) {
	g := openGuard(t, filepath.Join(t.TempDir(), "invalid.db"), clockFunc(func() time.Time { return baseTime }), 2)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, row := range []struct {
		name  string
		ctx   context.Context
		value []byte
		until time.Time
	}{
		{"nil-context", nil, nonce(8), baseTime.Add(time.Minute)},
		{"cancelled", ctx, nonce(8), baseTime.Add(time.Minute)},
		{"short", context.Background(), make([]byte, 15), baseTime.Add(time.Minute)},
		{"long", context.Background(), make([]byte, 33), baseTime.Add(time.Minute)},
		{"zero-expiry", context.Background(), nonce(8), time.Time{}},
		{"expired", context.Background(), nonce(8), baseTime},
		{"overflow-expiry", context.Background(), nonce(8), time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC)},
	} {
		t.Run(row.name, func(t *testing.T) {
			if ok, err := g.Claim(row.ctx, row.value, row.until); ok || err == nil {
				t.Fatalf("invalid claim = %v, %v", ok, err)
			}
		})
	}
	for _, now := range []time.Time{{}, time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		g.clock = clockFunc(func() time.Time { return now })
		if ok, err := g.Claim(context.Background(), nonce(8), baseTime.Add(time.Minute)); ok || !errors.Is(err, ErrInvalidNonce) {
			t.Fatalf("invalid clock = %v, %v", ok, err)
		}
	}
	g.clock = clockFunc(func() time.Time { return baseTime })
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if ok, err := g.Claim(context.Background(), nonce(8), baseTime.Add(time.Minute)); ok || err == nil {
		t.Fatalf("closed database = %v, %v", ok, err)
	}
}

func TestOpenRejectsInvalidConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.db")
	clock := clockFunc(func() time.Time { return baseTime })
	for _, row := range []struct {
		ctx   context.Context
		path  string
		clock Clock
		max   int
	}{
		{nil, path, clock, 1}, {context.Background(), "", clock, 1},
		{context.Background(), path, nil, 1}, {context.Background(), path, clock, 0},
	} {
		if g, err := Open(row.ctx, row.path, row.clock, row.max); g != nil || !errors.Is(err, ErrInvalidGuard) {
			t.Fatalf("Open = %v, %v", g, err)
		}
	}
	var g *Guard
	if ok, err := g.Claim(context.Background(), nonce(9), baseTime.Add(time.Minute)); ok || !errors.Is(err, ErrInvalidGuard) {
		t.Fatalf("nil guard = %v, %v", ok, err)
	}
}

func TestFenceRefusesStaleEpochAndSurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fences.db")
	now := baseTime
	clock := clockFunc(func() time.Time { return now })
	g := openGuard(t, path, clock, 2)
	if ok, err := g.Fence(context.Background(), "tsk_1", 7, now.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("first fence = %v, %v", ok, err)
	}
	// A lower epoch is superseded and writes nothing.
	if ok, err := g.Fence(context.Background(), "tsk_1", 6, now.Add(time.Minute)); err != nil || ok {
		t.Fatalf("stale fence = %v, %v", ok, err)
	}
	// The same epoch is accepted again: a re-issued WorkOrder passes.
	if ok, err := g.Fence(context.Background(), "tsk_1", 7, now.Add(2*time.Minute)); err != nil || !ok {
		t.Fatalf("same fence = %v, %v", ok, err)
	}
	if ok, err := g.Fence(context.Background(), "tsk_1", 8, now.Add(2*time.Minute)); err != nil || !ok {
		t.Fatalf("higher fence = %v, %v", ok, err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	// The record survives the restart and keeps refusing the older epoch.
	now = baseTime.Add(time.Minute)
	g = openGuard(t, path, clock, 2)
	if ok, err := g.Fence(context.Background(), "tsk_1", 7, now.Add(time.Minute)); err != nil || ok {
		t.Fatalf("stale fence after reopen = %v, %v", ok, err)
	}
	// Another Task keeps its own record.
	if ok, err := g.Fence(context.Background(), "tsk_2", 1, now.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("other task = %v, %v", ok, err)
	}
}

func TestExpiredFenceReleasesCapacityAndInvalidClaimsFail(t *testing.T) {
	now := baseTime
	g := openGuard(t, filepath.Join(t.TempDir(), "fence-expiry.db"), clockFunc(func() time.Time { return now }), 1)
	if ok, err := g.Fence(context.Background(), "tsk_1", 1, now.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("first fence = %v, %v", ok, err)
	}
	if ok, err := g.Fence(context.Background(), "tsk_2", 1, now.Add(time.Minute)); ok || !errors.Is(err, ErrGuardCapacity) {
		t.Fatalf("capacity = %v, %v", ok, err)
	}
	now = now.Add(time.Minute)
	if ok, err := g.Fence(context.Background(), "tsk_2", 1, now.Add(time.Minute)); err != nil || !ok {
		t.Fatalf("after expiry = %v, %v", ok, err)
	}
	if ok, err := g.Fence(context.Background(), "", 1, now.Add(time.Minute)); ok || !errors.Is(err, ErrInvalidFence) {
		t.Fatalf("empty task = %v, %v", ok, err)
	}
	if ok, err := g.Fence(context.Background(), "tsk_3", 0, now.Add(time.Minute)); ok || !errors.Is(err, ErrInvalidFence) {
		t.Fatalf("zero epoch = %v, %v", ok, err)
	}
	if ok, err := g.Fence(context.Background(), "tsk_3", 1, now); ok || !errors.Is(err, ErrInvalidFence) {
		t.Fatalf("past expiry = %v, %v", ok, err)
	}
}
