package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func deferredHistoryFixture(t *testing.T) (RuntimePaths, *httptest.Server, *atomic.Int32) {
	t.Helper()
	root := t.TempDir()
	fixture := filepath.Join(root, "fixture.zip")
	digest := writeHistoryInjectionPayloadArchive(t, fixture, map[string]string{"history-injection/ppt/sample.zip": "example payload"})
	data, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	requests := new(atomic.Int32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.Write(data) }))
	t.Cleanup(server.Close)
	return RuntimePaths{HistoryInjectionRoot: filepath.Join(root, "data/history-injection"),
		HistoryInjectionArchive: filepath.Join(root, "cache/history.zip"), HistoryInjectionSHA256: digest,
		HistoryInjectionDownload: &HistoryInjectionDownload{URL: server.URL, SHA256: digest, Size: int64(len(data))}}, server, requests
}

func TestDeferredHistoryDownloadsOnceAndStartsOfflineWithoutCache(t *testing.T) {
	paths, server, requests := deferredHistoryFixture(t)
	if err := prepareBundledHistoryInjection(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests=%d", requests.Load())
	}
	sample := filepath.Join(paths.HistoryInjectionRoot, "ppt/sample.zip")
	if data, err := os.ReadFile(sample); err != nil || string(data) != "example payload" {
		t.Fatalf("example=%q error=%v", data, err)
	}
	// Re-extract a deleted unpacked directory from the verified download cache.
	os.RemoveAll(paths.HistoryInjectionRoot)
	if err := prepareBundledHistoryInjection(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatal("downloaded despite verified archive cache")
	}
	server.Close()
	os.Remove(paths.HistoryInjectionArchive)
	if err := prepareBundledHistoryInjection(context.Background(), paths); err != nil {
		t.Fatalf("offline startup failed: %v", err)
	}
}

func TestDeferredHistoryRejectsCorruptionAndRetriesWithoutChangingOldExamples(t *testing.T) {
	paths, _, requests := deferredHistoryFixture(t)
	os.MkdirAll(paths.HistoryInjectionRoot, 0755)
	previous := filepath.Join(paths.HistoryInjectionRoot, "old.zip")
	os.WriteFile(previous, []byte("old"), 0644)
	validHash := paths.HistoryInjectionDownload.SHA256
	paths.HistoryInjectionDownload.SHA256 = strings.Repeat("0", 64)
	if err := prepareBundledHistoryInjection(context.Background(), paths); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("error=%v", err)
	}
	if data, err := os.ReadFile(previous); err != nil || string(data) != "old" {
		t.Fatal("changed existing examples")
	}
	if _, err := os.Stat(paths.HistoryInjectionArchive); !os.IsNotExist(err) {
		t.Fatal("activated invalid archive")
	}
	temporary, _ := filepath.Glob(filepath.Join(filepath.Dir(paths.HistoryInjectionArchive), "*.tmp"))
	if len(temporary) != 0 {
		t.Fatal("left partial download")
	}
	paths.HistoryInjectionDownload.SHA256 = validHash
	if err := prepareBundledHistoryInjection(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatalf("retry requests=%d", requests.Load())
	}
}

func TestDeferredHistoryNetworkFailureIsOptionalButCancellationIsNot(t *testing.T) {
	paths, server, _ := deferredHistoryFixture(t)
	server.Close()
	var output bytes.Buffer
	manager := RuntimeManager{now: time.Now, out: &output}
	if err := manager.prepareStartupHistoryInjection(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"event":"phase.skipped"`) || strings.Contains(output.String(), `"event":"phase.failed"`) {
		t.Fatal(output.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := manager.prepareStartupHistoryInjection(ctx, paths); !errors.Is(err, context.Canceled) {
		t.Fatalf("ignored cancellation: %v", err)
	}
	paths.HistoryInjectionDownload = nil
	if err := manager.prepareStartupHistoryInjection(context.Background(), paths); err == nil {
		t.Fatal("bundled corrupt/missing package must still fail validation")
	}
}

func TestDeferredHistoryRejectsWrongSizeAndUntrustedURL(t *testing.T) {
	paths, _, _ := deferredHistoryFixture(t)
	paths.HistoryInjectionDownload.Size--
	if err := prepareBundledHistoryInjection(context.Background(), paths); err == nil || !strings.Contains(err.Error(), "size mismatch") {
		t.Fatalf("error=%v", err)
	}
	for _, url := range []string{"http://example.com/package.zip", "file:///tmp/package.zip", "https://user:password@example.com/"} {
		paths.HistoryInjectionDownload.URL = url
		if err := paths.HistoryInjectionDownload.validate(); err == nil {
			t.Fatalf("accepted %s", url)
		}
	}
}

func TestDeferredHistoryManifestUsesMutableCacheAndDataPaths(t *testing.T) {
	root := t.TempDir()
	resources := filepath.Join(root, "resources")
	os.MkdirAll(resources, 0755)
	download := &HistoryInjectionDownload{URL: "https://modelscope.cn/example.zip", SHA256: strings.Repeat("a", 64), Size: 123}
	manifest := RuntimeManifest{Version: 1, Profile: "desktop", HistoryInjectionDownload: download}
	data, _ := json.Marshal(manifest)
	os.WriteFile(filepath.Join(resources, runtimeManifestFileName), data, 0644)
	paths := RuntimePaths{ResourcesRoot: resources, RuntimeRoot: filepath.Join(root, "user"), DataDir: filepath.Join(root, "user/data")}
	if err := applyDesktopManifestPaths(&paths); err != nil {
		t.Fatal(err)
	}
	if paths.HistoryInjectionDownload == nil || paths.HistoryInjectionSHA256 != download.SHA256 {
		t.Fatal("lost download identity")
	}
	if paths.HistoryInjectionRoot != filepath.Join(paths.DataDir, "history-injection") {
		t.Fatal(paths.HistoryInjectionRoot)
	}
	if paths.HistoryInjectionArchive != filepath.Join(paths.RuntimeRoot, "cache/history-injection", download.SHA256+".zip") {
		t.Fatal(paths.HistoryInjectionArchive)
	}
}

// Optional release validation against the exact already-published ModelScope asset.
func TestDeferredHistoryPublishedModelScopePackage(t *testing.T) {
	if os.Getenv("LAZYMIND_TEST_HISTORY_DOWNLOAD") != "1" {
		t.Skip("network release validation is opt-in")
	}
	data, err := os.ReadFile("../../desktop/history-injection-package.json")
	if err != nil {
		t.Fatal(err)
	}
	var descriptor HistoryInjectionDownload
	if err = json.Unmarshal(data, &descriptor); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	paths := RuntimePaths{HistoryInjectionDownload: &descriptor, HistoryInjectionSHA256: descriptor.SHA256,
		HistoryInjectionRoot: filepath.Join(root, "data/history-injection"), HistoryInjectionArchive: filepath.Join(root, "cache", descriptor.SHA256+".zip")}
	if err := prepareBundledHistoryInjection(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
	if count := historyInjectionBundleCount(paths.HistoryInjectionRoot); count != 5 {
		t.Fatalf("got %d published bundles, want 5", count)
	}
	if err := os.Remove(paths.HistoryInjectionArchive); err != nil {
		t.Fatal(err)
	}
	if err := prepareBundledHistoryInjection(context.Background(), paths); err != nil {
		t.Fatal(err)
	}
}
