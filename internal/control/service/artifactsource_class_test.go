package service

import (
	"testing"
	"time"

	artifactfile "github.com/hat47x/eki-fabric/internal/adapters/reference/artifactstore/file"
)

const digestHex = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestTheDeploymentsClassIsRecordedWithTheBody(t *testing.T) {
	store, err := artifactfile.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sink := metaSink{store: store, dataClass: "internal"}
	if err := sink.PutMeta(digestHex, "alice", "", 3, "text/plain", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("PutMeta() error = %v", err)
	}
	meta, err := store.LoadMeta(digestHex)
	if err != nil {
		t.Fatal(err)
	}
	if meta.DataClass != "internal" {
		t.Fatalf("data_class = %q, want the deployment's class", meta.DataClass)
	}
}

func TestWithoutADeclaredClassTheBodyCarriesNone(t *testing.T) {
	store, err := artifactfile.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sink := metaSink{store: store}
	if err := sink.PutMeta(digestHex, "alice", "", 3, "text/plain", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("PutMeta() error = %v", err)
	}
	meta, err := store.LoadMeta(digestHex)
	if err != nil {
		t.Fatal(err)
	}
	if meta.DataClass != "" {
		t.Fatalf("data_class = %q, want empty: an undeclared class must not look classified", meta.DataClass)
	}
}
