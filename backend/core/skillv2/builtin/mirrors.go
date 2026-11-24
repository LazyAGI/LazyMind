package builtin

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const (
	originSource      = "origin"
	modelScopeSource  = "modelscope"
	huggingFaceSource = "hugging_face"

	originDownloadTimeout = 5 * time.Minute
	mirroredSourceTimeout = 90 * time.Second

	modelScopeMirrorBase  = "https://modelscope.cn/datasets/CarlosShaoting/lazymind-cst/resolve/"
	huggingFaceMirrorBase = "https://huggingface.co/datasets/LazyAGI/LazyMind/resolve/"
)

//go:embed mirrors.json
var mirrorSnapshotJSON []byte

type mirrorArtifact struct {
	Filename      string `json:"filename"`
	ArchiveSHA256 string `json:"archive_sha256"`
}

type mirrorSnapshot struct {
	ModelScopeRevision  string                    `json:"modelscope_revision"`
	HuggingFaceRevision string                    `json:"hugging_face_revision"`
	Packages            map[string]mirrorArtifact `json:"packages"`
}

var loadMirrorSnapshot = sync.OnceValues(func() (mirrorSnapshot, error) {
	var snapshot mirrorSnapshot
	err := json.Unmarshal(mirrorSnapshotJSON, &snapshot)
	return snapshot, err
})

type packageSource struct {
	name string
	url  string
}

func packageSources(entry CatalogSkill) ([]packageSource, time.Duration) {
	sources := []packageSource{{name: originSource}, {name: modelScopeSource}, {name: huggingFaceSource}}
	snapshot, err := loadMirrorSnapshot()
	if err != nil {
		return sources, originDownloadTimeout
	}
	artifact, ok := snapshot.Packages[entry.UID]
	if !ok || artifact.ArchiveSHA256 != entry.ArchiveSHA256 {
		return sources, originDownloadTimeout
	}
	path := "/Skills/" + url.PathEscape(artifact.Filename)
	sources[1].url = modelScopeMirrorBase + snapshot.ModelScopeRevision + path
	sources[2].url = huggingFaceMirrorBase + snapshot.HuggingFaceRevision + path
	return sources, mirroredSourceTimeout
}

var (
	errMirrorUnpublished = errors.New("no mirror published for the locked package")
	errSourceInvalid     = errors.New("invalid download source")
	errPackageValidation = errors.New("package validation failed")
)

type downloadHTTPError int

func (e downloadHTTPError) Error() string { return fmt.Sprintf("download returned HTTP %d", e) }

type SourceFailure struct {
	Source     string `json:"source"`
	Reason     string `json:"reason"`
	HTTPStatus int    `json:"http_status,omitempty"`
}

type PackageDownloadError struct {
	Failures []SourceFailure
	cause    error
}

func (e *PackageDownloadError) Error() string { return e.cause.Error() }
func (e *PackageDownloadError) Unwrap() error { return e.cause }

func (source packageSource) failure(err error) SourceFailure {
	failure := SourceFailure{Source: source.name}
	var response downloadHTTPError
	var network net.Error
	switch {
	case errors.Is(err, errMirrorUnpublished):
		failure.Reason = "not_published"
	case errors.Is(err, errSourceInvalid):
		failure.Reason = "invalid_source"
	case errors.Is(err, errPackageValidation):
		failure.Reason = "invalid_package"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &network) && network.Timeout():
		failure.Reason = "timeout"
	case errors.As(err, &response):
		switch response {
		case http.StatusNotFound, http.StatusGone:
			failure.Reason = "missing"
		case http.StatusRequestTimeout, http.StatusGatewayTimeout:
			failure.Reason = "timeout"
		default:
			failure.Reason = "http_error"
			failure.HTTPStatus = int(response)
		}
	default:
		failure.Reason = "unreachable"
	}
	return failure
}
