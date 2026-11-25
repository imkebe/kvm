package kvm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/jetkvm/kvm/internal/osservices/release"
)

// Another repository could mirror JetKVM's signed stable index and serve its
// own unsigned dev index: that index is never read, and lists nothing.
func TestCustomRepositoryHasNoDevIndex(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte(`{"releases":[]}`))
	}))
	defer srv.Close()
	previous := config
	config = &Config{OSServices: &OSServicesConfig{RepositoryURL: srv.URL}}
	t.Cleanup(func() { config = previous })

	r := &osServicesUpdates{svc: &osServicesService{stateSignal: make(chan struct{}, 1)}}
	r.releases[osUpdateIndexDev] = release.Release{Version: "0.9.9-dev"}
	if err := r.fetchIndex(context.Background(), srv.Client(), osUpdateIndexDev); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 0 {
		t.Fatal("the dev index of another repository was fetched")
	}
	if r.releases[osUpdateIndexDev].Version != "" {
		t.Fatalf("dev releases %+v, want none", r.releases[osUpdateIndexDev])
	}
}
