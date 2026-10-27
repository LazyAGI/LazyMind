package desktopupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type transferFixture struct {
	*policyFixture
	server           *httptest.Server
	files            map[string][]byte
	packageRequests  atomic.Int32
	policyRequests   atomic.Int32
	servePackage     func(http.ResponseWriter, *http.Request, []byte)
	servePolicy      func(http.ResponseWriter, *http.Request)
	serveArtifacts   map[string]func(http.ResponseWriter, *http.Request, []byte)
	artifactRequests sync.Map
}

func newTransferFixture(t *testing.T) *transferFixture {
	return newTransferFixtureWithTLS(t, false)
}

func newTransferFixtureWithTLS(t *testing.T, secure bool) *transferFixture {
	t.Helper()
	f := &transferFixture{policyFixture: newPolicyFixture(t), files: map[string][]byte{}, serveArtifacts: map[string]func(http.ResponseWriter, *http.Request, []byte){}}
	f.config.Mode = "unsigned-test"
	f.config.Audience = "personal-transfer"
	f.config.Channel = "personal-test"
	f.policy["audience"] = f.config.Audience
	f.policy["channel"] = f.config.Channel
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/policy.json" {
			f.policyRequests.Add(1)
			if f.servePolicy != nil {
				f.servePolicy(w, r)
				return
			}
			_, _ = w.Write(f.envelope(t))
			return
		}
		body, ok := f.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		counter, _ := f.artifactRequests.LoadOrStore(r.URL.Path, &atomic.Int32{})
		counter.(*atomic.Int32).Add(1)
		if serve := f.serveArtifacts[r.URL.Path]; serve != nil {
			serve(w, r, body)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/package") {
			f.packageRequests.Add(1)
			if f.servePackage != nil {
				f.servePackage(w, r, body)
				return
			}
		}
		_, _ = w.Write(body)
	})
	if secure {
		f.server = httptest.NewTLSServer(handler)
	} else {
		f.server = httptest.NewServer(handler)
	}
	t.Cleanup(f.server.Close)
	f.config.PolicyURL = f.server.URL + "/policy.json"
	f.config.AllowedOrigins = []string{f.server.URL}
	for _, item := range f.policy["releases"].([]any) {
		release := item.(map[string]any)
		id := release["releaseId"].(string)
		for _, kind := range []string{"package", "feed", "components"} {
			p := "/" + id + "/" + kind
			body := []byte("synthetic unit fixture " + id + " " + kind)
			f.files[p] = body
			sum := sha256.Sum256(body)
			release[kind] = map[string]any{"url": f.server.URL + p, "sha256": hex.EncodeToString(sum[:]), "size": len(body)}
		}
	}
	for _, item := range f.policy["edges"].([]any) {
		edge := item.(map[string]any)
		sum := sha256.Sum256(f.files["/"+edge["sourceReleaseId"].(string)+"/package"])
		edge["recoveryContract"].(map[string]any)["sourcePackageSHA256"] = hex.EncodeToString(sum[:])
	}
	return f
}

func (f *transferFixture) download(t *testing.T) (DownloadReceipt, error) {
	t.Helper()
	return Download(context.Background(), f.config, f.state, f.root, "r2", f.server.Client(), policyTime, nil)
}

func TestDiscoverUsesActualSignedNetworkPolicy(t *testing.T) {
	f := newTransferFixture(t)
	d, err := Discover(context.Background(), f.config, f.state, f.root, f.server.Client(), policyTime)
	if err != nil || d.Status != "bridge" || d.Next == nil || d.Next.ID != "r2" || f.policyRequests.Load() != 1 {
		t.Fatalf("decision=%+v err=%v requests=%d", d, err, f.policyRequests.Load())
	}
	if f.packageRequests.Load() != 0 {
		t.Fatal("checking updates downloaded a package")
	}
}

func TestDownloadVerifiesAllPinnedArtifactsAndReusesOnlyValidCache(t *testing.T) {
	f := newTransferFixture(t)
	var events []Progress
	r, err := Download(context.Background(), f.config, f.state, f.root, "r2", f.server.Client(), policyTime, func(p Progress) { events = append(events, p) })
	if err != nil || r.Status != "downloaded" || r.ReleaseID != "r2" || r.PolicySequence != 42 || len(r.PolicyDigest) != 64 {
		t.Fatalf("receipt=%+v err=%v", r, err)
	}
	for kind, p := range map[string]string{"package": r.PackagePath, "feed": r.FeedPath, "components": r.ComponentsPath} {
		data, err := os.ReadFile(p)
		if err != nil || string(data) != string(f.files["/r2/"+kind]) {
			t.Fatalf("%s bytes=%q err=%v", kind, data, err)
		}
		relative, err := filepath.Rel(f.root, p)
		if err != nil || strings.HasPrefix(relative, "..") {
			t.Fatalf("artifact outside fixed root: %q", p)
		}
	}
	if len(events) == 0 || events[len(events)-1].DownloadedBytes != events[len(events)-1].TotalBytes || events[len(events)-1].TotalBytes <= 0 {
		t.Fatalf("incomplete progress: %+v", events)
	}
	for i := 1; i < len(events); i++ {
		if events[i].DownloadedBytes < events[i-1].DownloadedBytes || events[i].DownloadedBytes > events[i].TotalBytes {
			t.Fatal("invalid progress")
		}
	}
	if _, err := f.download(t); err != nil {
		t.Fatal(err)
	}
	if f.packageRequests.Load() != 1 || f.policyRequests.Load() != 2 {
		t.Fatalf("cache reuse must still refresh policy: packages=%d policies=%d", f.packageRequests.Load(), f.policyRequests.Load())
	}
	if err := os.WriteFile(r.PackagePath, []byte("corrupt cached executable"), 0600); err != nil {
		t.Fatal(err)
	}
	r, err = f.download(t)
	if err != nil {
		t.Fatal(err)
	}
	bytes, _ := os.ReadFile(r.PackagePath)
	if string(bytes) != string(f.files["/r2/package"]) || f.packageRequests.Load() != 2 {
		t.Fatal("corrupt cache reported as ready")
	}
}

func TestDownloadCannotUseWithdrawnChangedOrUnapprovedTarget(t *testing.T) {
	for _, kind := range []string{"withdrawn", "changed", "wrong-id", "replay"} {
		t.Run(kind, func(t *testing.T) {
			f := newTransferFixture(t)
			if _, err := Discover(context.Background(), f.config, f.state, f.root, f.server.Client(), policyTime); err != nil {
				t.Fatal(err)
			}
			expected := "r2"
			switch kind {
			case "withdrawn":
				f.policy["policySequence"] = 43
				f.policy["revoked"] = []string{"r2"}
			case "changed":
				f.policy["policySequence"] = 43
				f.policy["edges"] = []any{fixtureEdge("r1", "r3")}
				source := f.policy["releases"].([]any)[0].(map[string]any)
				f.policy["edges"].([]any)[0].(map[string]any)["recoveryContract"].(map[string]any)["sourcePackageSHA256"] = source["package"].(map[string]any)["sha256"]
			case "wrong-id":
				expected = "r3"
			case "replay":
				f.policy["policySequence"] = 41
			}
			if _, err := Download(context.Background(), f.config, f.state, f.root, expected, f.server.Client(), policyTime, nil); err == nil {
				t.Fatal("unapproved target downloaded")
			}
			if f.packageRequests.Load() != 0 {
				t.Fatal("package fetched before authorizing the fixed target")
			}
		})
	}
}

func TestDownloadRejectsWrongBytesAndLengthsAndCleansPartialFiles(t *testing.T) {
	for _, artifactKind := range []string{"package", "feed", "components"} {
		t.Run(artifactKind, func(t *testing.T) {
			for _, kind := range []string{"hash", "short", "long", "status"} {
				t.Run(kind, func(t *testing.T) {
					f := newTransferFixture(t)
					f.serveArtifacts["/r2/"+artifactKind] = func(w http.ResponseWriter, r *http.Request, body []byte) {
						switch kind {
						case "hash":
							_, _ = w.Write([]byte(strings.Repeat("x", len(body))))
						case "short":
							w.Header().Set("Content-Length", fmt.Sprint(len(body)))
							_, _ = w.Write(body[:len(body)-1])
						case "long":
							w.(http.Flusher).Flush() // No Content-Length: require an actual bounded read through EOF.
							_, _ = w.Write(append(append([]byte{}, body...), 'x'))
						case "status":
							http.Error(w, "upstream failure", http.StatusServiceUnavailable)
						}
					}
					if receipt, err := f.download(t); err == nil || receipt.Status == "downloaded" {
						t.Fatalf("invalid artifact accepted: %+v %v", receipt, err)
					}
					assertNoPartialDownload(t, f.root)
					assertOnlyCompleteFixtureArtifacts(t, f)
				})
			}
		})
	}
}

func assertOnlyCompleteFixtureArtifacts(t *testing.T, f *transferFixture) {
	t.Helper()
	if err := filepath.WalkDir(f.root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "trust" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".lock") {
			return nil
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, complete := range f.files {
			if string(body) == string(complete) {
				return nil
			}
		}
		return fmt.Errorf("incomplete or corrupt bytes were published outside trust state: %s", p)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNetworkSignatureFailureNeverFetchesArtifactsOrAdvancesTrust(t *testing.T) {
	f := newTransferFixture(t)
	_, f.private, _ = ed25519.GenerateKey(rand.Reader)
	if _, err := Discover(context.Background(), f.config, f.state, f.root, f.server.Client(), policyTime); ErrorCode(err) != "policy_signature" {
		t.Fatalf("wrong signature was not classified: %v", err)
	}
	if _, err := f.download(t); ErrorCode(err) != "policy_signature" {
		t.Fatalf("download ignored signature failure: %v", err)
	}
	f.artifactRequests.Range(func(key, value any) bool {
		if value.(*atomic.Int32).Load() != 0 {
			t.Errorf("untrusted policy fetched %v", key)
		}
		return true
	})
	entries, err := os.ReadDir(f.root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("invalid signature created trust/cache state: %v %v", entries, err)
	}
}

func TestEveryArtifactCacheIsReverified(t *testing.T) {
	for _, kind := range []string{"package", "feed", "components"} {
		t.Run(kind, func(t *testing.T) {
			f := newTransferFixture(t)
			receipt, err := f.download(t)
			if err != nil {
				t.Fatal(err)
			}
			p := map[string]string{"package": receipt.PackagePath, "feed": receipt.FeedPath, "components": receipt.ComponentsPath}[kind]
			if err := os.WriteFile(p, []byte("corrupt cached "+kind), 0600); err != nil {
				t.Fatal(err)
			}
			receipt, err = f.download(t)
			if err != nil {
				t.Fatal(err)
			}
			p = map[string]string{"package": receipt.PackagePath, "feed": receipt.FeedPath, "components": receipt.ComponentsPath}[kind]
			body, err := os.ReadFile(p)
			if err != nil || string(body) != string(f.files["/r2/"+kind]) {
				t.Fatalf("corrupt cache accepted: %s %q %v", kind, body, err)
			}
			counter, _ := f.artifactRequests.Load("/r2/" + kind)
			if counter == nil || counter.(*atomic.Int32).Load() != 2 {
				t.Fatal("corrupt artifact was not fetched again")
			}
		})
	}
}

func assertNoPartialDownload(t *testing.T, root string) {
	t.Helper()
	if err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && (strings.HasSuffix(d.Name(), ".part") || strings.HasPrefix(d.Name(), ".download-")) {
			return fmt.Errorf("partial file left behind: %s", p)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadCancellationDoesNotPublishPartialArtifact(t *testing.T) {
	f := newTransferFixture(t)
	f.servePackage = func(w http.ResponseWriter, r *http.Request, body []byte) {
		w.Header().Set("Content-Length", fmt.Sprint(len(body)))
		_, _ = w.Write(body[:1])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r, err := Download(ctx, f.config, f.state, f.root, "r2", f.server.Client(), policyTime, func(p Progress) {
		if p.DownloadedBytes > 0 {
			cancel()
		}
	})
	if err == nil || r.Status == "downloaded" {
		t.Fatalf("cancelled download succeeded: %+v %v", r, err)
	}
	assertNoPartialDownload(t, f.root)
	assertOnlyCompleteFixtureArtifacts(t, f)
}

func TestRedirectCannotExpandCompiledAllowedOrigins(t *testing.T) {
	var received atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1); _, _ = w.Write([]byte("untrusted")) }))
	defer other.Close()
	f := newTransferFixture(t)
	f.servePackage = func(w http.ResponseWriter, r *http.Request, body []byte) {
		http.Redirect(w, r, other.URL+"/app.zip", http.StatusFound)
	}
	if _, err := f.download(t); err == nil {
		t.Fatal("untrusted redirect accepted")
	}
	if received.Load() != 0 {
		t.Fatal("untrusted origin received a request before rejection")
	}
	assertNoPartialDownload(t, f.root)
}

func TestTrustedRedirectWorksButRedirectLoopsAreBounded(t *testing.T) {
	f := newTransferFixture(t)
	f.files["/redirected-package"] = f.files["/r2/package"]
	f.servePackage = func(w http.ResponseWriter, r *http.Request, _ []byte) {
		http.Redirect(w, r, f.server.URL+"/redirected-package", http.StatusFound)
	}
	if receipt, err := f.download(t); err != nil || receipt.Status != "downloaded" {
		t.Fatalf("trusted redirect failed: %+v %v", receipt, err)
	}
	loop := newTransferFixture(t)
	loop.servePackage = func(w http.ResponseWriter, r *http.Request, _ []byte) {
		http.Redirect(w, r, loop.server.URL+r.URL.Path, http.StatusFound)
	}
	if _, err := loop.download(t); err == nil {
		t.Fatal("redirect loop accepted")
	}
	if loop.packageRequests.Load() > 6 {
		t.Fatalf("too many redirects: %d", loop.packageRequests.Load())
	}
}

func TestOfficialHTTPSDoesNotDisableCertificateVerification(t *testing.T) {
	f := newTransferFixtureWithTLS(t, true)
	f.config.Mode = "official"
	f.config.Audience = "official"
	f.config.Channel = "stable"
	f.policy["audience"] = "official"
	f.policy["channel"] = "stable"
	if _, err := Discover(context.Background(), f.config, f.state, f.root, nil, policyTime); err == nil {
		t.Fatal("default client accepted an untrusted TLS certificate")
	}
	// Explicit test transport trusts this fixture's certificate; production
	// callers use the default verifier and never receive this injected client.
	d, err := Discover(context.Background(), f.config, f.state, f.root, f.server.Client(), policyTime)
	if err != nil || d.Status != "bridge" {
		t.Fatalf("trusted TLS fixture failed: %+v %v", d, err)
	}
}

// Both entrypoints must use the same bounded HTTP behavior. Download failures
// here are injected on the package request after its policy has been accepted.
func retryFixture(t *testing.T, phase string, serve func(http.ResponseWriter, *http.Request, []byte)) (*transferFixture, func(context.Context) error) {
	t.Helper()
	f := newTransferFixture(t)
	if phase == "discover" {
		f.servePolicy = func(w http.ResponseWriter, r *http.Request) { serve(w, r, f.envelope(t)) }
	} else {
		f.serveArtifacts["/r2/package"] = serve
	}
	return f, func(ctx context.Context) error {
		if phase == "discover" {
			_, err := Discover(ctx, f.config, f.state, f.root, f.server.Client(), policyTime)
			return err
		}
		_, err := Download(ctx, f.config, f.state, f.root, "r2", f.server.Client(), policyTime, nil)
		return err
	}
}

func TestHTTPRetryAfterAndRecoverableErrorsHaveBoundedBackoff(t *testing.T) {
	for _, phase := range []string{"discover", "download"} {
		t.Run(phase, func(t *testing.T) {
			for _, kind := range []string{"rate-limit", "rate-limit-date", "temporary-server", "network"} {
				t.Run(kind, func(t *testing.T) {
					var attempts atomic.Int32
					attemptTimes := make(chan time.Time, 4)
					retryDeadline := make(chan time.Time, 1)
					f, run := retryFixture(t, phase, func(w http.ResponseWriter, r *http.Request, body []byte) {
						n := attempts.Add(1)
						select {
						case attemptTimes <- time.Now():
						default:
						}
						if kind == "rate-limit" && n == 1 {
							w.Header().Set("Retry-After", "1")
							http.Error(w, "slow down", 429)
							return
						}
						if kind == "rate-limit-date" && n == 1 {
							deadline := time.Now().Add(2 * time.Second).UTC().Truncate(time.Second)
							retryDeadline <- deadline
							w.Header().Set("Retry-After", deadline.Format(http.TimeFormat))
							http.Error(w, "slow down", 429)
							return
						}
						if kind == "temporary-server" && n <= 2 {
							http.Error(w, "try later", 503)
							return
						}
						if kind == "network" && n == 1 {
							connection, _, err := w.(http.Hijacker).Hijack()
							if err != nil {
								t.Error(err)
								return
							}
							_ = connection.Close()
							return
						}
						_, _ = w.Write(body)
					})
					// Keep this request fresh so net/http's transparent reused-connection
					// retry cannot mask the updater's own bounded network backoff.
					if kind == "network" {
						f.server.Config.SetKeepAlivesEnabled(false)
					}
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if err := run(ctx); err != nil {
						t.Fatal(err)
					}
					want := int32(2)
					if kind == "temporary-server" {
						want = 3
					}
					if attempts.Load() != want {
						t.Fatalf("attempts=%d want=%d", attempts.Load(), want)
					}
					first, second := <-attemptTimes, <-attemptTimes
					if kind == "rate-limit-date" {
						if deadline := <-retryDeadline; second.Before(deadline) {
							t.Fatalf("retried before HTTP-date: %v < %v", second, deadline)
						}
						return
					}
					minimum := 100 * time.Millisecond
					if kind == "rate-limit" {
						minimum = time.Second
					}
					if second.Sub(first) < minimum {
						t.Fatalf("retried before required delay: %v < %v", second.Sub(first), minimum)
					}
				})
			}
		})
	}
}

func TestHTTPRetryWaitIsCancellableAndLongRateLimitFailsWithoutRetry(t *testing.T) {
	for _, phase := range []string{"discover", "download"} {
		t.Run(phase, func(t *testing.T) {
			var attempts atomic.Int32
			seen := make(chan struct{}, 1)
			_, run := retryFixture(t, phase, func(w http.ResponseWriter, r *http.Request, _ []byte) {
				attempts.Add(1)
				w.Header().Set("Retry-After", "10")
				http.Error(w, "wait", 429)
				w.(http.Flusher).Flush()
				select {
				case seen <- struct{}{}:
				default:
				}
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				select {
				case <-seen:
					timer := time.NewTimer(100 * time.Millisecond)
					defer timer.Stop()
					select {
					case <-timer.C:
						cancel()
					case <-ctx.Done():
					}
				case <-ctx.Done():
				}
			}()
			started := time.Now()
			err := run(ctx)
			if ErrorCode(err) != "cancelled" || time.Since(started) < 80*time.Millisecond || time.Since(started) > time.Second || attempts.Load() != 1 {
				t.Fatalf("wait did not cancel correctly: %v elapsed=%v attempts=%d", err, time.Since(started), attempts.Load())
			}
			attempts.Store(0)
			_, run = retryFixture(t, phase, func(w http.ResponseWriter, r *http.Request, _ []byte) {
				attempts.Add(1)
				w.Header().Set("Retry-After", "3600")
				http.Error(w, "wait an hour", 429)
			})
			deadline, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			if err := run(deadline); ErrorCode(err) != "rate_limited" || attempts.Load() != 1 {
				t.Fatalf("long wait must stop, not retry early: %v attempts=%d", err, attempts.Load())
			}
		})
	}
}

func TestHTTPPermanentErrorsAreClassifiedAndRecoverableErrorsStopAfterThreeAttempts(t *testing.T) {
	for _, phase := range []string{"discover", "download"} {
		t.Run(phase, func(t *testing.T) {
			for _, tc := range []struct {
				status   int
				code     string
				attempts int32
			}{{401, "source_unauthorized", 1}, {403, "source_forbidden", 1}, {404, "release_missing", 1}, {503, "source_unavailable", 3}} {
				t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
					var attempts atomic.Int32
					_, run := retryFixture(t, phase, func(w http.ResponseWriter, r *http.Request, _ []byte) {
						attempts.Add(1)
						http.Error(w, "fixture error", tc.status)
					})
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					err := run(ctx)
					if ErrorCode(err) != tc.code || attempts.Load() != tc.attempts {
						t.Fatalf("status=%d err=%v attempts=%d; want %s/%d", tc.status, err, attempts.Load(), tc.code, tc.attempts)
					}
				})
			}
		})
	}
}
