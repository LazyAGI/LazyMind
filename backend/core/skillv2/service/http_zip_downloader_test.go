package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"

	"lazymind/core/skillv2/httperr"
	"testing"
	"time"
)

func TestSkillArchiveHTTPClientAllowsSlowResponseBodies(t *testing.T) {
	client := newSkillArchiveHTTPClient()
	if client.Timeout != skillArchiveDownloadTimeout {
		t.Fatalf("download timeout = %s, want %s", client.Timeout, skillArchiveDownloadTimeout)
	}
	if client.Timeout <= 30*time.Second {
		t.Fatalf("download timeout = %s, must allow response bodies longer than 30 seconds", client.Timeout)
	}

	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport type = %T, want *http.Transport", client.Transport)
	}
	if transport.ResponseHeaderTimeout != 30*time.Second {
		t.Fatalf("response header timeout = %s, want 30s", transport.ResponseHeaderTimeout)
	}
}

func TestSkillArchiveDownloadAcceptsArchiveLargerThanTwentyMiB(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(21<<20))
		_, _ = io.CopyN(w, packageZeroReader{}, 21<<20)
	}))
	defer server.Close()
	downloaded, err := (HTTPZipDownloader{}).Download(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer downloaded.Cleanup()
	info, err := os.Stat(downloaded.Path)
	if err != nil || info.Size() != 21<<20 {
		t.Fatalf("download size=%v error=%v", info, err)
	}
}

func TestSkillArchiveDownloadReportsTypedSizeErrors(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !stream {
					w.Header().Set("Content-Length", strconv.Itoa((100<<20)+1))
					w.WriteHeader(200)
					return
				}
				w.WriteHeader(200)
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				_, _ = io.CopyN(w, packageZeroReader{}, (100<<20)+1)
			}))
			defer server.Close()
			_, err := (HTTPZipDownloader{}).Download(context.Background(), server.URL)
			if err == nil {
				t.Fatal("expected size error")
			}
			if sem := httperr.ForError(err); sem.Status != 413 || sem.Code != httperr.CodePayloadTooLarge {
				t.Fatalf("error=%+v", sem)
			}
		})
	}
}

func TestSkillArchiveDownloadReportsTypedUpstreamErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests, http.StatusBadGateway} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
			}))
			defer server.Close()
			_, err := (HTTPZipDownloader{}).Download(context.Background(), server.URL)
			if err == nil {
				t.Fatal("expected source error")
			}
			sem := httperr.ForError(err)
			if status == http.StatusNotFound && (sem.Status != http.StatusNotFound || sem.Code != httperr.CodeNotFound) {
				t.Fatalf("error=%+v", sem)
			}
			if status != http.StatusNotFound && sem.Code != httperr.CodeSourceUnavailable {
				t.Fatalf("error=%+v", sem)
			}
			if status != http.StatusNotFound && status != http.StatusTooManyRequests && sem.Status != http.StatusBadGateway {
				t.Fatalf("error=%+v", sem)
			}
			if status == http.StatusTooManyRequests && sem.Status != http.StatusTooManyRequests {
				t.Fatalf("error=%+v", sem)
			}
		})
	}
}

type packageZeroReader struct{}

func (packageZeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }
