package worker

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFileTempObjectStorePutOpenCleanup(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewFileTempObjectStore(t.TempDir())
	object, err := store.Put(ctx, TempObjectInput{Reader: strings.NewReader("hello temp")})
	if err != nil {
		t.Fatalf("put temp object: %v", err)
	}
	if !strings.HasPrefix(object.URI, "scan-temp://") || strings.Contains(object.URI, store.baseDir) {
		t.Fatalf("temp object URI must be scan-temp token without local path: %+v", object)
	}
	if object.CleanupToken == "" || object.SizeBytes != int64(len("hello temp")) {
		t.Fatalf("unexpected temp object metadata: %+v", object)
	}

	reader, err := store.Open(ctx, object.URI)
	if err != nil {
		t.Fatalf("open temp object: %v", err)
	}
	content, err := io.ReadAll(reader)
	if closeErr := reader.Close(); closeErr != nil {
		t.Fatalf("close temp object: %v", closeErr)
	}
	if err != nil {
		t.Fatalf("read temp object: %v", err)
	}
	if string(content) != "hello temp" {
		t.Fatalf("unexpected temp content %q", string(content))
	}

	if err := store.Cleanup(ctx, object.CleanupToken); err != nil {
		t.Fatalf("cleanup temp object: %v", err)
	}
	if _, err := store.Open(ctx, object.URI); !errors.Is(err, errTempNotFound) {
		t.Fatalf("open after cleanup should report not found, got %v", err)
	}
}

func TestFileTempObjectStoreRejectsInvalidURI(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewFileTempObjectStore(t.TempDir())
	object, err := store.Put(ctx, TempObjectInput{Reader: strings.NewReader("x")})
	if err != nil {
		t.Fatalf("put temp object: %v", err)
	}
	token := strings.TrimPrefix(object.URI, "scan-temp://")
	for _, uri := range []string{
		"file:///tmp/" + token,
		"https://example.test/" + token,
		"scan-temp://",
		"scan-temp://" + token + "/extra",
		"scan-temp://" + token + "?path=../x",
		"scan-temp://" + token + "#fragment",
	} {
		if _, err := store.Open(ctx, uri); !errors.Is(err, errInvalidTempURI) && !errors.Is(err, errInvalidTempToken) {
			t.Fatalf("open %q should reject invalid URI, got %v", uri, err)
		}
	}
}

func TestFileTempObjectStoreRejectsPathTraversal(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewFileTempObjectStore(t.TempDir())
	for _, value := range []string{
		"scan-temp://../secret",
		"scan-temp://..%2fsecret",
		"scan-temp://abc/../../secret",
		"../secret",
		"/tmp/secret",
		"0123456789abcdef0123456789abcde/",
	} {
		if _, err := store.Open(ctx, value); err == nil {
			t.Fatalf("open %q should reject traversal", value)
		}
		if err := store.Cleanup(ctx, value); err == nil {
			t.Fatalf("cleanup %q should reject traversal", value)
		}
	}
}

func TestFileTempObjectStoreRejectsUnknownTokenEvenIfFileExists(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	baseDir := t.TempDir()
	store := NewFileTempObjectStore(baseDir)
	token := "0123456789abcdef0123456789abcdef"
	if err := os.WriteFile(filepath.Join(baseDir, token), []byte("external"), 0o600); err != nil {
		t.Fatalf("seed external temp file: %v", err)
	}
	if _, err := store.Open(ctx, "scan-temp://"+token); !errors.Is(err, errTempNotFound) {
		t.Fatalf("unknown token should not be openable, got %v", err)
	}
	if err := store.Cleanup(ctx, token); err != nil {
		t.Fatalf("unknown token cleanup should no-op: %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, token)); err != nil {
		t.Fatalf("unknown token cleanup should not remove external file: %v", err)
	}
}

func TestFileTempObjectStoreCleanupIsIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := NewFileTempObjectStore(t.TempDir())
	object, err := store.Put(ctx, TempObjectInput{Reader: strings.NewReader("x")})
	if err != nil {
		t.Fatalf("put temp object: %v", err)
	}
	if err := store.Cleanup(ctx, object.CleanupToken); err != nil {
		t.Fatalf("first cleanup: %v", err)
	}
	if err := store.Cleanup(ctx, object.CleanupToken); err != nil {
		t.Fatalf("second cleanup must be idempotent: %v", err)
	}
	if err := store.Cleanup(ctx, object.URI); err != nil {
		t.Fatalf("cleanup URI form must also be idempotent: %v", err)
	}
}

func TestFileTempObjectStoreCleanupExpired(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := t.TempDir()
	store := NewFileTempObjectStore(dir)
	expired, err := store.Put(ctx, TempObjectInput{Reader: strings.NewReader("old")})
	if err != nil {
		t.Fatalf("put expired temp object: %v", err)
	}
	fresh, err := store.Put(ctx, TempObjectInput{Reader: strings.NewReader("fresh")})
	if err != nil {
		t.Fatalf("put fresh temp object: %v", err)
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(filepath.Join(dir, strings.TrimPrefix(expired.URI, "scan-temp://")), oldTime, oldTime); err != nil {
		t.Fatalf("age expired object: %v", err)
	}

	cleaned, err := store.CleanupExpired(ctx, time.Hour)
	if err != nil {
		t.Fatalf("cleanup expired: %v", err)
	}
	if cleaned != 1 {
		t.Fatalf("expected one expired object cleaned, got %d", cleaned)
	}
	if _, err := store.Open(ctx, expired.URI); !errors.Is(err, errTempNotFound) {
		t.Fatalf("expired object should be removed, got %v", err)
	}
	if reader, err := store.Open(ctx, fresh.URI); err != nil {
		t.Fatalf("fresh object should remain: %v", err)
	} else {
		_ = reader.Close()
	}
}
