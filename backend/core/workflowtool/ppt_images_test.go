package workflowtool

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/artifactfile"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/graphengine"
)

func pptTestImage(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 128, 64))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestPPTDownloadTriesUsableCandidateAndPreservesBytes(t *testing.T) {
	data := pptTestImage(t)
	urls := []string{"https://example.org/expired", "https://example.org/login", "https://example.org/photo?signature=a%2Bb"}
	var tried []string
	download := func(_ context.Context, source string, limit int64) ([]byte, error) {
		tried = append(tried, source)
		if limit != 12<<20 {
			t.Fatalf("unexpected download limit %d", limit)
		}
		switch source {
		case urls[0]:
			return nil, errors.New("image download returned HTTP 403")
		case urls[1]:
			return []byte("<html>log in</html>"), nil
		default:
			return data, nil
		}
	}
	result, err := callPPTImageDownload(t.Context(), map[string]any{"urls": urls}, download)
	if err != nil || result.Error != nil || !reflect.DeepEqual(tried, urls) {
		t.Fatalf("download tried %v, got %+v %v", tried, result, err)
	}
	value := result.Result.(map[string]any)
	files := value["files"].([]map[string]any)
	if value["status"] != "ready" || value["source_url"] != urls[2] || len(files) != 1 ||
		files[0]["mime_type"] != "image/png" || files[0]["content_base64"] != base64.StdEncoding.EncodeToString(data) ||
		files[0]["sha256"] != controlstore.Hash(data) || len(value["failures"].([]map[string]any)) != 2 {
		t.Fatal("download did not return the validated fallback image and both errors")
	}
	result, err = callPPTImageDownload(t.Context(), map[string]any{"urls": urls[:2]}, download)
	if err != nil || result.Result.(map[string]any)["status"] != "unavailable" {
		t.Fatalf("failed sources were reported as ready: %+v %v", result, err)
	}
}

func TestPPTPreparePageUsesPinnedBriefAndReturnsOfflineImage(t *testing.T) {
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	imageData := pptTestImage(t)
	imageBase64 := base64.StdEncoding.EncodeToString(imageData)
	const brief = "## Assets\nPalace panorama\n<!-- ppt-material-images: [\"photo\"] -->"
	const draftHTML = `<!doctype html><html><head><title>PPT</title></head><body><img data-ppt-image="photo"></body></html>`
	var sawBrief bool
	service, input := workflowToolFixture(t, func(request *http.Request) (*http.Response, error) {
		var payload struct {
			Arguments struct {
				Brief     string            `json:"brief"`
				Responses []json.RawMessage `json:"generation_responses"`
			} `json:"arguments"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		query := payload.Arguments.Brief
		if !strings.HasPrefix(query, brief) || !strings.Contains(query, `data-ppt-image="photo"`) ||
			strings.Contains(query, "caller-provided") || strings.Contains(query, imageBase64) {
			t.Fatal("tool did not use the pinned brief with only lightweight image IDs")
		}
		sawBrief = true
		if len(payload.Arguments.Responses) == 0 {
			return toolResponse(`{"result":{"status":"generation_required","generation_request":{"request_hash":"replay"}}}`), nil
		}
		return toolResponse(`{"result":{"status":"ready","files":[{"name":"page_001.html","content_base64":"` +
			base64.StdEncoding.EncodeToString([]byte(draftHTML)) + `"}]}}`), nil
	})
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	db := service.DB
	var revision orm.WorkflowRevision
	must(db.Take(&revision, "id = ?", "pinned-revision").Error)
	var graph graphengine.CompiledStateGraph
	must(json.Unmarshal(revision.CompiledGraph, &graph))
	node := graph.Nodes["write"]
	node.Execution.OutputValidator = graphengine.PPTImagesOutputValidator
	node.Execution.Tools = []graphengine.ExecutionTool{{Name: preparePPTPage, Source: "package"}}
	graph.Nodes["write"] = node
	must(db.Model(&revision).Update("compiled_graph", graph.JSON()).Error)
	value, _, err := artifactfile.Materialize("run", "photo", json.RawMessage(`{"storage":"inline_base64","name":"photo.png","mime_type":"image/png","content_base64":"`+imageBase64+`"}`))
	must(err)
	must(db.Create(&orm.WorkflowSlotRevision{ID: "photo", SessionID: "run", SlotID: "material_images", Slot: "material_images", ContentSnapshot: value}).Error)
	inputs, err := json.Marshal(map[string]any{
		"metadata": map[string]string{"workflow_control_inputs_frozen": "true"},
		"inputs": map[string]any{
			"slide_outline":   []map[string]any{{"source_revision_id": "brief", "sort_order": 1, "value": brief}},
			"material_images": []map[string]any{{"source_revision_id": "photo"}},
		},
	})
	must(err)
	must(db.Model(&orm.WorkflowOutbox{}).Where("attempt_id = ?", "attempt").Update("payload_json", json.RawMessage(inputs)).Error)
	input.ToolName = preparePPTPage
	input.Arguments = map[string]any{"page_no": 1, "brief": "caller-provided"}
	result, err := service.Call(t.Context(), "owner", "run", "attempt", input)
	must(err)
	if !sawBrief || result.Error != nil || result.Result.(map[string]any)["status"] != "generation_required" {
		t.Fatalf("page tool did not request model output: %+v", result.Error)
	}
	input.CommandID = "accept-model-output"
	input.Arguments["generation_responses"] = []map[string]string{{"request_hash": "replay", "text": draftHTML}}
	result, err = service.Call(t.Context(), "owner", "run", "attempt", input)
	must(err)
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	file := result.Result.(map[string]any)["files"].([]any)[0].(map[string]any)
	raw, err := base64.StdEncoding.DecodeString(file["content_base64"].(string))
	must(err)
	if !strings.Contains(string(raw), `src="data:image/png;base64,`+imageBase64+`"`) ||
		file["sha256"] != controlstore.Hash(raw) || file["size_bytes"] != len(raw) {
		t.Fatal("page tool did not return verified offline HTML with the actual photo")
	}
}
