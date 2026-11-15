package paperguide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestRequestBudgetSharedWithActualPythonTemplates(t *testing.T) {
	raw, err := os.ReadFile("testdata/request-budget.json")
	if err != nil {
		t.Fatal(err)
	}
	var limits struct {
		TaskVersion string         `json:"task_version"`
		Frame       int            `json:"provider_frame_reserve_bytes"`
		System      int            `json:"provider_system_bytes"`
		JSONMode    int            `json:"provider_json_mode_text_bytes"`
		Legacy      map[string]int `json:"measured_prompt_bytes"`
		Native      map[string]int `json:"measured_native_prompt_bytes"`
		NativeText  map[string]int `json:"measured_native_text_bytes"`
		Prompt      int            `json:"prompt_reserve_bytes"`
		Defaults    int            `json:"input_defaults_reserve_bytes"`
		Evidence    int            `json:"evidence_expansion_reserve_bytes"`
	}
	if err = json.Unmarshal(raw, &limits); err != nil {
		t.Fatal(err)
	}
	if limits.TaskVersion != TaskVersion || limits.Prompt != guidePromptReserve || limits.Defaults != guideInputDefaultsReserve || limits.Evidence != guideEvidenceExpansionReserve {
		t.Fatalf("budget fixture must match the verified Go admission constants: %+v", limits)
	}
	for _, task := range []string{"paper.guide.extract", "paper.guide.plan", "paper.guide.section", "paper.guide.review"} {
		legacy, native, text := limits.Legacy[task], limits.Native[task], limits.NativeText[task]
		if legacy <= 0 || native <= 0 || text <= 0 || limits.System <= 0 || limits.Frame <= 0 || limits.JSONMode <= 0 {
			t.Fatalf("missing measured provider input sizes for %s", task)
		}
		overhead := limits.System + limits.Frame
		legacyTotal, nativeTotal := legacy+limits.JSONMode+overhead, native+text+overhead
		if legacyTotal > guidePromptReserve || nativeTotal > guidePromptReserve {
			t.Fatalf("%s exceeds the existing fixed prompt reserve: inline=%d native=%d", task, legacyTotal, nativeTotal)
		}
	}
}

func TestOutputReserveStillLeavesPromptAndSchemaSpace(t *testing.T) {
	x := execution{model: ModelIdentity{ContextTokens: 40000, OutputReserveTokens: 16000}}
	for _, size := range []int{23900, 23000, 16000} {
		if x.inputFits("paper.guide.extract", map[string]any{"text": strings.Repeat("x", size)}) {
			t.Fatalf("accepted %d-byte input without template space", size)
		}
	}
	if !x.inputFits("paper.guide.extract", map[string]any{"text": strings.Repeat("x", 13000)}) {
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
	if err = x.task(t.Context(), "too-large", "paper.guide.extract", x.input(snap), &out, func() error { return ValidateExtract(snap, out) }); !errors.Is(err, ErrContext) || calls != 0 || len(sourceContextSteps(t, x)) != 0 {
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

func TestPlanRepeatCreditAdmitsCompleteCatalogAtExactBoundary(t *testing.T) {
	doc, id := fixture(t)
	doc.Blocks = []pdfir.Block{{ID: "text", Type: "text", Page: 0, Text: "Original measurement."}}
	snap, err := BuildSourceSnapshot(doc, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Evidence) != 1 || len(snap.Sections) != 1 || len(snap.Sections[0].EvidenceIDs) != 1 {
		t.Fatal("want one evidence ID and one typed repeat in the source directory")
	}
	intent := execution{scope: Scope{Language: "en"}, snap: snap}
	input := intent.planningInput(snap)
	raw, err := json.MarshalIndent(input, "", "")
	if err != nil {
		t.Fatal(err)
	}
	// The repeat becomes e1, saving 65 bytes. The first occurrence remains charged.
	atLimit := len(raw) + 256 + 64 + 10240 - 65
	for _, delta := range []int{-1, 0, 1} {
		x := newSectionSourceExecution(t, snap, atLimit+delta)
		hash := digest([]any{TaskVersion, input})
		x.steps["plan"] = orm.PaperGuideStep{InputHash: hash, OutputJSON: string(mustJSON(baselinePlan(snap)))}
		// At -1 the initial PLAN cannot be admitted. Missing extract history means
		// the runner stops without writing a request marker or doing any call.
		got, packet, planErr := x.planInput(t.Context())
		var out GuidePlan
		taskErr := x.task(t.Context(), "plan", "paper.guide.plan", input, &out, func() error { return ValidatePlan(snap, out) })
		if delta < 0 {
			if !errors.Is(planErr, ErrEvidence) || !errors.Is(taskErr, ErrContext) {
				t.Fatalf("one byte over: plan=%v task=%v", planErr, taskErr)
			}
		} else if planErr != nil || taskErr != nil || !reflect.DeepEqual(packet.Evidence, snap.Evidence) ||
			digest([]any{TaskVersion, got}) != hash {
			t.Fatalf("delta=%d: complete PLAN packet or checkpoint rejected: plan=%v task=%v", delta, planErr, taskErr)
		}
		if len(sourceContextSteps(t, x)) != 0 || digest([]any{TaskVersion, input}) != hash {
			t.Fatal("candidate admission changed the input or reserved a ticket")
		}
	}
}

func TestGuideEvidenceCapAppliesToGenericJSON(t *testing.T) {
	x := execution{model: ModelIdentity{ContextTokens: 10_000_000}}
	evidence := make([]any, 2049)
	for i := range evidence {
		evidence[i] = map[string]any{"id": fmt.Sprintf("ev_%064x", i)}
	}
	if x.inputFits("paper.guide.extract", map[string]any{"evidence": evidence}) {
		t.Fatal("2049 evidence entries admitted through a generic JSON array")
	}
	if !x.inputFits("paper.guide.extract", map[string]any{"evidence": evidence[:2048]}) {
		t.Fatal("2048 generic JSON entries exceed the evidence cap")
	}
}
