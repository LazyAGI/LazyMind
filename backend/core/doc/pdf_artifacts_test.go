package doc

import (
	"context"
	"testing"

	"lazymind/core/common/orm"
)

func TestPDFCacheKeyIncludesSourceParseAndTranslationProvenance(t *testing.T) {
	state := orm.DocumentProcessingState{SourceFingerprint: "source-a", ParseFingerprint: "parse-a", ParserVersion: "mineru-1"}
	request := createPDFJobRequest{TargetLanguage: "zh", ProviderType: "api", Provider: "Tencent Translation", OptionsHash: "layout-v1"}
	base := pdfCacheKey(state, pdfArtifactTranslation, request)

	changedSource := state
	changedSource.SourceFingerprint = "source-b"
	if base == pdfCacheKey(changedSource, pdfArtifactTranslation, request) {
		t.Fatal("source changes must invalidate cached artifacts")
	}
	changedProvider := request
	changedProvider.ProviderType = "llm"
	changedProvider.Model = "document-context"
	if base == pdfCacheKey(state, pdfArtifactTranslation, changedProvider) {
		t.Fatal("translation provenance must be part of the cache key")
	}
}

func TestPDFTranslationJobIdempotencyKeyIsDocumentScoped(t *testing.T) {
	cacheKey := "same-content-cache-key"
	first := pdfTranslationJobIdempotencyKey("dataset-1", "document-1", cacheKey)
	if first != pdfTranslationJobIdempotencyKey("dataset-1", "document-1", cacheKey) {
		t.Fatal("translation job idempotency key must be stable")
	}
	if first == pdfTranslationJobIdempotencyKey("dataset-1", "document-2", cacheKey) {
		t.Fatal("translation jobs from different documents must not share an idempotency key")
	}
	if len(first) > 128 {
		t.Fatalf("translation job idempotency key length = %d, exceeds database limit", len(first))
	}
}

func TestFailedOrCancelledBackendTranslationJobCanRetry(t *testing.T) {
	for _, status := range []string{"FAILED", "CANCELLED"} {
		job := pdfRenderJobRecord{ID: "old-job", Status: status, BackendManaged: true}
		if reusableBackendTranslationJob(context.Background(), job) {
			t.Fatalf("%s translation job must not block a retry", status)
		}
	}
}

func TestLatestArtifactReturnsNewestMatchingKind(t *testing.T) {
	ext := documentExt{PDFArtifacts: []pdfArtifactRecord{
		{ID: "search-1", Kind: pdfArtifactSearchable},
		{ID: "translation-1", Kind: pdfArtifactTranslation},
		{ID: "search-2", Kind: pdfArtifactSearchable},
	}}
	artifact := latestArtifact(ext, pdfArtifactSearchable)
	if artifact == nil || artifact.ID != "search-2" {
		t.Fatalf("latest searchable artifact = %#v", artifact)
	}
}

func TestReplaceArtifactWithSameCacheKey(t *testing.T) {
	artifacts := []pdfArtifactRecord{
		{ID: "old", Kind: pdfArtifactSearchable, CacheKey: "same"},
		{ID: "translation", Kind: pdfArtifactTranslation, CacheKey: "same"},
		{ID: "other", Kind: pdfArtifactSearchable, CacheKey: "other"},
	}
	got, replaced := replaceArtifactWithSameCacheKey(artifacts, pdfArtifactRecord{ID: "new", Kind: pdfArtifactSearchable, CacheKey: "same"})
	if len(got) != 3 || got[2].ID != "new" || len(replaced) != 1 || replaced[0].ID != "old" {
		t.Fatalf("replacement result = %#v, replaced = %#v", got, replaced)
	}
}

func TestRemoveArtifactCacheEntryRemovesRegeneratedRevisions(t *testing.T) {
	artifacts := []pdfArtifactRecord{
		{ID: "revision-1", Kind: pdfArtifactTranslation, CacheKey: "same"},
		{ID: "revision-2", Kind: pdfArtifactTranslation, CacheKey: "same"},
		{ID: "another-config", Kind: pdfArtifactTranslation, CacheKey: "other"},
	}
	kept, removed := removeArtifactCacheEntry(artifacts, artifacts[1])
	if len(kept) != 1 || kept[0].ID != "another-config" || len(removed) != 2 {
		t.Fatalf("kept = %#v, removed = %#v", kept, removed)
	}
}
