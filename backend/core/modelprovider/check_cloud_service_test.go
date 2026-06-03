package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDoMinerUCloudServiceCheckUsesReaderRequestShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/file-urls/batch" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if got := r.Header.Get("Content-Type"); !strings.Contains(got, "application/json") {
			t.Fatalf("unexpected content type: %q", got)
		}
		var payload struct {
			Files []struct {
				Name string `json:"name"`
			} `json:"files"`
			ModelVersion string `json:"model_version"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if len(payload.Files) != 1 || payload.Files[0].Name == "" {
			t.Fatalf("unexpected files payload: %+v", payload.Files)
		}
		if payload.ModelVersion != "vlm" {
			t.Fatalf("unexpected model_version: %q", payload.ModelVersion)
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"missing uploaded file"}`))
	}))
	defer server.Close()

	result, err := doMinerUCloudServiceCheck(t.Context(), "MinerU", server.URL+"/api/v4", "test-key")
	if err != nil {
		t.Fatalf("doMinerUCloudServiceCheck error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected auth-level success, got %+v", result)
	}
}

func TestDoPaddleOCRCloudServiceCheckUsesReaderRequestShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/ocr/jobs" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "bearer test-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if err := r.ParseMultipartForm(1024 * 1024); err != nil {
			t.Fatalf("parse multipart form: %v", err)
		}
		if got := r.FormValue("model"); got != paddleOCRDefaultModel {
			t.Fatalf("unexpected model: %q", got)
		}
		if got := r.FormValue("optionalPayload"); got != paddleOCROptionalPayload {
			t.Fatalf("unexpected optionalPayload: %q", got)
		}
		if len(r.MultipartForm.File) != 0 {
			t.Fatalf("expected no file upload in key check, got %d file fields", len(r.MultipartForm.File))
		}
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"file is required"}`))
	}))
	defer server.Close()

	result, err := doPaddleOCRCloudServiceCheck(t.Context(), "PaddleOCR", server.URL+"/api/v2/ocr/jobs", "test-key")
	if err != nil {
		t.Fatalf("doPaddleOCRCloudServiceCheck error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected auth-level success, got %+v", result)
	}
}

func TestDoTavilySearchCloudServiceCheckUsesOfficialRequestShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		if got := r.Header.Get("Content-Type"); !strings.Contains(got, "application/json") {
			t.Fatalf("unexpected content type: %q", got)
		}
		var payload struct {
			Query      string `json:"query"`
			MaxResults int    `json:"max_results"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if payload.Query == "" || payload.MaxResults != 1 {
			t.Fatalf("unexpected payload: %+v", payload)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer server.Close()

	result, err := doTavilySearchCloudServiceCheck(t.Context(), "Tavily", server.URL, "test-key")
	if err != nil {
		t.Fatalf("doTavilySearchCloudServiceCheck error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected success, got %+v", result)
	}
}

func TestDoBingSearchCloudServiceCheckUsesOfficialRequestShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v7.0/search" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Ocp-Apim-Subscription-Key"); got != "test-key" {
			t.Fatalf("unexpected subscription key header: %q", got)
		}
		query := r.URL.Query()
		if query.Get("q") == "" || query.Get("count") != "1" || query.Get("responseFilter") != "Webpages" {
			t.Fatalf("unexpected query: %s", r.URL.RawQuery)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"webPages":{"value":[]}}`))
	}))
	defer server.Close()

	result, err := doBingSearchCloudServiceCheck(t.Context(), "Bing", server.URL, "test-key")
	if err != nil {
		t.Fatalf("doBingSearchCloudServiceCheck error: %v", err)
	}
	if result == nil || !result.Success {
		t.Fatalf("expected success, got %+v", result)
	}
}

func TestShouldVerifyCloudServiceOnSaveIncludesSearchProviders(t *testing.T) {
	if !shouldVerifyCloudServiceOnSave("search", "Bing") {
		t.Fatal("expected search providers to be verified on save")
	}
	if !shouldVerifyCloudServiceOnSave("ocr", "PaddleOCR") {
		t.Fatal("expected supported OCR providers to be verified on save")
	}
	if shouldVerifyCloudServiceOnSave("model", "Qwen") {
		t.Fatal("did not expect model providers to use cloud service save verification")
	}
}

func TestCloudServiceCheckRejectsAuthFailure(t *testing.T) {
	if cloudServiceCheckAccepted(http.StatusUnauthorized, []byte(`{"message":"invalid api key"}`)) {
		t.Fatal("expected unauthorized response to fail")
	}
	if cloudServiceCheckAccepted(http.StatusBadRequest, []byte(`{"message":"invalid api key"}`)) {
		t.Fatal("expected auth-looking bad request to fail")
	}
}
