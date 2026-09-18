package cloudclient

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestDownloadSignedRequestDoesNotAddCloudBearer(t *testing.T) {
	body := "fixture zip bytes"
	hash := sha256.Sum256([]byte(body))
	httpClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Header.Get("Authorization") != "" {
			t.Fatalf("object request received Cloud bearer: %q", request.Header.Get("Authorization"))
		}
		if request.Header.Get("X-Signed") != "fixture" {
			t.Fatalf("signed header = %q", request.Header.Get("X-Signed"))
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})}
	client, err := New("https://cloud.example", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.DownloadSignedRequest(context.Background(), SignedRequest{
		URL: "https://objects.example/resource.zip", Method: http.MethodGet,
		Headers: map[string]string{"X-Signed": "fixture"},
	}, int64(len(body)), hex.EncodeToString(hash[:]), 1024)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(result.Path)
	if result.Size != int64(len(body)) || result.SHA256 != hex.EncodeToString(hash[:]) {
		t.Fatalf("result = %+v", result)
	}
	info, err := os.Stat(result.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o", info.Mode().Perm())
	}
}

func TestDownloadSignedRequestRejectsHashMismatch(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("wrong"))}, nil
	})}
	client, err := New("https://cloud.example", httpClient)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.DownloadSignedRequest(context.Background(), SignedRequest{
		URL: "https://objects.example/resource.zip", Method: http.MethodGet,
	}, 5, strings.Repeat("a", 64), 1024); err == nil {
		t.Fatal("hash mismatch must fail")
	}
}
