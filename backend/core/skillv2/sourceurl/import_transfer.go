package sourceurl

import (
	"errors"
	"io"
	"net/http"
	"sync/atomic"

	"lazymind/core/skillv2/skillpackage"
)

const MaxImportTransferBytes int64 = skillpackage.MaxArchiveBytes

var ErrImportTransferLimit = errors.New("skill package download exceeds transfer limit")

// PreparedPackage is owned by one resolution result. Retaining only the selected
// files lets archive-based discovery hand its bytes to import without a global
// file cache or a temporary artifact that an abandoned resolution could leak.
type PreparedPackage struct {
	Files       map[string][]byte
	PackageRoot string
}

type importTransferTransport struct {
	base        http.RoundTripper
	cacheClient *http.Client
	received    atomic.Int64
}

func (t *importTransferTransport) GitHubAPICacheClient() *http.Client { return t.cacheClient }
func (t *importTransferTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	if response.ContentLength > MaxImportTransferBytes-t.received.Load() {
		response.Body.Close()
		return nil, ErrImportTransferLimit
	}
	response.Body = &importTransferBody{ReadCloser: response.Body, total: &t.received}
	return response, nil
}

type importTransferBody struct {
	io.ReadCloser
	total *atomic.Int64
}

func (b *importTransferBody) Read(p []byte) (int, error) {
	remaining := MaxImportTransferBytes - b.total.Load()
	if remaining < 0 {
		return 0, ErrImportTransferLimit
	}
	if int64(len(p)) > remaining+1 {
		p = p[:remaining+1]
	}
	n, err := b.ReadCloser.Read(p)
	if b.total.Add(int64(n)) > MaxImportTransferBytes {
		return n, ErrImportTransferLimit
	}
	return n, err
}
