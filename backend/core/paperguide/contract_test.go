package paperguide

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
	"os"
	"testing"
)

type historicalSource struct {
	fixtureSource
	legacy GuideSourceSnapshot
}

func (s *historicalSource) Read(context.Context, string) (GuideSourceSnapshot, error) {
	return s.legacy, nil
}

func TestLegacyPublishedGuideAndSelectionRemainReadable(t *testing.T) {
	raw, err := os.ReadFile("testdata/legacy-snapshot-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var old GuideSourceSnapshot
	if err = json.Unmarshal(raw, &old); err != nil {
		t.Fatal(err)
	}
	d, id := fixture(t)
	current, _ := BuildSourceSnapshot(d, id)
	repo := Repository{DB: guideDB(t)}
	scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "zh"}
	model := ModelIdentity{ID: "m"}
	rev, err := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: old.IRDigest, SnapshotPath: "legacy", Model: model})
	if err != nil {
		t.Fatal(err)
	}
	section := teachingSection(RoleBackground)
	section.Version = 1
	section.PublicationStatus = "published"
	section.Blocks[0].EvidenceIDs = []string{old.Evidence[1].ID}
	for _, step := range []StepWrite{{Key: "plan", InputHash: "old-plan", Output: mustJSON(baselinePlan(old))}, {Key: "section:background:v1", InputHash: "old-section", Output: mustJSON(section), Published: true}} {
		if err = repo.PutStep(t.Context(), testFence{repo.DB}, rev.ID, rev.Epoch, step); err != nil {
			t.Fatal(err)
		}
	}
	if err = repo.Transition(t.Context(), testFence{repo.DB}, rev.ID, rev.Epoch, "completed", ""); err != nil {
		t.Fatal(err)
	}
	repo.DB.Model(&rev).Update("fingerprint", digest([]any{id.SourceSHA256, old.IRDigest, "paper-guide-v1", model}))
	service := Service{Repo: repo, Source: &historicalSource{fixtureSource{current, nil}, old}, Authorize: func(context.Context, Scope, bool) error { return nil }}
	got, err := service.Revision(t.Context(), scope, rev.ID, 0)
	if err != nil || !got.SourceCurrent || len(got.Sections) != 1 {
		t.Fatalf("legacy history: %+v %v", got, err)
	}
	selected, err := service.Selection(t.Context(), scope, rev.ID, SelectionRequest{SectionKey: "section:background:v1", BlockID: "b_background", Start: 0, End: 3})
	if err != nil || selected.Text != "The" || len(selected.Evidence) != 1 || selected.Evidence[0].ID != old.Evidence[1].ID {
		t.Fatalf("legacy selection: %+v %v", selected, err)
	}
}

func TestPristineWaitingSourceAdoptsCurrentContract(t *testing.T) {
	d, id := fixture(t)
	d.Blocks = append(d.Blocks, pdfir.Block{ID: "body", Type: "text", Page: 1, Text: "The original study reports additional sample conditions."})
	snap, _ := BuildSourceSnapshot(d, id)
	repo := Repository{DB: guideDB(t)}
	scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "en"}
	model := ModelIdentity{ID: "m", ContextTokens: 128000}
	rev, err := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, Model: model, DependencyID: "parser"})
	if err != nil {
		t.Fatal(err)
	}
	repo.DB.Model(&rev).Update("fingerprint", digest([]any{id.SourceSHA256, "", "paper-guide-v1", model}))
	calls := 0
	service := Service{Repo: repo, Source: &fixtureSource{s: snap}, Authorize: func(context.Context, Scope, bool) error { return nil }, ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil }, Call: func(ctx context.Context, task string, input map[string]any, cfg map[string]any) (json.RawMessage, error) {
		calls++
		switch task {
		case "paper.guide.plan":
			return json.Marshal(baselinePlan(snap))
		case "paper.guide.section":
			return json.Marshal(teachingSection(input["role"].(Role)))
		}
		return json.Marshal(GuideReview{Findings: []GuideFinding{}})
	}}
	if _, err = service.Run(t.Context(), asyncjob.Job{ID: rev.JobID, PayloadJSON: mustJSON(JobPayload{rev.ID, rev.Epoch})}, testFence{repo.DB}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Revision(t.Context(), scope, rev.ID)
	if err != nil || got.Fingerprint != contractFingerprint(id.SourceSHA256, snap.IRDigest, model) || got.Stage != "completed" || calls != 10 {
		t.Fatalf("first freeze: %+v calls=%d err=%v", got, calls, err)
	}
}

func TestUncertainLegacyAttemptCannotAdoptContract(t *testing.T) {
	repo := Repository{DB: guideDB(t)}
	scope := Scope{OwnerID: "u", DatasetID: "ds", DocumentID: "doc", Language: "en"}
	rev, err := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: "hash", Model: ModelIdentity{ID: "m"}})
	if err != nil {
		t.Fatal(err)
	}
	rev.Fingerprint = "legacy"
	// Even a zero call counter with a persisted attempt/checkpoint is not pristine.
	repo.DB.Create(&orm.PaperGuideStep{ID: "ticket", RevisionID: rev.ID, StepKey: "request:plan:unknown"})
	if err = repo.CheckContract(t.Context(), rev); !errors.Is(err, ErrRegenerate) {
		t.Fatalf("uncertain attempt adopted: %v", err)
	}
}

func TestImpossibleOutputReserveMakesZeroModelCalls(t *testing.T) {
	for _, reserve := range []int{1024, 2048} {
		t.Run(fmt.Sprint(reserve), func(t *testing.T) {
			d, id := fixture(t)
			snap, _ := BuildSourceSnapshot(d, id)
			repo := Repository{DB: guideDB(t)}
			scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "en"}
			model := ModelIdentity{ID: "m", ContextTokens: 1024, OutputReserveTokens: reserve}
			rev, err := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: snap.IRDigest, SnapshotPath: "current", Model: model})
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			service := Service{Repo: repo, Source: &fixtureSource{s: snap}, Authorize: func(context.Context, Scope, bool) error { return nil }, ResolveModel: func(context.Context, Scope, string) (ModelIdentity, map[string]any, error) { return model, nil, nil }, Call: func(context.Context, string, map[string]any, map[string]any) (json.RawMessage, error) {
				calls++
				return nil, nil
			}}
			_, err = service.Run(t.Context(), asyncjob.Job{ID: rev.JobID, PayloadJSON: mustJSON(JobPayload{rev.ID, rev.Epoch})}, testFence{repo.DB})
			if !errors.Is(err, ErrContext) || calls != 0 {
				t.Fatalf("impossible request spent calls=%d err=%v", calls, err)
			}

		})
	}
}

func TestV2ThroughV5PublishedGuideAndSelectionRemainReadable(t *testing.T) {
	for _, version := range []string{"paper-guide-v2", "paper-guide-v3", "paper-guide-v4", "paper-guide-v5"} {
		t.Run(version, func(t *testing.T) {
			d, id := fixture(t)
			current, _ := BuildSourceSnapshot(d, id)
			old := current
			repo := Repository{DB: guideDB(t)}
			scope := Scope{OwnerID: id.OwnerID, DatasetID: id.DatasetID, DocumentID: id.DocumentID, Language: "zh"}
			model := ModelIdentity{ID: "m"}
			rev, err := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: old.IRDigest, SnapshotPath: "legacy", Model: model})
			if err != nil {
				t.Fatal(err)
			}
			section := teachingSection(RoleBackground)
			section.Version = 1
			section.PublicationStatus = "published"
			section.Blocks[0].EvidenceIDs = []string{old.Evidence[1].ID}
			for _, step := range []StepWrite{{Key: "plan", InputHash: "old-plan", Output: mustJSON(baselinePlan(old))}, {Key: "section:background:v1", InputHash: "old-section", Output: mustJSON(section), Published: true}} {
				if err = repo.PutStep(t.Context(), testFence{repo.DB}, rev.ID, rev.Epoch, step); err != nil {
					t.Fatal(err)
				}
			}
			if err = repo.Transition(t.Context(), testFence{repo.DB}, rev.ID, rev.Epoch, "completed", ""); err != nil {
				t.Fatal(err)
			}
			repo.DB.Model(&rev).Update("fingerprint", digest([]any{id.SourceSHA256, old.IRDigest, SourceCatalogVersion, version, model}))
			service := Service{Repo: repo, Source: &historicalSource{fixtureSource{current, nil}, old}, Authorize: func(context.Context, Scope, bool) error { return nil }}
			got, err := service.Revision(t.Context(), scope, rev.ID, 0)
			if err != nil || !got.SourceCurrent || len(got.Sections) != 1 {
				t.Fatalf("legacy history: %+v %v", got, err)
			}
			evidence, currentSource, err := service.Evidence(t.Context(), scope, rev.ID, old.Evidence[1].ID)
			if err != nil || !currentSource || evidence.ID != old.Evidence[1].ID {
				t.Fatalf("legacy evidence: %+v %v", evidence, err)
			}
			selected, err := service.Selection(t.Context(), scope, rev.ID, SelectionRequest{SectionKey: "section:background:v1", BlockID: "b_background", Start: 0, End: 3})
			if err != nil || selected.Text != "The" || len(selected.Evidence) != 1 || selected.Evidence[0].ID != old.Evidence[1].ID {
				t.Fatalf("legacy selection: %+v %v", selected, err)
			}
		})
	}
}
