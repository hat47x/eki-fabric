package dispatchguard_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hat47x/eki-fabric/conformance/externaleffecttest"
	"github.com/hat47x/eki-fabric/internal/adapters/reference/dispatchlog/memory"
	"github.com/hat47x/eki-fabric/internal/adapters/reference/dispatchlog/sqlite"
	"github.com/hat47x/eki-fabric/internal/core/dispatchguard"
	"github.com/hat47x/eki-fabric/internal/spi/dispatchlog"
)

func guardSender(t testing.TB, log dispatchlog.Log) externaleffecttest.Sender {
	t.Helper()
	guard, err := dispatchguard.New(log)
	if err != nil {
		t.Fatalf("dispatchguard.New() error = %v", err)
	}
	return guard
}

// TestGuardMeetsCommitBeforeDispatchContract runs the External Effect Adapter
// contract against the guard, once with the in-memory log and once with the
// durable SQLite log.
func TestGuardMeetsCommitBeforeDispatchContract(t *testing.T) {
	t.Run("memory-log", func(t *testing.T) {
		externaleffecttest.RunCommitBeforeDispatch(t, externaleffecttest.Subject{
			NewLog: func(testing.TB) dispatchlog.Log {
				return memory.New()
			},
			NewSender: guardSender,
		})
	})

	t.Run("sqlite-log", func(t *testing.T) {
		externaleffecttest.RunCommitBeforeDispatch(t, externaleffecttest.Subject{
			NewLog: func(t testing.TB) dispatchlog.Log {
				log, err := sqlite.Open(context.Background(), filepath.Join(t.TempDir(), "dispatch.db"))
				if err != nil {
					t.Fatalf("sqlite.Open() error = %v", err)
				}
				t.Cleanup(func() {
					if err := log.Close(); err != nil {
						t.Errorf("close sqlite log: %v", err)
					}
				})
				return log
			},
			NewSender: guardSender,
		})
	})
}
