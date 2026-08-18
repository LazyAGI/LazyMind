package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lazymind/core/common"
	"lazymind/core/skillv2/testutil"
)

func TestNormalizeSkillImportURL(t *testing.T) {
	tests := []struct {
		name       string
		rawURL     string
		wantURL    string
		wantPrefix string
		wantErr    bool
	}{
		{
			name:    "GitHub repository root",
			rawURL:  "https://github.com/example/skills",
			wantURL: "https://github.com/example/skills/archive/HEAD.zip",
		},
		{
			name:       "GitHub tree subdirectory",
			rawURL:     "https://github.com/example/skills/tree/main/skills/target",
			wantURL:    "https://github.com/example/skills/archive/refs/heads/main.zip",
			wantPrefix: "skills/target",
		},
		{
			name:    "direct zip URL",
			rawURL:  "https://example.test/skills.zip",
			wantURL: "https://example.test/skills.zip",
		},
		{
			name:    "invalid scheme",
			rawURL:  "ftp://example.test/skills.zip",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotURL, gotPrefix, err := normalizeSkillImportURL(tt.rawURL)
			if tt.wantErr {
				if err == nil {
					t.Fatal("normalizeSkillImportURL returned nil error")
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeSkillImportURL returned error: %v", err)
			}
			if gotURL != tt.wantURL || gotPrefix != tt.wantPrefix {
				t.Fatalf("normalizeSkillImportURL = (%q, %q), want (%q, %q)", gotURL, gotPrefix, tt.wantURL, tt.wantPrefix)
			}
		})
	}
}

func TestCreateSkillFromInvalidURLReturnsInvalidParams(t *testing.T) {
	db := testutil.NewTestDB(t)
	withHandlerDB(t, db)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>not a zip</html>"))
	}))
	defer server.Close()

	payload, err := json.Marshal(map[string]any{
		"source": map[string]any{
			"type": "url",
			"url":  server.URL + "/skill",
		},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/core/skills", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "user_001")
	rec := httptest.NewRecorder()

	Create(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("Create status = %d, want %d; body=%s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	var response common.APIResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Code != common.ErrCodeInvalidParams {
		t.Fatalf("response code = %d, want %d", response.Code, common.ErrCodeInvalidParams)
	}
	if got := testutil.CountRows(t, db, "skills", ""); got != 0 {
		t.Fatalf("skills count = %d, want 0", got)
	}
}
