package doc

import (
	"context"
	"encoding/json"
	"errors"
	"gorm.io/gorm"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/pdfir"
)

type readingCapabilityStatus struct {
	ID           string `json:"id"`
	Status       string `json:"status"`
	Completed    int    `json:"completed"`
	Total        int    `json:"total"`
	Running      int    `json:"running,omitempty"`
	Failed       int    `json:"failed,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

// Keys are the same source-, document-, user- and language-scoped paths as the
// saved artifacts. In-flight work is process-local; durable batch jobs are read
// separately. Failed standalone attempts expire instead of growing without bound.
type readingWorkState struct {
	running int
	failed  bool
	updated time.Time
}

var readingWork = struct {
	sync.Mutex
	items map[string]readingWorkState
}{items: map[string]readingWorkState{}}

func startReadingWork(path string) func(error) {
	readingWork.Lock()
	for key, state := range readingWork.items {
		if state.running == 0 && time.Since(state.updated) > 10*time.Minute {
			delete(readingWork.items, key)
		}
	}
	state := readingWork.items[path]
	state.running++
	state.failed = false
	state.updated = time.Now()
	readingWork.items[path] = state
	readingWork.Unlock()
	return func(err error) {
		readingWork.Lock()
		defer readingWork.Unlock()
		state := readingWork.items[path]
		state.running--
		state.failed = err != nil
		state.updated = time.Now()
		if state.running == 0 && !state.failed {
			delete(readingWork.items, path)
		} else {
			readingWork.items[path] = state
		}
	}
}
func readingWorkFor(path string) readingWorkState {
	readingWork.Lock()
	defer readingWork.Unlock()
	state := readingWork.items[path]
	if state.running == 0 && time.Since(state.updated) > 10*time.Minute {
		delete(readingWork.items, path)
		return readingWorkState{}
	}
	return state
}
func paperVisualCacheKey(d pdfir.Document, blockID, language string, evidence []formulaEvidence) string {
	keyData, _ := json.Marshal([]any{paperReadingPromptVersion, "visual", blockID, d.SourceSHA256, d.Parser, pdfir.Revision, d.ProjectionVersion, "", language, evidence, []pdfir.Block(nil)})
	return pdfir.Digest(string(keyData))
}
func readingArtifactDir(source, dataset, document, user, kind string) string {
	return filepath.Join(filepath.Dir(source), ".lazymind-artifacts", kind, pdfIRResource(dataset, document), pdfir.Digest(user))
}
func readReadingArtifact(path string, max int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, max+1))
	return string(raw), err
}
func readReadingCapabilities(state pdfIRResponse, source, dataset, document, user string, jobs []orm.AsyncJob) ([]readingCapabilityStatus, error) {
	caps := []readingCapabilityStatus{{ID: "formula"}, {ID: "figure"}, {ID: "table"}}
	ready := state.Status == "ready" && state.Document != nil && state.Document.Version == 1 && state.Document.CoordinateSpace == "normalized_top_left"
	if ready {
		d := *state.Document
		for _, block := range d.Blocks {
			index := -1
			kind := "paper-reading"
			switch block.Type {
			case "formula":
				index = 0
				kind = "formula-explanations"
			case "image":
				index = 1
			case "table":
				index = 2
			}
			if index < 0 {
				continue
			}
			c := &caps[index]
			c.Total++
			var evidence []formulaEvidence
			var err error
			if index == 0 {
				_, evidence, err = formulaExplanationContext(d, block.ID)
			} else {
				_, evidence, err = paperVisualContext(d, block.ID)
			}
			if err != nil {
				continue
			}
			completed, running, failed := false, false, false
			for _, language := range []string{"zh", "en"} {
				key := paperVisualCacheKey(d, block.ID, language, evidence)
				if index == 0 {
					key = formulaExplanationCacheKey(d, block.ID, block.Latex, "saved-explanation:"+language, evidence)
				}
				path := filepath.Join(readingArtifactDir(source, dataset, document, user, kind), key+".json")
				max := int64(paperReadingMaxBytes)
				if index == 0 {
					max = int64(formulaExplanationMaxBytes)
				}
				raw, err := readReadingArtifact(path, max)
				if err != nil {
					return nil, err
				}
				if raw != "" {
					if index == 0 {
						_, err = validateCachedFormulaExplanation(raw, evidence)
					} else {
						_, err = validatePaperVisual(raw, evidence, language)
					}
					completed = completed || err == nil
				}
				work := readingWorkFor(path)
				running = running || work.running > 0
				failed = failed || work.failed
			}
			if completed {
				c.Completed++
			}
			if running {
				c.Running++
			}
			if failed && !completed {
				c.Failed++
			}
		}
	}
	for i := range caps {
		finalizeReadingCapability(&caps[i], ready, jobs)
		if !ready && caps[i].Status == "not_requested" && (state.Status == "failed" || state.Status == "unavailable" || state.Status == "ready") {
			caps[i].Status = "unavailable"
			caps[i].ErrorMessage = state.ErrorMessage
		}
	}
	return caps, nil
}

// Jobs are newest first. A recent completed batch must not hide another active
// batch, and successful bounded batches must not imply all blocks are cached.
func finalizeReadingCapability(c *readingCapabilityStatus, ready bool, jobs []orm.AsyncJob) {
	c.Status = "not_requested"
	var latest *orm.AsyncJob
	active := ""
	for i := range jobs {
		var p readingPreparationPayload
		if json.Unmarshal(jobs[i].PayloadJSON, &p) != nil {
			continue
		}
		for _, scope := range p.Scopes {
			if scope != c.ID {
				continue
			}
			if latest == nil {
				latest = &jobs[i]
			}
			switch jobs[i].Status {
			case "running":
				active = "running"
			case "pending", "retry_wait":
				if active == "" {
					active = "pending"
				}
			}
			break
		}
	}
	switch {
	case c.Running > 0:
		c.Status = "running"
	case ready && c.Total == 0:
		c.Status = "empty"
	case ready && c.Completed == c.Total:
		c.Status = "succeeded"
	case active != "":
		c.Status = active
	case c.Completed > 0:
		c.Status = "partial"
	case c.Failed > 0:
		c.Status = "failed"
		c.ErrorMessage = "研读生成失败，请重试"
	case latest != nil && (latest.Status == "failed" || latest.Status == "canceled"):
		c.Status = "failed"
	case latest != nil && latest.Status == "succeeded":
		c.Status = "partial"
	}
	if c.Failed > 0 && c.ErrorMessage == "" && c.Status == "partial" {
		c.ErrorMessage = "部分研读生成失败，请重试"
	}
	if latest != nil && latest.Status == "failed" && latest.ErrorMessage != "" && c.Status != "succeeded" && c.Status != "empty" && active == "" {
		c.ErrorMessage = latest.ErrorMessage
	}
}

// Retain the latest batch for backwards-compatible fields and the latest batch
// per capability, plus every active batch. Historical retries are not all loaded.
func loadReadingPreparationJobs(ctx context.Context, db *gorm.DB, row orm.Document, user, hash string) ([]orm.AsyncJob, error) {
	base := db.WithContext(ctx).Where("job_type=? AND create_user_id=?", ReadingPreparationJob, user).
		Where("payload_json ->> 'dataset_id' = ? AND payload_json ->> 'document_id' = ? AND payload_json ->> 'source_sha256' = ?", row.DatasetID, row.ID, hash)
	var ids []string
	for _, scope := range []string{"", "formula", "figure", "table"} {
		q := base.Session(&gorm.Session{})
		if scope != "" {
			if db.Dialector.Name() == "sqlite" {
				q = q.Where("EXISTS (SELECT 1 FROM json_each(payload_json, '$.scopes') WHERE value = ?)", scope)
			} else {
				q = q.Where("(payload_json -> 'scopes')::jsonb @> ?::jsonb", string(mustJSON([]string{scope})))
			}
		}
		var recent []orm.AsyncJob
		if err := q.Select("id").Order("created_at DESC, id DESC").Limit(1).Find(&recent).Error; err != nil {
			return nil, err
		}
		if len(recent) > 0 {
			ids = append(ids, recent[0].ID)
		}
	}
	var jobs []orm.AsyncJob
	err := base.Session(&gorm.Session{}).Where("status IN ? OR id IN ?", []string{"pending", "running", "retry_wait"}, ids).Order("created_at DESC, id DESC").Find(&jobs).Error
	return jobs, err
}
