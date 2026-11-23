package paperguide

import (
	"context"
	"encoding/json"
	"errors"
	"lazymind/core/common/orm"
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

func TestPublishedGuideAndSelectionRemainReadableAcrossTaskVersions(t *testing.T) {
	for _, version := range []string{"paper-guide-v2", "paper-guide-v3", "paper-guide-v4", "paper-guide-v5", "paper-guide-v6", "paper-guide-v7", "paper-guide-v8", "paper-guide-v9", "paper-guide-v10", "paper-guide-v11", "paper-guide-v12", "paper-guide-v13", "paper-guide-v14", "paper-guide-v15", "paper-guide-v16", "paper-guide-v17", "paper-guide-v18", "paper-guide-v19", "paper-guide-v20", "paper-guide-v21"} {
		t.Run(version, func(t *testing.T) {
			d, id := fixture(t)
			current, _ := BuildSourceSnapshot(d, id)
			old := current
			if version == "paper-guide-v16" {
				old, _ = buildSourceSnapshot(d, id, sourceCatalogV5)
			}
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
			switch version {
			case "paper-guide-v13":
				section.Blocks[0].Text = "样本范围。〔e2〕"
			case "paper-guide-v14", "paper-guide-v15", "paper-guide-v16", "paper-guide-v17", "paper-guide-v18", "paper-guide-v19", "paper-guide-v20", "paper-guide-v21":
				section.Blocks[0].Text = "样本范围。校准值"
			}
			storedSection := mustJSON(section)
			for _, step := range []StepWrite{{Key: "plan", InputHash: "old-plan", Output: mustJSON(baselinePlan(old))}, {Key: "section:background:v1", InputHash: "old-section", Output: storedSection, Published: true}} {
				if err = repo.PutStep(t.Context(), testFence{repo.DB}, rev.ID, rev.Epoch, step); err != nil {
					t.Fatal(err)
				}
			}
			if err = repo.Transition(t.Context(), testFence{repo.DB}, rev.ID, rev.Epoch, "completed", ""); err != nil {
				t.Fatal(err)
			}
			if err = repo.DB.Model(&rev).Update("fingerprint", digest([]any{id.SourceSHA256, old.IRDigest, old.CatalogVersion, version, model})).Error; err != nil {
				t.Fatal(err)
			}
			if version == "paper-guide-v12" || version == "paper-guide-v13" || version == "paper-guide-v14" || version == "paper-guide-v15" || version == "paper-guide-v16" || version == "paper-guide-v17" || version == "paper-guide-v18" || version == "paper-guide-v19" || version == "paper-guide-v20" || version == "paper-guide-v21" {
				newer, err := repo.Create(t.Context(), CreateRevision{Scope: scope, SourceSHA256: id.SourceSHA256, IRDigest: old.IRDigest, SnapshotPath: "new", Model: model, Regenerate: true})
				if err != nil {
					t.Fatal(err)
				}
				if err = repo.Cancel(t.Context(), scope, newer.ID); err != nil {
					t.Fatal(err)
				}
				guide, err := repo.Guide(t.Context(), scope)
				if err != nil || guide.PublishedRevisionID != rev.ID {
					t.Fatalf("cancel hid the previous published revision: %+v %v", guide, err)
				}
			}
			service := Service{Repo: repo, Source: &historicalSource{fixtureSource{current, nil}, old}, Authorize: func(context.Context, Scope, bool) error { return nil }}
			got, err := service.Revision(t.Context(), scope, rev.ID, 0)
			if err != nil || !got.SourceCurrent || len(got.Sections) != 1 {
				t.Fatalf("legacy history: %+v %v", got, err)
			}
			if blocks := got.Sections[0].Blocks; len(blocks) != 1 || blocks[0].Text != section.Blocks[0].Text {
				t.Fatalf("legacy body changed: %+v", blocks)
			}
			response, err := service.Evidence(t.Context(), scope, rev.ID, old.Evidence[1].ID)
			evidence, currentSource := response.Evidence, response.SourceCurrent
			if err != nil || !currentSource || evidence.ID != old.Evidence[1].ID {
				t.Fatalf("legacy evidence: %+v %v", evidence, err)
			}
			var original orm.PaperGuideStep
			if err = repo.DB.Where("revision_id = ? AND step_key = ?", rev.ID, "section:background:v1").Take(&original).Error; err != nil ||
				!original.Published || original.OutputJSON != string(storedSection) {
				t.Fatalf("legacy publication was rewritten: %+v %v", original, err)
			}
		})
	}
}
