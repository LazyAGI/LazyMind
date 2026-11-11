package coreclient

import (
	"context"
	"net/http"
	"testing"
)

func TestHTTPCoreClientCreateDatasetCarriesTrustedScanOrigin(t *testing.T) {
	t.Parallel()

	const internalToken = "fixture-scan-internal-token"
	client := newHTTPTestCoreClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/datasets" {
			t.Fatalf("unexpected dataset request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("X-LazyMind-Internal-Token") != internalToken {
			t.Error("Scan dataset creation must carry the configured internal token")
		}
		if got := r.Header.Get("X-LazyMind-Source-Origin"); got != "scan" {
			t.Errorf("Scan dataset creation origin = %q, want scan", got)
		}
		if r.Header.Get("X-User-Id") != "user-1" || r.Header.Get("X-User-Name") != "User One" {
			t.Error("trusted source origin must preserve the caller identity headers")
		}
		writeJSON(t, w, http.StatusOK, CreateDatasetResponse{DatasetID: "dataset-1", Created: true})
	}))
	client.SetInternalToken(internalToken)

	_, err := client.CreateDataset(context.Background(), CreateDatasetRequest{
		IdempotencyKey: "create-source:user-1:req-1:dataset",
		Name:           "Docs",
		CreatedBy:      "user-1",
		UserName:       "User One",
		TenantID:       "tenant-1",
		Tags:           []string{"scan"},
	})
	if err != nil {
		t.Fatalf("create dataset: %v", err)
	}
}

func TestHTTPCoreClientOtherResourceRequestsDoNotCarrySourceOrigin(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		method string
		path   string
		call   func(*HTTPCoreClient) error
	}{
		{"binding root", http.MethodPost, "/datasets/dataset-1/documents", func(client *HTTPCoreClient) error {
			_, err := client.CreateBindingRootDocument(context.Background(), CreateBindingRootDocumentRequest{
				IdempotencyKey: "binding-root-1", DatasetID: "dataset-1", Name: "Binding", UserID: "user-1",
			})
			return err
		}},
		{"update dataset", http.MethodPatch, "/datasets/dataset-1", func(client *HTTPCoreClient) error {
			return client.UpdateDataset(context.Background(), UpdateDatasetRequest{DatasetID: "dataset-1", DisplayName: "Renamed", UserID: "user-1"})
		}},
		{"delete dataset", http.MethodDelete, "/datasets/dataset-1", func(client *HTTPCoreClient) error {
			return client.DeleteDataset(context.Background(), DeleteDatasetRequest{DatasetID: "dataset-1", UserID: "user-1"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			client := newHTTPTestCoreClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Fatalf("unexpected resource request: %s %s", r.Method, r.URL.Path)
				}
				if got := r.Header.Get("X-LazyMind-Source-Origin"); got != "" {
					t.Errorf("ordinary resource request inherited source origin %q", got)
				}
				writeJSON(t, w, http.StatusOK, map[string]any{"document_id": "folder-1"})
			}))
			client.SetInternalToken("fixture-scan-internal-token")
			if err := tc.call(client); err != nil {
				t.Fatalf("resource request: %v", err)
			}
			if requests != 1 {
				t.Fatalf("resource request count = %d, want 1", requests)
			}
		})
	}
}
