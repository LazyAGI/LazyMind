package hosted

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/artifactfile"
	"lazymind/core/workflow/controlstore"
	"lazymind/core/workflow/executor"
	"lazymind/core/workflow/facade"
	"lazymind/core/workflow/graphengine"
)

func pptHostedFixture(t *testing.T, step string) (*Service, *gorm.DB) {
	t.Helper()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	service, db := prepareControlledService(t)
	root := filepath.Join("..", "..", "..", "..", "workflows", "ppt-workflow")
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, name))
		pptMust(t, err)
		return data
	}
	compiled := graphengine.Compile(string(read("workflow.yaml")), string(read("scenario/state.yml")), string(read("scenario/scenario.md")), graphengine.ProfilePublish)
	if !compiled.Valid || compiled.Graph == nil {
		t.Fatalf("PPT package failed compilation: %+v", compiled.Diagnostics)
	}
	graph := compiled.Graph
	node := graph.Nodes[step]
	if node.Execution == nil || node.Execution.OutputValidator != graphengine.PPTImagesOutputValidator {
		t.Fatal("the pinned PPT step has no image validator")
	}
	pptMust(t, db.AutoMigrate(&orm.WorkflowResource{}))
	pptMust(t, db.Create(&orm.WorkflowResource{ID: "resource-1", WorkflowRef: "builtin:ppt-workflow", WorkflowID: "ppt-workflow",
		OwnerScope: "builtin", SourceType: "builtin", Status: "active", HeadRevisionID: "revision-1", RelativeRoot: "ppt-workflow"}).Error)
	pptMust(t, db.Model(&orm.WorkflowRevision{}).Where("id = ?", "revision-1").Updates(map[string]any{"compiled_graph": graph.JSON(), "graph_hash": graph.GraphHash, "graph_schema_version": graph.SchemaVersion}).Error)
	pptMust(t, db.Model(&orm.WorkflowSession{}).Where("id = ?", "session-1").Updates(map[string]any{"plugin_id": "ppt-workflow", "graph_hash": graph.GraphHash, "graph_schema_version": graph.SchemaVersion}).Error)
	pptMust(t, db.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt-1").Updates(map[string]any{"step_id": step, "executor_host": HostName, "review_required": false}).Error)
	for _, name := range append([]string{"workflow.yaml"}, node.Execution.Skills...) {
		body := read(name)
		hash := controlstore.Hash(body)
		pptMust(t, db.Create(&orm.WorkflowBlob{Hash: hash, Size: int64(len(body)), Content: body}).Error)
		pptMust(t, db.Create(&orm.WorkflowRevisionEntry{RevisionID: "revision-1", Path: name, EntryType: "file", BlobHash: &hash}).Error)
	}
	return service, db
}

func pptMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func bindPPTFixture(t *testing.T, db *gorm.DB, id, slot string, index *int, value json.RawMessage) {
	t.Helper()
	pptMust(t, db.Create(&orm.WorkflowSlotRevision{ID: id, SessionID: "session-1", SlotID: slot, Slot: slot,
		Revision: 1, ListIndex: index, Selected: true, Validity: "effective", ContentSnapshot: value}).Error)
	pptMust(t, db.Create(&orm.WorkflowAttemptInputBinding{ID: "binding-" + id, SessionID: "session-1", AttemptID: "attempt-1",
		MaterialID: slot, MaterialRevisionID: id, SourceID: id, SourceType: "artifact"}).Error)
}

func pptPNG(t *testing.T, tint uint8) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 256, 128))
	canvas.Set(2, 2, color.RGBA{R: tint, A: 255})
	var out bytes.Buffer
	pptMust(t, png.Encode(&out, canvas))
	return out.Bytes()
}

func pptJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	pptMust(t, err)
	return raw
}

func TestExternalPPTImagePublicationRequiresDecodableBytes(t *testing.T) {
	s, db := pptHostedFixture(t, "collect_materials")
	grant, err := s.Begin(t.Context(), "owner", "session-1", "attempt-1")
	pptMust(t, err)
	publish := func(value json.RawMessage) error {
		return s.Publish(t.Context(), "owner", "session-1", "attempt-1", Publication{ExecutionHandle: grant.ExecutionHandle,
			Artifact: executor.Artifact{Slot: "material_images", Seq: 1, ContentType: "image", Value: value}})
	}
	imageValue := func(raw []byte) json.RawMessage {
		return pptJSON(t, map[string]any{"storage": "inline_base64", "name": "result.jpg", "mime_type": "image/jpeg",
			"content_base64": base64.StdEncoding.EncodeToString(raw)})
	}
	data := pptPNG(t, 1)
	for _, value := range []json.RawMessage{
		pptJSON(t, "https://example.org/photo.png"),
		imageValue([]byte("<html>access denied</html>")),
		imageValue(data[:50]),
	} {
		var invalid *controlstore.Error
		if err := publish(value); !errors.As(err, &invalid) || invalid.Code != "INVALID_ARTIFACT" {
			t.Fatalf("invalid image publication returned %v", err)
		}
	}
	pptMust(t, publish(imageValue(data)))
	pptMust(t, publish(imageValue(data)))

	var saved orm.WorkflowSlotRevision
	pptMust(t, db.Where("producer_attempt_id = ? AND slot_id = ?", "attempt-1", "material_images").Take(&saved).Error)
	read := mux.SetURLVars(httptest.NewRequest(http.MethodGet, "/workflow-artifacts/"+saved.ID, nil), map[string]string{"artifact_id": saved.ID})
	read.Header.Set("X-User-Id", "owner")
	rec := httptest.NewRecorder()
	(facade.Handler{Store: s.Store}).ReadArtifact(rec, read)
	var response struct {
		Result struct {
			Value struct {
				Storage string `json:"storage"`
				MIME    string `json:"mime_type"`
				Bytes   []byte `json:"content_base64"`
			} `json:"value"`
		} `json:"result"`
	}
	pptMust(t, json.Unmarshal(rec.Body.Bytes(), &response))
	value := response.Result.Value
	if rec.Code != 200 || value.Storage != "inline_base64" || value.MIME != "image/png" || !bytes.Equal(data, value.Bytes) {
		t.Fatalf("MCP artifact read did not return the saved PNG, HTTP %d MIME %q", rec.Code, value.MIME)
	}
}

func TestExternalPPTPreviewUsesPinnedForegroundAndPerPageBackground(t *testing.T) {
	s, db := pptHostedFixture(t, "generate_ppt")
	bindPPTFixture(t, db, "html-format", "presentation_output_format", nil, pptJSON(t, "html"))
	bindPPTFixture(t, db, "background-enabled", "generate_background_images", nil, pptJSON(t, "enabled"))
	// A user reordered the page prompts independently of their stable indices.
	first, second := 9, 3
	bindPPTFixture(t, db, "photo-page", "slide_outline", &first, pptJSON(t, "## Assets\nPalace panorama\n<!-- ppt-material-images: [\"palace-photo\"] -->"))
	bindPPTFixture(t, db, "text-page", "slide_outline", &second, pptJSON(t, "## Copy\nPalace history\n<!-- ppt-material-images: [] -->"))
	pptMust(t, db.Create(&orm.WorkflowSlotOrder{SessionID: "session-1", SlotID: "slide_outline", OrderList: pptJSON(t, []int{first, second})}).Error)

	images := map[string]string{}
	for _, item := range []struct {
		id, slot string
		index    int
		tint     uint8
	}{
		{"palace-photo", "material_images", 0, 120},
		{"first-background", "background_images", 0, 60},
		{"second-background", "background_images", 1, 180},
	} {
		data := pptPNG(t, item.tint)
		images[item.id] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
		// In production these snapshots are produced by the preceding step's
		// image publication, covered by TestExternalPPTImagePublicationRequiresDecodableBytes.
		bindPPTFixture(t, db, item.id, item.slot, &item.index, savePPTImageFixture(t, item.id, data))
	}
	grant, err := s.Begin(t.Context(), "owner", "session-1", "attempt-1")
	pptMust(t, err)
	publish := func(page int, body string) error {
		return s.Publish(t.Context(), "owner", "session-1", "attempt-1", Publication{ExecutionHandle: grant.ExecutionHandle,
			Artifact: executor.Artifact{Slot: "preview_html", Seq: page, ContentType: "text", Value: pptJSON(t, body)}})
	}
	complete := func() error {
		_, err := s.Complete(t.Context(), "owner", "session-1", "attempt-1", executor.Completion{ExecutionHandle: grant.ExecutionHandle, Outcome: "succeeded"})
		return err
	}
	page := func(body string) string {
		return "<!doctype html><html><head><title>PPT</title></head><body>" + body + "</body></html>"
	}
	for _, candidate := range []struct{ body, message string }{
		{`<div id="bg"></div><p>未提供全景照片的可用路径</p>`, "approved material image"},
		{`<div id="bg"><img data-ppt-image="palace-photo"></div>`, "bound foreground"},
		{`<div id="bg"></div><!-- <img data-ppt-image="palace-photo"> -->`, "approved material image"},
		{`<div id="bg"></div><img data-ppt-image="foreign-revision">`, "bound foreground"},
		{`<img data-ppt-image="palace-photo">`, "missing the #bg layer"},
	} {
		if err := publish(1, page(candidate.body)); err == nil || !strings.Contains(err.Error(), candidate.message) {
			t.Fatalf("invalid page was accepted or returned unexpected error: %v", err)
		}
	}
	firstHTML := page(`<div id="bg"></div><img data-ppt-image="palace-photo" alt="Palace panorama">`)
	pptMust(t, publish(1, firstHTML))
	pptMust(t, publish(1, firstHTML))
	if err := complete(); err == nil || !strings.Contains(err.Error(), "expected 2") {
		t.Fatalf("incomplete deck completed: %v", err)
	}
	secondHTML := page(`<div id="bg"></div><h1>Palace history</h1>`)
	pptMust(t, publish(2, secondHTML))
	var previews []orm.WorkflowSlotRevision
	pptMust(t, db.Where("producer_attempt_id = ? AND slot_id = ?", "attempt-1", "preview_html").Order("list_index").Find(&previews).Error)
	if len(previews) != 2 {
		t.Fatalf("got %d previews", len(previews))
	}
	for i, row := range previews {
		raw, err := controlstore.ResolveValue(db, row)
		pptMust(t, err)
		var value struct{ Text string }
		pptMust(t, json.Unmarshal(raw, &value))
		if i == 0 && !strings.Contains(value.Text, `src="`+images["palace-photo"]+`"`) {
			t.Fatal("first preview did not contain the real panorama")
		}
		pptMust(t, publish(i+1, value.Text))
		background := []string{"first-background", "second-background"}[i]
		if !strings.Contains(value.Text, images[background]) || (i == 1 && strings.Contains(value.Text, images["palace-photo"])) {
			t.Fatalf("preview %d has an incorrect image binding", i+1)
		}
	}
	pptMust(t, complete())
}

func TestExternalPPTPlanCannotSilentlyDropSelectedPhoto(t *testing.T) {
	s, db := pptHostedFixture(t, "plan_page_prompts")
	bindPPTFixture(t, db, "html-format", "presentation_output_format", nil, pptJSON(t, "html"))
	index := 0
	bindPPTFixture(t, db, "selected-photo", "material_images", &index, savePPTImageFixture(t, "selected-photo", pptPNG(t, 80)))
	grant, err := s.Begin(t.Context(), "owner", "session-1", "attempt-1")
	pptMust(t, err)
	publish := func(slot, typ string, seq int, value json.RawMessage) error {
		return s.Publish(t.Context(), "owner", "session-1", "attempt-1", Publication{ExecutionHandle: grant.ExecutionHandle,
			Artifact: executor.Artifact{Slot: slot, Seq: seq, ContentType: typ, Value: value}})
	}
	for _, brief := range []string{
		"Missing marker",
		"Wrong image\n<!-- ppt-material-images: [\"different-revision\"] -->",
	} {
		if err := publish("slide_outline", "text", 1, pptJSON(t, brief)); err == nil || !strings.Contains(err.Error(), "ppt-material-images") && !strings.Contains(err.Error(), "material_images revision") {
			t.Fatalf("invalid page plan was accepted or returned unexpected error: %v", err)
		}
	}
	pptMust(t, publish("slide_outline", "text", 1, pptJSON(t, "## Assets\n<!-- ppt-material-images: [] -->")))
	pptMust(t, publish("speaker_notes_source", "file", 1, json.RawMessage(`{"name":"speaker_notes.json"}`)))
	_, err = s.Complete(t.Context(), "owner", "session-1", "attempt-1", executor.Completion{ExecutionHandle: grant.ExecutionHandle, Outcome: "succeeded"})
	if err == nil || !strings.Contains(err.Error(), "selected material image selected-photo has no binding") {
		t.Fatalf("a plan omitting all approved photos passed completion: %v", err)
	}
}
func savePPTImageFixture(t *testing.T, id string, data []byte) json.RawMessage {
	t.Helper()
	raw := pptJSON(t, map[string]any{"storage": "inline_base64", "name": id + ".png", "mime_type": "image/png",
		"content_base64": base64.StdEncoding.EncodeToString(data)})
	value, _, err := artifactfile.Materialize("session-1", id, raw)
	pptMust(t, err)
	return value
}
