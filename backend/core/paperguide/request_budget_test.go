package paperguide

import (
	"context"
	"encoding/json"
	"errors"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
	"os"
	"strings"
	"testing"
)

func TestRequestBudgetSharedWithActualPythonTemplates(t *testing.T) {
	raw, err := os.ReadFile("testdata/request-budget.json")
	if err != nil {
		t.Fatal(err)
	}
	var limits struct {
		TaskVersion string `json:"task_version"`
		Prompt      int    `json:"prompt_reserve_bytes"`
		Defaults    int    `json:"input_defaults_reserve_bytes"`
		Evidence    int    `json:"evidence_expansion_reserve_bytes"`
	}
	if err = json.Unmarshal(raw, &limits); err != nil {
		t.Fatal(err)
	}
	if limits.TaskVersion != TaskVersion || limits.Prompt != guidePromptReserve || limits.Defaults != guideInputDefaultsReserve || limits.Evidence != guideEvidenceExpansionReserve {
		t.Fatalf("budget fixture must match the verified Go admission constants: %+v", limits)
	}
}

func TestOutputReserveStillLeavesPromptAndSchemaSpace(t *testing.T) {
	x := execution{model: ModelIdentity{ContextTokens: 40000, OutputReserveTokens: 16000}}
	for _, size := range []int{23900, 23000, 16000} {
		if x.inputFits(map[string]any{"text": strings.Repeat("x", size)}) {
			t.Fatalf("accepted %d-byte input without template space", size)
		}
	}
	if !x.inputFits(map[string]any{"text": strings.Repeat("x", 14000)}) {
		t.Fatal("safe remaining space rejected")
	}
}

func TestTaskOnlyCallsModelAfterReservingWholePrompt(t *testing.T) {
	d, id := fixture(t)
	d.Blocks = []pdfir.Block{{ID: "large", Type: "text", Page: 0, Text: strings.Repeat("x", 22500)}, {ID: "small", Type: "text", Page: 0, Text: "Original short material."}}
	snap, err := BuildSourceSnapshot(d, id)
	if err != nil {
		t.Fatal(err)
	}
	repo := Repository{DB: guideDB(t)}
	scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "en"}
	model := ModelIdentity{ID: "m", ContextTokens: 40000, OutputReserveTokens: 16000}
	rev, err := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "test", Model: model})
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	service := Service{Repo: repo, Source: &fixtureSource{s: snap}, Authorize: func(context.Context, Scope, bool) error { return nil }, ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil }, Call: func(context.Context, string, map[string]any, map[string]any) (json.RawMessage, error) {
		calls++
		return mustJSON(GuideExtract{Claims: []GuideClaim{}, OpenQuestions: []string{}}), nil
	}}
	x := execution{s: service, scope: scope, rev: rev, model: model, snap: snap, f: testFence{repo.DB}, steps: map[string]orm.PaperGuideStep{}}
	var out GuideExtract
	if err = x.task(t.Context(), "too-large", "paper.guide.extract", x.input(snap), &out, func() error { return ValidateExtract(snap, out) }); !errors.Is(err, ErrContext) || calls != 0 {
		t.Fatalf("unsafe prompt called model: calls=%d err=%v", calls, err)
	}
	small, err := RestrictEvidence(snap, []string{snap.Evidence[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	if err = x.task(t.Context(), "small", "paper.guide.extract", x.input(small), &out, func() error { return ValidateExtract(small, out) }); err != nil || calls != 1 {
		t.Fatalf("safe prompt did not call once: calls=%d err=%v", calls, err)
	}
}
