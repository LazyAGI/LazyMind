package coreclient

import (
	"context"
	"net/http"
	"testing"
)

func TestKnowledgeCreationCarriesIdentityWithoutSourceClassification(t *testing.T) {
	client := newHTTPTestCoreClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-LazyMind-Internal-Token") != "fixture-internal-token" || r.Header.Get("X-User-Id") != "collector-owner" {
			t.Error("source collection must retain its authenticated Core caller")
		}
		if origin := r.Header.Get("X-LazyMind-Source-Origin"); origin != "" {
			t.Errorf("Core must not need a Scan classification for knowledge authorization: %q", origin)
		}
		writeJSON(t, w, http.StatusOK, CreateDatasetResponse{DatasetID: "knowledge", Created: true})
	}))
	client.SetInternalToken("fixture-internal-token")
	if _, err := client.CreateDataset(context.Background(), CreateDatasetRequest{
		IdempotencyKey: "create-source:owner:request:dataset", Name: "Collected", CreatedBy: "collector-owner",
	}); err != nil {
		t.Fatal(err)
	}
}
