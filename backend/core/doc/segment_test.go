package doc

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMapChunkToSegmentBuildsSignedDisplayContent(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)

	sourcePath := filepath.Join(root, ".image_cache", "doc-1", "images", "demo.jpg")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0o755); err != nil {
		t.Fatalf("create dir: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("img"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	seg := mapChunkToSegment("dataset-1", "doc-1", map[string]any{
		"uid":     "chunk-1",
		"content": "demo.jpg",
		"metadata": map[string]any{
			"source_path": sourcePath,
			"file_name":   "report.pdf",
		},
	})

	if !strings.HasPrefix(seg.DisplayContent, "![report.pdf](/static-files/") {
		t.Fatalf("expected signed markdown display content, got %q", seg.DisplayContent)
	}
	if len(seg.ImageKeys) != 1 || !strings.HasPrefix(seg.ImageKeys[0], "/static-files/") {
		t.Fatalf("expected signed image key, got %#v", seg.ImageKeys)
	}
}

func TestRefreshStaticFileURL(t *testing.T) {
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)

	rel := "tenants/root/normalized_images/demo.jpg"
	fullPath := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("create dir: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte("img"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	expired := "/static-files/" + rel + "?expires=1&sig=deadbeef"
	refreshed := refreshStaticFileURL(expired)
	if !strings.HasPrefix(refreshed, "/static-files/") || strings.Contains(refreshed, "expires=1") {
		t.Fatalf("expected refreshed signed url, got %q", refreshed)
	}
}

	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)

	fullPath := filepath.Join(root, "tenants", "root", "normalized_images", "root", "frame.jpg")
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("create dir: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte("img"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	signed := signSegmentImageKeys([]string{fullPath, "/static-files/tenants/root/a.png?expires=1&sig=x"})
	if !strings.HasPrefix(signed[0], "/static-files/") || !strings.Contains(signed[0], "sig=") {
		t.Fatalf("expected signed static file url, got %q", signed[0])
	}
	if signed[1] != "/static-files/tenants/root/a.png?expires=1&sig=x" {
		t.Fatalf("expected existing static path unchanged, got %q", signed[1])
	}
}

func TestBuildParserChunksURLUsesOffsetPagination(t *testing.T) {
	t.Setenv("LAZYMIND_PARSING_SERVICE_URL", "http://parser:8000/")

	got := buildParserChunksURL("kb-1", "algo-1", "doc-1", "block", 3, 12)
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse URL: %v", err)
	}
	if want := "http://parser:8000/doc/chunks"; parsed.Scheme+"://"+parsed.Host+parsed.Path != want {
		t.Fatalf("expected parser chunks URL %q, got %q", want, got)
	}
	q := parsed.Query()
	for key, want := range map[string]string{
		"kb_id":     "kb-1",
		"doc_id":    "doc-1",
		"group":     "block",
		"algo_id":   "algo-1",
		"offset":    "24",
		"page_size": "12",
	} {
		if got := q.Get(key); got != want {
			t.Fatalf("expected query %s=%q, got %q in %s", key, want, got, parsed.RawQuery)
		}
	}
}

func TestParseChunkSearchResponseAcceptsParserChunksShape(t *testing.T) {
	raw := map[string]any{
		"code": 200.0,
		"msg":  "success",
		"data": map[string]any{
			"items": []any{
				map[string]any{
					"uid":     "chunk-1",
					"doc_id":  "lazy-doc-1",
					"kb_id":   "dataset-1",
					"group":   "block",
					"number":  1.0,
					"content": "hello",
				},
			},
			"total":     25.0,
			"offset":    0.0,
			"page_size": 12.0,
		},
	}

	segments, total, next := parseChunkSearchResponse("dataset-1", "doc-1", raw, 1, 12)

	if total != 25 {
		t.Fatalf("expected total 25, got %d", total)
	}
	if next == "" {
		t.Fatalf("expected next page token")
	}
	if len(segments) != 1 {
		t.Fatalf("expected one segment, got %d", len(segments))
	}
	seg := segments[0]
	if seg.SegmentID != "chunk-1" || seg.DatasetID != "dataset-1" || seg.DocumentID != "lazy-doc-1" || seg.Content != "hello" {
		t.Fatalf("unexpected segment mapping: %+v", seg)
	}
}
