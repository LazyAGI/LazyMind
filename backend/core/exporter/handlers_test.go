package exporter

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/common"
)

func exporterRequest(method, path string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	return mux.SetURLVars(request, map[string]string{"provider_id": htmlPresentationProviderID})
}

func decodeCapabilities(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	var response common.APIResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data, ok := response.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected data: %#v", response.Data)
	}
	return data
}

func editableFormat(t *testing.T, data map[string]any) map[string]any {
	t.Helper()
	formats, ok := data["formats"].([]any)
	if !ok {
		t.Fatalf("formats = %#v", data["formats"])
	}
	for _, raw := range formats {
		format, _ := raw.(map[string]any)
		if format["id"] == "editable-pptx" {
			return format
		}
	}
	t.Fatal("editable-pptx capability missing")
	return nil
}

func TestHTMLPresentationCapabilitiesComeFromChat(t *testing.T) {
	chat := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/workflow/ppt/capabilities" {
			http.Error(w, "unexpected upstream path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"editable_pptx":true}`))
	}))
	defer chat.Close()

	t.Setenv("LAZYMIND_RUNTIME_MODE", "container")
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", chat.URL)
	recorder := httptest.NewRecorder()
	Capabilities(recorder, exporterRequest(http.MethodGet, "/exporters/html-presentation:capabilities"))

	data := decodeCapabilities(t, recorder)
	if data["provider_id"] != htmlPresentationProviderID {
		t.Fatalf("provider_id = %#v", data["provider_id"])
	}
	if available, _ := editableFormat(t, data)["available"].(bool); !available {
		t.Fatal("editable-pptx should be available")
	}
}

func TestHTMLPresentationCapabilitiesReportDependency(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	t.Setenv("LAZYMIND_RUNTIME_ROOT", t.TempDir())
	recorder := httptest.NewRecorder()
	Capabilities(recorder, exporterRequest(http.MethodGet, "/exporters/html-presentation:capabilities"))

	format := editableFormat(t, decodeCapabilities(t, recorder))
	if available, _ := format["available"].(bool); available {
		t.Fatal("editable-pptx should be unavailable")
	}
	dependency, _ := format["dependency"].(map[string]any)
	if dependency["id"] != "editable-ppt-dependency" {
		t.Fatalf("dependency = %#v", dependency)
	}
}
