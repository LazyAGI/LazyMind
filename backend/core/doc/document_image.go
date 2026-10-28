package doc

import (
	"context"
	"errors"
	"net/url"
	"path/filepath"
	"strings"

	"lazymind/core/staticstorage"
)

// ReadDocumentImage reads an image already referenced by an authorized artifact.
// It never follows an HTTP URL. Static namespaces and upload ownership still apply.
func ReadDocumentImage(ctx context.Context, source, owner string, limit int64) ([]byte, error) {
	rel := staticFileRelativePath(source)
	decoded, err := url.PathUnescape(rel)
	if owner == "" || rel == "" || err != nil || decoded != rel ||
		!staticstorage.Authorized(ctx, rel, owner) ||
		(isTempUserUploadRel(rel) && !TempUserUploadOwnedBy(source, owner)) {
		return nil, errors.New("image unavailable")
	}
	if strings.HasPrefix(rel, "subagent/") && !strings.HasPrefix(rel, "subagent/"+safePathPart(owner)+"/") {
		return nil, errors.New("image unavailable")
	}
	path := resolveSignedStaticFullPath(rel)
	if !filepath.IsAbs(path) {
		return nil, errors.New("image unavailable")
	}
	return ReadLocalArtifactFile(path, limit)
}
