package doc

import (
	"context"
	"io"
	"path/filepath"
	"slices"
	"strings"
)

const OriginalFileMaxBytes int64 = 500 << 20
const OriginalBatchMaxBytes int64 = 1 << 30

// KnowledgeFileTypes is the shared page/MCP import catalog. Storage support does
// not imply that a deployment has a parser for every format.
var KnowledgeFileTypes = []string{
	"pdf", "docx", "doc", "ppt", "pptx", "pptm", "jpg", "jpeg", "png", "gif",
	"bmp", "webp", "tiff", "tif", "ipynb", "epub", "md", "mbox", "csv", "xls",
	"xlsx", "mp3", "mp4", "txt", "xml", "json", "jsonl", "yaml", "yml", "html", "htm", "py",
}

// AcquireLocalOriginal copies from the same opened file that was validated.
// Dataset authorization belongs to the caller and must precede this operation.
func (s *OriginalService) AcquireLocalOriginal(ctx context.Context, caller DatasetCatalogCaller, path string, remaining int64) (OriginalRef, error) {
	if !filepath.IsAbs(path) {
		return OriginalRef{}, originalError("INVALID_ARGUMENT", "absolute local file path required")
	}
	if !slices.Contains(KnowledgeFileTypes, strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))) {
		return OriginalRef{}, originalError("UNSUPPORTED_FORMAT", "unsupported original file format")
	}
	f, err := openLocalOriginal(path)
	if err != nil {
		return OriginalRef{}, originalError("INVALID_ARGUMENT", "local file cannot be opened")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return OriginalRef{}, originalError("INVALID_ARGUMENT", "local path must be a regular file")
	}
	if info.Size() <= 0 || info.Size() > OriginalFileMaxBytes || info.Size() > remaining {
		return OriginalRef{}, originalError("INVALID_ARGUMENT", "empty file or upload size limit exceeded")
	}
	ref, err := s.AcquireOriginal(ctx, caller, AcquireOriginalRequest{
		Filename: filepath.Base(path), Reader: io.LimitReader(f, min(remaining, OriginalFileMaxBytes)+1),
	})
	if err != nil {
		return ref, err
	}
	after, err := f.Stat()
	if err != nil || ref.Size != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return OriginalRef{}, originalError("CONFLICT", "local file changed while importing")
	}
	return ref, nil
}
