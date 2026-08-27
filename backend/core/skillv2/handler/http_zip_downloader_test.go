package handler

import (
	"net/http"
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
