package pdfir

import (
	"crypto/x509"
	"errors"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedArchiveCacheValidatesSourceAndModel(t *testing.T) {
	source := filepath.Join(t.TempDir(), "paper.pdf")
	hash := strings.Repeat("a", 64)
	raw := archive(t, `[{"type":"text","text":"hello","page_idx":0,"bbox":[0,0,1,1]}]`)
	SaveCachedArchive(source, hash, "vlm", raw)
	if len(ReadCachedArchive(source, hash, "vlm")) == 0 {
		t.Fatal("cache miss")
	}
	if ReadCachedArchive(source, strings.Repeat("b", 64), "vlm") != nil {
		t.Fatal("source mismatch")
	}
	if ReadCachedArchive(source, hash, "pipeline") != nil {
		t.Fatal("model mismatch")
	}
	SaveCachedArchive(source, hash, "vlm", []byte("broken"))
	if ReadCachedArchive(source, hash, "vlm") != nil {
		t.Fatal("invalid archive reused")
	}
}
func TestMineruTransportDiagnosticDoesNotLeakURL(t *testing.T) {
	err := &url.Error{Op: "Get", URL: "https://storage/path?secret=token", Err: x509.HostnameError{Certificate: &x509.Certificate{}, Host: "mineru.net"}}
	got := mineruTransportError(err).Error()
	if !strings.Contains(got, "hostname mismatch") || strings.Contains(got, "secret") {
		t.Fatal(got)
	}
	if strings.Contains(mineruTransportError(errors.New("secret=token")).Error(), "secret") {
		t.Fatal("leaked transport detail")
	}
}
