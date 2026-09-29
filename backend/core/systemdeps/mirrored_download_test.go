package systemdeps

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestMirroredAssetFallbackAndIntegrity(t *testing.T) {
	for _, mode := range []string{"success", "missing", "corrupt", "no-first-byte", "slow-body"} {
		t.Run(mode, func(t *testing.T) {
			var fallbackCalls atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/hf/asset.zip" {
					fallbackCalls.Add(1)
					w.Write([]byte("test"))
					return
				}
				switch mode {
				case "missing":
					http.NotFound(w, r)
				case "corrupt":
					w.Write([]byte("oops"))
				case "no-first-byte":
					<-r.Context().Done()
				case "slow-body":
					w.Write([]byte("t"))
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				default:
					w.Write([]byte("test"))
				}
			}))
			defer server.Close()
			destination := filepath.Join(t.TempDir(), "download.zip")
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err := downloadMirroredAsset(ctx, server.Client(), server.URL+"/ms/asset.zip", server.URL+"/hf/asset.zip",
				"asset.zip", destination, 4, fmt.Sprintf("%x", sha256.Sum256([]byte("test"))),
				mirrorDownloadPolicy{40 * time.Millisecond, 40 * time.Millisecond, 4})
			if err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(destination)
			if err != nil || string(body) != "test" {
				t.Fatalf("bad result: %q, %v", body, err)
			}
			expected := int32(1)
			if mode == "success" {
				expected = 0
			}
			if fallbackCalls.Load() != expected {
				t.Fatalf("fallback called %d times", fallbackCalls.Load())
			}
		})
	}
}

func TestMirroredAssetCancellationDoesNotStartFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var fallbackCalls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hf/asset.zip" {
			fallbackCalls.Add(1)
		} else {
			cancel()
			<-r.Context().Done()
		}
	}))
	defer server.Close()
	err := downloadMirroredAsset(ctx, server.Client(), server.URL+"/ms/asset.zip", server.URL+"/hf/asset.zip",
		"asset.zip", filepath.Join(t.TempDir(), "asset.zip"), 4, "unused", primaryMirrorPolicy)
	if err == nil || fallbackCalls.Load() != 0 {
		t.Fatalf("cancellation ignored: %v, fallback=%d", err, fallbackCalls.Load())
	}
}

func TestMirroredAssetRejectsCorruptFallbackAndUnsafeSources(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("oops"))
	}))
	defer server.Close()
	destination := filepath.Join(t.TempDir(), "asset.zip")
	err := downloadMirroredAsset(context.Background(), server.Client(), server.URL+"/ms/asset.zip",
		server.URL+"/hf/asset.zip", "asset.zip", destination, 4, fmt.Sprintf("%x", sha256.Sum256([]byte("test"))), primaryMirrorPolicy)
	if err == nil {
		t.Fatal("accepted invalid mirrors")
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("corrupt partial file remains")
	}
	for _, fallback := range []string{"http://example.com/asset.zip", "https://user:pass@example.com/asset.zip",
		"https://example.com/different.zip"} {
		if validateAssetMirrors(server.URL+"/asset.zip", fallback, "asset.zip") == nil {
			t.Fatalf("accepted %s", fallback)
		}
	}
}
