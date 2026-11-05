package workflow

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	corestore "lazymind/core/store"
)

type paperExportImage struct {
	Source string `json:"source"`
	SHA256 string `json:"sha256"`
	Alt    string `json:"alt"`
}

func paperImageExportFixture(t *testing.T) (rollbackFixture, DocumentImageExportRequest, []paperExportImage, []string) {
	t.Helper()
	f, input := publicationFixture(t, false)
	if err := f.db.AutoMigrate(&orm.AsyncJob{}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&orm.WorkflowSession{}).Where("id = ?", input.SessionID).Update("plugin_id", "paper-writing").Error; err != nil {
		t.Fatal(err)
	}
	corestore.Init(f.db.DB, nil, nil)
	t.Cleanup(func() { corestore.Init(nil, nil, nil) })
	root := t.TempDir()
	t.Setenv("LAZYMIND_UPLOAD_ROOT", root)
	t.Setenv("LAZYMIND_SUBAGENT_WORKSPACE", t.TempDir())
	raw, err := os.ReadFile("testdata/paper_image_export_20261030/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Items []paperExportImage `json:"items"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil || len(manifest.Items) != 3 {
		t.Fatalf("invalid regression manifest: %v", err)
	}
	markdown := "# Paper\n"
	paths := []string{}
	for i, image := range manifest.Items {
		data, err := os.ReadFile(filepath.Join("testdata/paper_image_export_20261030", image.SHA256+".svg"))
		if err != nil || imageExportHash(data) != image.SHA256 {
			t.Fatalf("original SVG differs from failed export: %v", err)
		}
		path := filepath.Join(root, fmt.Sprintf("analysis-chart-%d.svg", i))
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
		value, _ := json.Marshal(map[string]any{"path": path, "filename": filepath.Base(path)})
		id := fmt.Sprintf("paper-chart-%d", i)
		if err := f.db.Create(&orm.WorkflowHumanArtifact{ID: id, SessionID: input.SessionID, Slot: "analysis_chart", ContentType: "file", Value: value}).Error; err != nil {
			t.Fatal(err)
		}
		index := i
		if err := f.db.Create(&orm.WorkflowSlotRevision{ID: id, SessionID: input.SessionID, SlotID: "analysis_chart", Slot: "analysis_chart", HumanArtifactID: &id, ListIndex: &index, Selected: true, Validity: "effective", Revision: 1}).Error; err != nil {
			t.Fatal(err)
		}
		markdown += fmt.Sprintf("![%s](%s)\n", image.Alt, image.Source)
	}
	document, _ := json.Marshal(markdown)
	return f, DocumentImageExportRequest{BaseRevision: &input.BaseRevision, BaseDraftVersion: input.BaseDraftVersion, Document: document}, manifest.Items, paths
}

func completePaperImageExport(t *testing.T, f rollbackFixture, created DocumentImageExportState) DocumentImageExportState {
	t.Helper()
	job, err := asyncjob.Get(t.Context(), f.db.DB, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	result, err := runDocumentImageExport(t.Context(), asyncjob.Job{ID: job.ID, CreateUserID: "owner", PayloadJSON: job.PayloadJSON}, exportTestReporter{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(job).Updates(map[string]any{"status": "succeeded", "result_json": result.ResultJSON}).Error; err != nil {
		t.Fatal(err)
	}
	return imageExportReply(t, imageExportHTTP(t, ReadDocumentImageExportHTTP, "owner", f.currentRevisionID, created.ID, "", nil))
}

func TestPaperImageExport20261030DownloadsOriginalCharts(t *testing.T) {
	f, body, images, _ := paperImageExportFixture(t)
	created := imageExportReply(t, imageExportHTTP(t, CreateDocumentImageExportHTTP, "owner", f.currentRevisionID, "", "", body))
	ready := completePaperImageExport(t, f, created)
	if ready.Status != "ready" || ready.Ready != 3 || ready.Total != 3 {
		t.Fatalf("not ready: %#v", ready)
	}
	response := imageExportHTTP(t, DownloadDocumentImageExportHTTP, "owner", f.currentRevisionID, ready.ID, "bundle", nil)
	archive, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if response.Code != 200 || err != nil || len(archive.File) != 3 {
		t.Fatalf("download HTTP %d: %v", response.Code, err)
	}
	for i, file := range archive.File {
		reader, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		reader.Close()
		if err != nil || imageExportHash(data) != images[i].SHA256 {
			t.Fatalf("archive image %d changed: %v", i, err)
		}
	}
	if response := imageExportHTTP(t, DownloadDocumentImageExportHTTP, "other", f.currentRevisionID, ready.ID, "bundle", nil); response.Code != 404 {
		t.Fatal("cross-user download accepted")
	}
	body.ImageReference = images[0].Source
	single := imageExportReply(t, imageExportHTTP(t, CreateDocumentImageExportHTTP, "owner", f.currentRevisionID, "", "", body))
	single = completePaperImageExport(t, f, single)
	response = imageExportHTTP(t, DownloadDocumentImageExportHTTP, "owner", f.currentRevisionID, single.ID, "image-001", nil)
	if single.Ready != 1 || !single.Single || response.Code != 200 || imageExportHash(response.Body.Bytes()) != images[0].SHA256 {
		t.Fatal("single original download failed")
	}
}

func TestPaperImageExportKeepsAvailableCharts(t *testing.T) {
	f, body, _, paths := paperImageExportFixture(t)
	if err := os.Remove(paths[1]); err != nil {
		t.Fatal(err)
	}
	created := imageExportReply(t, imageExportHTTP(t, CreateDocumentImageExportHTTP, "owner", f.currentRevisionID, "", "", body))
	state := completePaperImageExport(t, f, created)
	if state.Status != "partial" || state.Ready != 2 || state.Items[1].ErrorCode != "IMAGE_UNAVAILABLE" {
		t.Fatalf("state=%#v", state)
	}
	response := imageExportHTTP(t, DownloadDocumentImageExportHTTP, "owner", f.currentRevisionID, state.ID, "bundle", nil)
	archive, err := zip.NewReader(bytes.NewReader(response.Body.Bytes()), int64(response.Body.Len()))
	if response.Code != 200 || err != nil || len(archive.File) != 2 {
		t.Fatalf("partial download HTTP %d: %v", response.Code, err)
	}
}

func TestPaperImageExportRejectsUnboundCharts(t *testing.T) {
	for _, scenario := range []string{"foreign-session", "unselected", "stale", "other-workflow", "unregistered-reference", "modified-svg", "escaped-symlink"} {
		t.Run(scenario, func(t *testing.T) {
			f, body, images, paths := paperImageExportFixture(t)
			var err error
			switch scenario {
			case "foreign-session":
				err = f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "paper-chart-0").Update("session_id", "another-session").Error
			case "unselected":
				err = f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "paper-chart-0").Update("selected", false).Error
			case "stale":
				err = f.db.Model(&orm.WorkflowSlotRevision{}).Where("id = ?", "paper-chart-0").Update("validity", "stale").Error
			case "other-workflow":
				err = f.db.Model(&orm.WorkflowSession{}).Where("id = ?", "rollback-session").Update("plugin_id", "writer-workflow").Error
			case "modified-svg":
				err = os.WriteFile(paths[0], []byte(`<svg xmlns="http://www.w3.org/2000/svg"><text>changed</text></svg>`), 0o600)
			case "escaped-symlink":
				outside := filepath.Join(t.TempDir(), "outside.svg")
				data, readErr := os.ReadFile(paths[0])
				if readErr != nil {
					t.Fatal(readErr)
				}
				if err = os.WriteFile(outside, data, 0o600); err == nil {
					err = os.Remove(paths[0])
				}
				if err == nil {
					err = os.Symlink(outside, paths[0])
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			target, err := prepareImageExport(t.Context(), "owner", f.currentRevisionID, body)
			if err != nil {
				t.Fatal(err)
			}
			ref := images[0].Source
			if scenario == "unregistered-reference" {
				ref = "/static-files/paper-charts/" + images[0].SHA256 + "-foreign.svg"
			}
			sources, err := documentImageSources(t.Context(), target, []imageReference{{Source: ref}})
			if err != nil || len(sources) != 1 || sources[0].Source != "" {
				t.Fatalf("unsafe chart accepted: %#v %v", sources, err)
			}
		})
	}
}

func TestPaperImageExportRechecksHashBeforeDownload(t *testing.T) {
	f, body, images, paths := paperImageExportFixture(t)
	target, err := prepareImageExport(t.Context(), "owner", f.currentRevisionID, body)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := documentImageSources(t.Context(), target, []imageReference{{Source: images[0].Source}})
	if err != nil || len(sources) != 1 || sources[0].ExpectedHash != images[0].SHA256 {
		t.Fatalf("missing digest: %#v %v", sources, err)
	}
	if err := os.WriteFile(paths[0], []byte(`<svg xmlns="http://www.w3.org/2000/svg"><text>changed</text></svg>`), 0o600); err != nil {
		t.Fatal(err)
	}
	client := imageExportHTTPClient()
	defer client.CloseIdleConnections()
	if _, code := fetchDocumentImage(t.Context(), client, "owner", sources[0]); code != "IMAGE_CHANGED" {
		t.Fatalf("changed image code=%s", code)
	}
}
