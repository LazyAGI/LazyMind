package coreapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/credentials"
)

func TestOriginalUploadStreamsExactBytesAndRejectsRedirect(t *testing.T) {
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/core/knowledge/originals" {
			t.Errorf("path %s", r.URL.Path)
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Error(err)
			return
		}
		part, err := reader.NextPart()
		if err != nil {
			t.Error(err)
			return
		}
		bytes, _ := io.ReadAll(part)
		received = string(bytes)
		if part.FileName() != "source.txt" {
			t.Errorf("filename %s", part.FileName())
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"file_ref":"upload-1"}`)
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "source.txt")
	os.WriteFile(path, []byte("exact bytes"), 0600)
	file, _ := os.Open(path)
	defer file.Close()
	c := &Client{http: server.Client()}
	ref, err := c.uploadOriginalTo(context.Background(), server.URL, file, "source.txt", 11)
	if err != nil || ref != "upload-1" || received != "exact bytes" {
		t.Fatalf("ref=%s bytes=%s err=%v", ref, received, err)
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	if _, err := c.uploadOriginalTo(context.Background(), redirect.URL, file, "source.txt", 11); err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("redirect accepted: %v", err)
	}
}

func TestOriginalUploadReplaysStreamAfterCredentialRefresh(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/core/knowledge/originals":
			calls++
			reader, err := r.MultipartReader()
			if err != nil {
				t.Error(err)
				return
			}
			part, err := reader.NextPart()
			if err != nil {
				t.Error(err)
				return
			}
			data, _ := io.ReadAll(part)
			if string(data) != "repeat exact bytes" {
				t.Errorf("attempt %d body %q", calls, data)
			}
			if r.Header.Get("Authorization") != "Bearer new-access" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			io.WriteString(w, `{"file_ref":"replayed-original"}`)
		case "/api/authservice/auth/refresh":
			w.WriteHeader(http.StatusUnauthorized)
		case "/_local/admin-session":
			io.WriteString(w, `{"token":"new-access","refreshToken":"new-refresh","username":"admin"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	store, err := credentials.NewStore(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(credentials.Credentials{ServerURL: server.URL, AccessToken: "old-access", RefreshToken: "old-refresh"}); err != nil {
		t.Fatal(err)
	}
	client, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(path, []byte("repeat exact bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	ref, err := client.UploadOriginal(context.Background(), file, "source.txt")
	if err != nil || ref != "replayed-original" || calls != 2 {
		t.Fatalf("ref=%s calls=%d err=%v", ref, calls, err)
	}
}
