package claimclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	sqlitequeue "github.com/hat47x/eki-fabric/internal/adapters/reference/claimqueue/sqlite"
	"github.com/hat47x/eki-fabric/internal/agent/claimqueue"
	"github.com/hat47x/eki-fabric/internal/spi/workerplane"
)

type ackPresenceClock struct{ now time.Time }

func (c *ackPresenceClock) Now() time.Time { return c.now }

// An omitted or null false-valued flag must not silently turn a partial HTTP
// response into a receipt that deletes durable evidence.
func TestHTTPIncompleteAckRetainsDurableClaim(t *testing.T) {
	for name, body := range map[string]string{
		"missing flag": `{"decision":"new","attempt_phase":"closed"}`,
		"null flag":    `{"decision":"new","attempt_phase":"closed","verification_required":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			var valid atomic.Bool
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if valid.Load() {
					_, _ = w.Write([]byte(`{"decision":"duplicate","attempt_phase":"closed","verification_required":false}`))
				} else {
					_, _ = w.Write([]byte(body))
				}
			}))
			defer srv.Close()
			transport, err := NewHTTP(HTTPConfig{ControlURL: srv.URL, Token: httpToken,
				RootCAs: srv.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs})
			if err != nil {
				t.Fatal(err)
			}
			defer transport.CloseIdleConnections()
			sender, err := New(transport, workerplane.Principal{WorkerIdentity: "worker-a"})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "queue.db")
			store, err := sqlitequeue.Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = store.Close() }()
			clock := &ackPresenceClock{now: time.Unix(200, 0)}
			makeQueue := func() *claimqueue.Queue {
				q, err := claimqueue.New(claimqueue.Config{Store: store, Sender: sender, Clock: clock,
					BaseDelay: time.Second, MaxDelay: time.Minute})
				if err != nil {
					t.Fatal(err)
				}
				return q
			}
			q := makeQueue()
			claim := httpTestClaim(t)
			if _, err := q.Enqueue(ctx, claim); err != nil {
				t.Fatal(err)
			}
			report, err := q.DeliverDue(ctx, 1)
			if err != nil || report != (claimqueue.Report{Retried: 1}) {
				t.Fatalf("incomplete receipt: report=%+v err=%v", report, err)
			}
			entry, found, err := store.Load(ctx, claim.AttemptID())
			if err != nil || !found || !entry.Claim.Equal(claim) || entry.Attempts != 1 ||
				!entry.QueuedAt.Equal(clock.now) || !entry.NextAt.After(clock.now) || entry.LastError == "" {
				t.Fatalf("pending entry=%+v found=%v err=%v", entry, found, err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = sqlitequeue.Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			restored, found, err := store.Load(ctx, claim.AttemptID())
			if err != nil || !found || !restored.Claim.Equal(entry.Claim) || restored.Attempts != entry.Attempts ||
				!restored.QueuedAt.Equal(entry.QueuedAt) || !restored.NextAt.Equal(entry.NextAt) || restored.LastError != entry.LastError {
				t.Fatalf("restored entry=%+v found=%v err=%v", restored, found, err)
			}
			q = makeQueue()
			valid.Store(true)
			if report, err := q.DeliverDue(ctx, 1); err != nil || report != (claimqueue.Report{}) {
				t.Fatalf("early delivery: %+v %v", report, err)
			}
			clock.now = restored.NextAt
			if report, err := q.DeliverDue(ctx, 1); err != nil || report != (claimqueue.Report{Delivered: 1}) {
				t.Fatalf("complete receipt: %+v %v", report, err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = sqlitequeue.Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			if _, found, err := store.Load(ctx, claim.AttemptID()); err != nil || found {
				t.Fatalf("deletion not durable: found=%v err=%v", found, err)
			}
		})
	}
}
