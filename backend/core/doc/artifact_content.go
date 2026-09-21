package doc

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// ReadLocalArtifactFile reads bounded content under the same roots used for
// static Artifact files. OpenRoot also confines symlinks during the actual open.
// Callers must authorize the owning Artifact before supplying its stored path.
func ReadLocalArtifactFile(path string, limit int64) ([]byte, error) {
	if !filepath.IsAbs(path) || limit < 1 {
		return nil, errors.New("invalid artifact file")
	}
	path = filepath.Clean(rewriteCanonicalUploadPath(path))
	for _, directory := range []string{UploadRoot(), subagentWorkspaceRoot()} {
		directory, err := filepath.Abs(directory)
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		root, err := os.OpenRoot(directory)
		if err != nil {
			return nil, err
		}
		info, err := root.Stat(relative)
		if err != nil {
			root.Close()
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > limit {
			root.Close()
			return nil, errors.New("invalid artifact file size or type")
		}
		file, err := root.Open(relative)
		root.Close()
		if err != nil {
			return nil, err
		}
		defer file.Close()
		info, err = file.Stat()
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > limit {
			return nil, errors.New("invalid artifact file size or type")
		}
		content, err := io.ReadAll(io.LimitReader(file, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(content)) > limit {
			return nil, errors.New("artifact file exceeds limit")
		}
		return content, nil
	}
	return nil, errors.New("artifact file is outside storage")
}
