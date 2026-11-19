package executor

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

func TestPaperContextPinsOwnedInputBytesAndLease(t *testing.T) {
	db := executorComponentDB(t, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowInputResource{})
	content := []byte("# Actual research notes")
	hash := fmt.Sprintf("sha256:%x", sha256.Sum256(content))
	for _, row := range []any{
		&orm.WorkflowSession{ID: "paper-run", CreateUserID: "owner"},
		&orm.WorkflowSessionStep{ID: "paper-attempt", SessionID: "paper-run", LeaseToken: "private-lease"},
		&orm.WorkflowInputResource{ID: "notes", OwnerUserID: "owner", Name: "notes.md", Revision: 2, Content: content, ContentHash: hash},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	attempt := AttemptContext{SessionID: "paper-run", AttemptID: "paper-attempt", StepID: "literature_review", WorkflowRevision: "pinned",
		Execution: &ExecutionContract{StepExecution: graphengine.StepExecution{OutputValidator: graphengine.PaperAgentOutputValidator}},
		Inputs:    map[string]any{"research_materials": map[string]any{"source_type": "input_resource", "source_id": "notes", "content_hash": hash, "source_revision": "2"}}}
	scope, err := PaperToolContext(t.Context(), db, attempt)
	if err != nil {
		t.Fatal(err)
	}
	values := scope["values"].(map[string]any)
	notes := values["research_materials"].(map[string]any)
	raw, err := base64.StdEncoding.DecodeString(notes["__file_base64"].(string))
	if err != nil || string(raw) != string(content) {
		t.Fatal("did not resolve real bound bytes")
	}
	if scope["lease_hash"] != fmt.Sprintf("%x", sha256.Sum256([]byte("private-lease"))) {
		t.Fatal("missing reclaim fence")
	}
	for _, mutation := range []string{"owner", "bytes", "version"} {
		t.Run(mutation, func(t *testing.T) {
			changes := map[string]any{"owner_user_id": "owner", "content": content, "revision": 2}
			switch mutation {
			case "owner":
				changes["owner_user_id"] = "other"
			case "bytes":
				changes["content"] = []byte("replaced")
			case "version":
				changes["revision"] = 3
			}
			if err := db.Model(&orm.WorkflowInputResource{}).Where("id = ?", "notes").Updates(changes).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := PaperToolContext(t.Context(), db, attempt); err == nil {
				t.Fatal("changed or foreign resource accepted")
			}
		})
	}
}

func TestPaperCompletionSendsExactFilesAndUsesArtifactSequence(t *testing.T) {
	db := executorComponentDB(t, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowHumanArtifact{}, &orm.WorkflowSlotRevision{})
	for _, row := range []any{&orm.WorkflowSession{ID: "run", CreateUserID: "owner"}, &orm.WorkflowSessionStep{ID: "attempt", SessionID: "run", LeaseToken: "lease"}} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	human := "file"
	value := json.RawMessage(`{"storage":"inline_base64","name":"chart.svg","content_base64":"PHN2Zy8+"}`)
	if err := db.Create(&orm.WorkflowHumanArtifact{ID: human, SessionID: "run", Slot: "analysis_chart", ContentType: "file", Value: value}).Error; err != nil {
		t.Fatal(err)
	}
	sequence, index := 2, 8
	rows := []orm.WorkflowSlotRevision{{ID: "rev", SessionID: "run", SlotID: "analysis_chart", HumanArtifactID: &human, ArtifactSeq: &sequence, ListIndex: &index}}
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "paper-verification-test")
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/workflow/paper/verify" || r.Header.Get("X-LazyMind-Internal-Token") != "paper-verification-test" {
			t.Error("wrong verification boundary")
		}
		var request struct {
			Context   map[string]any `json:"context"`
			Artifacts []struct {
				Slot  string         `json:"slot"`
				Seq   int            `json:"seq"`
				Value map[string]any `json:"value"`
			} `json:"artifacts"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if len(request.Artifacts) != 1 || request.Artifacts[0].Seq != 2 || request.Artifacts[0].Value["content_base64"] != "PHN2Zy8+" {
			t.Error("verification lost exact bytes or artifact sequence")
		}
		if calls == 1 {
			w.Write([]byte(`{"valid":true}`))
		} else {
			w.Write([]byte(`{"valid":false}`))
		}
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	attempt := AttemptContext{SessionID: "run", AttemptID: "attempt", StepID: "data_analysis", WorkflowRevision: "pinned", Execution: &ExecutionContract{StepExecution: graphengine.StepExecution{OutputValidator: graphengine.PaperAgentOutputValidator}}}
	if err := validatePaperCompletion(t.Context(), db, attempt, rows); err != nil {
		t.Fatal(err)
	}
	if err := validatePaperCompletion(t.Context(), db, attempt, rows); err == nil {
		t.Fatal("rejected candidate accepted")
	}
}
