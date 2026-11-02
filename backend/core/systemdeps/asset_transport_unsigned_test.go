//go:build lazymind_unsigned_test

package systemdeps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnsignedAssetHTTPOnlyAllowsLiteralLoopback(t *testing.T) {
	for _, address := range []string{"http://127.0.0.1:18765/rag.zip", "https://example.invalid/rag.zip"} {
		if err := validateAssetURL(address); err != nil {
			t.Fatal("rejected test source", address, err)
		}
	}
	for _, address := range []string{"http://localhost/rag.zip", "http://127.0.0.2/rag.zip", "http://127.0.0.1.example.invalid/rag.zip", "http://192.0.2.1/rag.zip", "http://user@127.0.0.1/rag.zip", "http://127.0.0.1/rag.zip#fragment", "file:///tmp/rag.zip"} {
		if validateAssetURL(address) == nil {
			t.Fatal("accepted non-loopback or unsafe test source", address)
		}
	}
}

func TestUnsignedAssetHTTPRetainsDigestAndRedirectChecks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "http://example.invalid/rag.zip", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("test"))
	}))
	defer server.Close()
	entry := testPythonEntry()
	digest := sha256.Sum256([]byte("test"))
	entry.SHA256 = hex.EncodeToString(digest[:])
	file := filepath.Join(t.TempDir(), "component.zip")
	if err := downloadPythonComponent(context.Background(), entry, server.URL+"/rag.zip", file); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "test" {
		t.Fatal("verified download missing", err)
	}
	if err := downloadPythonComponent(context.Background(), entry, server.URL+"/redirect", file); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatal("redirect expanded test HTTP access", err)
	}
	entry.SHA256 = strings.Repeat("0", 64)
	if err := downloadPythonComponent(context.Background(), entry, server.URL+"/rag.zip", file); err == nil {
		t.Fatal("test mode accepted corrupt component bytes")
	}
}
