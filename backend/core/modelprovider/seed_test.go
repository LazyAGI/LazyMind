package modelprovider

import (
	"strings"
	"testing"
)

// --- normalizeBaseURL ---

// TestNormalizeBaseURL_AddsTrailingSlash appends slash to plain domain.
func TestNormalizeBaseURL_AddsTrailingSlash(t *testing.T) {
	got := normalizeBaseURL("https://api.openai.com")
	want := "https://api.openai.com/"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestNormalizeBaseURL_HasTrailingSlash keeps it as-is.
func TestNormalizeBaseURL_HasTrailingSlash(t *testing.T) {
	got := normalizeBaseURL("https://api.openai.com/")
	want := "https://api.openai.com/"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestNormalizeBaseURL_EndpointPath skips adding slash for endpoint URLs.
func TestNormalizeBaseURL_EndpointPath(t *testing.T) {
	// "/embeddings", "/rerank", "/embed" in the URL → no trailing slash added.
	got := normalizeBaseURL("https://api.openai.com/v1/embeddings")
	want := "https://api.openai.com/v1/embeddings"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestNormalizeBaseURL_RerankEndpoint skips trailing slash for /rerank paths.
func TestNormalizeBaseURL_RerankEndpoint(t *testing.T) {
	got := normalizeBaseURL("https://api.cohere.com/v1/rerank")
	if !strings.Contains(got, "rerank") || strings.HasSuffix(got, "/") {
		t.Fatalf("got %q, expected no trailing slash on rerank endpoint", got)
	}
}

// TestNormalizeBaseURL_EmbedEndpoint skips trailing slash for /embed paths.
func TestNormalizeBaseURL_EmbedEndpoint(t *testing.T) {
	got := normalizeBaseURL("https://api.example.com/v1/embed")
	if !strings.Contains(got, "embed") || strings.HasSuffix(got, "/") {
		t.Fatalf("got %q, expected no trailing slash on embed endpoint", got)
	}
}

// TestNormalizeBaseURL_Empty returns empty.
func TestNormalizeBaseURL_Empty(t *testing.T) {
	got := normalizeBaseURL("")
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// TestNormalizeBaseURL_WhitespaceOnly returns empty.
func TestNormalizeBaseURL_WhitespaceOnly(t *testing.T) {
	got := normalizeBaseURL("   ")
	if got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// TestNormalizeBaseURL_SubstringMatchWithEmbed blocks trailing slash because "/embed" is a marker.
func TestNormalizeBaseURL_SubstringMatchWithEmbed(t *testing.T) {
	// "/embed" in the URL is detected as an endpoint marker → no trailing slash added.
	got := normalizeBaseURL("https://api.example.com/embedding-service")
	want := "https://api.example.com/embedding-service"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	// A URL without endpoint markers gets a trailing slash.
	got2 := normalizeBaseURL("https://api.example.com/some-path")
	want2 := "https://api.example.com/some-path/"
	if got2 != want2 {
		t.Fatalf("got %q, want %q", got2, want2)
	}
}

// --- loadModelCatalog ---

// TestLoadModelCatalog_EmptyYAML returns nil catalog when YAML is empty (no sections).
func TestLoadModelCatalog_EmptyYAML(t *testing.T) {
	catalog, err := loadModelCatalog([]byte(``))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if catalog != nil {
		t.Fatalf("expected nil catalog for empty YAML, got %v", catalog)
	}
}

// TestLoadModelCatalog_InvalidYAML returns error.
func TestLoadModelCatalog_InvalidYAML(t *testing.T) {
	_, err := loadModelCatalog([]byte(`: bad yaml`))
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

// TestLoadModelCatalog_MinimalProvider parses a simple model_providers section.
func TestLoadModelCatalog_MinimalProvider(t *testing.T) {
	yaml := []byte(`
model_providers:
  capabilities: [chat]
  suppliers:
    - name: OpenAI
      base_url: https://api.openai.com/v1
      models:
        - name: gpt-4
          type: llm
`)
	catalog, err := loadModelCatalog(yaml)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	section, ok := catalog["model_providers"]
	if !ok {
		t.Fatal("expected model_providers section")
	}
	if len(section.Suppliers) != 1 || section.Suppliers[0].Name != "OpenAI" {
		t.Fatalf("unexpected suppliers: %+v", section.Suppliers)
	}
	if len(section.Suppliers[0].Models) != 1 || section.Suppliers[0].Models[0].Name != "gpt-4" {
		t.Fatalf("unexpected models: %+v", section.Suppliers[0].Models)
	}
}
