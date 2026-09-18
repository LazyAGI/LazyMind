package feishu

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFeishuCLIClientMapsDriveListAndUsesUserIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/internal/provider-connections/feishu-cli:execute" || request.Header.Get("X-LazyMind-Internal-Token") != "fixture-internal-token" {
			t.Fatal("unexpected Feishu CLI Core request")
		}
		var body struct {
			Handle    string            `json:"handle"`
			Operation string            `json:"operation"`
			Identity  string            `json:"identity"`
			Params    map[string]string `json:"params"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Handle != "lmc_fcli_fixture" || body.Operation != "drive_list" || body.Identity != "user" || body.Params["folder_token"] != "folder-a" {
			t.Fatalf("unexpected request: %#v", body)
		}
		writeFeishuJSON(t, w, http.StatusOK, map[string]any{
			"data": map[string]any{
				"files":    []map[string]any{{"token": "file-a", "name": "Fixture.pdf", "type": "file"}},
				"has_more": false,
			},
		})
	}))
	defer server.Close()
	client, err := NewFeishuCLIClient(server.URL, "fixture-internal-token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	page, err := client.ListDriveChildren(t.Context(), "lmc_fcli_fixture", "folder-a", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Token != "file-a" || page.Items[0].Name != "Fixture.pdf" {
		t.Fatalf("drive page = %#v", page)
	}
}

func TestFeishuCLIClientDecodesControlledDownload(t *testing.T) {
	want := []byte("fixture download")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		writeFeishuJSON(t, w, http.StatusOK, map[string]any{"content_base64": base64.StdEncoding.EncodeToString(want)})
	}))
	defer server.Close()
	client, err := NewFeishuCLIClient(server.URL, "fixture-internal-token", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	downloaded, err := client.DownloadDriveFile(t.Context(), "lmc_fcli_fixture", "file-a", "revision-a")
	if err != nil {
		t.Fatal(err)
	}
	if string(downloaded.Content) != string(want) || downloaded.ExportedVersion != "revision-a" {
		t.Fatalf("download = %#v", downloaded)
	}
}
