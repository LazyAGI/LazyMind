package desktopupdate

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// A stable source may redirect to an expiring CDN address. Retry the stable
// source, never the remembered temporary address, without changing pinned bytes.
func TestExpiredRedirectRefreshesStableSource(t *testing.T) {
	for _, operation := range []string{"discover", "download"} {
		for _, status := range []int{401, 403, 404} {
			for _, succeeds := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/%d/succeeds=%v", operation, status, succeeds), func(t *testing.T) {
					f := newTransferFixture(t)
					var stable, expired atomic.Int32
					f.files["/expired"] = []byte("not an artifact")
					f.serveArtifacts["/expired"] = func(w http.ResponseWriter, _ *http.Request, _ []byte) { expired.Add(1); w.WriteHeader(status) }
					handler := func(w http.ResponseWriter, r *http.Request, body []byte) {
						if stable.Add(1) == 1 || !succeeds {
							http.Redirect(w, r, f.server.URL+"/expired", http.StatusFound)
							return
						}
						_, _ = w.Write(body)
					}
					if operation == "discover" {
						f.servePolicy = func(w http.ResponseWriter, r *http.Request) { handler(w, r, f.envelope(t)) }
					} else {
						f.serveArtifacts["/r2/package"] = handler
					}
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					var err error
					if operation == "discover" {
						var d Decision
						d, err = Discover(ctx, f.config, f.state, f.root, f.server.Client(), policyTime)
						if succeeds && err == nil && (d.Next == nil || d.Next.ID != "r2") {
							t.Fatalf("wrong target: %+v", d)
						}
					} else {
						_, err = Download(ctx, f.config, f.state, f.root, "r2", f.server.Client(), policyTime, nil)
					}
					if succeeds {
						if err != nil || stable.Load() != 2 || expired.Load() != 1 {
							t.Fatalf("err=%v stable=%d expired=%d", err, stable.Load(), expired.Load())
						}
					} else {
						code := map[int]string{401: "source_unauthorized", 403: "source_forbidden", 404: "release_missing"}[status]
						if ErrorCode(err) != code || stable.Load() != 3 || expired.Load() != 3 {
							t.Fatalf("err=%v stable=%d expired=%d", err, stable.Load(), expired.Load())
						}
					}
				})
			}
		}
	}
}

func TestExpiredRedirectRefreshCanBeCancelled(t *testing.T) {
	f := newTransferFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f.files["/expired"] = nil
	f.serveArtifacts["/expired"] = func(w http.ResponseWriter, _ *http.Request, _ []byte) { w.WriteHeader(403); cancel() }
	f.servePolicy = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, f.server.URL+"/expired", http.StatusFound)
	}
	_, err := Discover(ctx, f.config, f.state, f.root, f.server.Client(), policyTime)
	if ErrorCode(err) != "cancelled" || f.policyRequests.Load() != 1 {
		t.Fatalf("err=%v requests=%d", err, f.policyRequests.Load())
	}
}
