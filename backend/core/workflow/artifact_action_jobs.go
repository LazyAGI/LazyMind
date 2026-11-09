package workflow

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"lazymind/core/algo"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/workflow/controlstore"
	workflowstore "lazymind/core/workflow/store"
)

// The shared package produces each model request and validates the returned
// text. No client-supplied HTML, selector, commit token or tool code is trusted.
func enqueueArtifactAction(w http.ResponseWriter, r *http.Request, target *artifactActionTarget, body artifactActionPreviewBody) {
	version, err := resolveArtifactActionWorkflow(r.Context(), target, body.Action)
	if err != nil {
		common.ReplyErr(w, "artifact action unavailable", 422)
		return
	}
	binding, err := controlstore.DecodeBinding(*target.session)
	if err != nil || binding.DriverSession == "" || binding.ConnectorID == "" {
		common.ReplyErr(w, "Connect an external agent before editing this artifact", 409)
		return
	}
	responses := []map[string]any{}
	req := algo.WorkflowActionInvokeRequest{WorkflowID: version.workflowID, RevisionID: version.revisionID, TreeHash: version.treeHash,
		UserID: store.UserID(r), Action: body.Action, Phase: "preview", Slot: target.revision.Slot, Artifact: target.artifact,
		Arguments: body.Input, ArtifactStore: target.artifactStore, GenerationResponses: &responses}
	// A package must explicitly advertise support; an older planner must never
	// accidentally fall through to a locally configured model.
	if err := checkExternalActionPackage(r, target, version, body.Action); err != nil {
		common.ReplyErr(w, err.Error(), 422)
		return
	}
	if err := attachActionCompanion(r, target, &req); err != nil {
		common.ReplyErr(w, err.Error(), 409)
		return
	}
	// Recover an unchanged request after a browser/network interruption.
	var pending []orm.WorkflowActionJob
	if err := target.db.WithContext(r.Context()).Where("session_id = ? AND slot_id = ? AND base_revision = ? AND status IN ?", target.session.ID, target.revision.SlotID, body.BaseRevision, []string{"waiting_agent", "ready"}).Find(&pending).Error; err != nil {
		common.ReplyErr(w, "cannot read queued edits", 500)
		return
	}
	for _, prior := range pending {
		var saved algo.WorkflowActionInvokeRequest
		if json.Unmarshal([]byte(prior.RequestJSON), &saved) == nil {
			a, _ := json.Marshal(saved.Arguments)
			b, _ := json.Marshal(req.Arguments)
			index := -1
			if target.revision.ListIndex != nil {
				index = *target.revision.ListIndex
			}
			if string(a) == string(b) && prior.ListIndex == index && sameDraftVersion(prior.BaseDraftVersion, body.BaseDraftVersion) {
				common.ReplyOK(w, actionJobView(prior))
				return
			}
		}
	}
	response, status, err := algo.InvokeWorkflowAction(r.Context(), req)
	if err != nil {
		replyWorkflowActionError(w, status, err)
		return
	}
	requestJSON, _ := json.Marshal(req)
	index := -1
	if target.revision.ListIndex != nil {
		index = *target.revision.ListIndex
	}
	job := orm.WorkflowActionJob{ID: common.GenerateID(), SessionID: target.session.ID, SlotID: target.revision.SlotID, ListIndex: index,
		BaseRevision: body.BaseRevision, BaseDraftVersion: body.BaseDraftVersion, Version: 1, RequestJSON: string(requestJSON), ResultJSON: string(response.Result)}
	job.Status = actionJobStatus(response.Result)
	err = target.db.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&job).Error; err != nil {
			return err
		}
		if job.Status == "ready" {
			return nil
		}
		_, err := controlstore.EnqueueHostAction(tx, *target.session, "artifact-edit:"+job.ID, "artifact_edit", job.ID)
		return err
	})
	if err != nil {
		common.ReplyErr(w, "cannot queue external edit", 409)
		return
	}
	common.ReplyOK(w, actionJobView(job))
}

func actionJobStatus(raw []byte) string {
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	if result["status"] == "needs_generation" {
		return "waiting_agent"
	}
	return "ready"
}

func actionJobView(job orm.WorkflowActionJob) map[string]any {
	var result map[string]any
	_ = json.Unmarshal([]byte(job.ResultJSON), &result)
	if job.Status != "ready" {
		return map[string]any{"status": job.Status, "job_id": job.ID, "version": job.Version, "generation_request": result["generation_request"]}
	}
	result["status"] = "ready"
	result["job_id"] = job.ID
	result["version"] = job.Version
	result["action"] = "rewrite_selection"
	result["base_revision"] = job.BaseRevision
	if job.BaseDraftVersion != nil {
		result["base_draft_version"] = *job.BaseDraftVersion
	}
	result["commit"] = map[string]any{"token": "external:" + job.ID}
	return result
}

func GetArtifactActionJob(w http.ResponseWriter, r *http.Request) {
	var job orm.WorkflowActionJob
	if err := store.DB().WithContext(r.Context()).Where("id = ? AND session_id = ?", common.PathVar(r, "job_id"), common.PathVar(r, "session_id")).First(&job).Error; err != nil {
		common.ReplyErr(w, "artifact edit not found", 404)
		return
	}
	common.ReplyOK(w, actionJobView(job))
}

func SubmitArtifactActionJob(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Version     int64  `json:"version"`
		RequestHash string `json:"request_hash"`
		Text        string `json:"text"`
		Cancel      bool   `json:"cancel"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&input) != nil {
		common.ReplyErr(w, "invalid generation response", 400)
		return
	}
	var job orm.WorkflowActionJob
	err := func() error {
		if err := store.DB().WithContext(r.Context()).Where("id = ? AND session_id = ?", common.PathVar(r, "job_id"), common.PathVar(r, "session_id")).First(&job).Error; err != nil {
			return err
		}
		previousVersion := job.Version
		if input.Cancel {
			if job.Status == "cancelled" || job.Status == "applied" {
				return nil
			}
			job.Status = "cancelled"
		} else {
			var req algo.WorkflowActionInvokeRequest
			var result struct {
				Request struct {
					Hash string `json:"request_hash"`
				} `json:"generation_request"`
			}
			if json.Unmarshal([]byte(job.RequestJSON), &req) != nil || json.Unmarshal([]byte(job.ResultJSON), &result) != nil {
				return fmt.Errorf("invalid saved action")
			}
			// Retried responses are idempotent, but cannot replace an accepted response.
			if req.GenerationResponses != nil && input.Version > 0 && input.Version <= int64(len(*req.GenerationResponses)) {
				prior := (*req.GenerationResponses)[input.Version-1]
				if prior["request_hash"] == input.RequestHash && prior["text"] == input.Text {
					return nil
				}
			}
			if job.Status != "waiting_agent" || job.Version != input.Version || result.Request.Hash != input.RequestHash || strings.TrimSpace(input.Text) == "" {
				return fmt.Errorf("stale or invalid edit response")
			}
			*req.GenerationResponses = append(*req.GenerationResponses, map[string]any{"request_hash": input.RequestHash, "text": input.Text})
			if len(*req.GenerationResponses) > 16 {
				return fmt.Errorf("edit generation limit reached")
			}
			response, _, err := algo.InvokeWorkflowAction(r.Context(), req)
			if err != nil {
				return err
			}
			encoded, _ := json.Marshal(req)
			job.RequestJSON = string(encoded)
			job.ResultJSON = string(response.Result)
			job.Status = actionJobStatus(response.Result)
		}
		job.Version++
		job.UpdatedAt = time.Now().UTC()
		// Invoke the planner outside a database transaction: it reads the package
		// back through Core, which otherwise deadlocks SQLite's sole connection.
		return store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
			updated := tx.Model(&orm.WorkflowActionJob{}).Where("id = ? AND version = ?", job.ID, previousVersion).
				Updates(map[string]any{"status": job.Status, "version": job.Version, "request_json": job.RequestJSON, "result_json": job.ResultJSON, "updated_at": job.UpdatedAt})
			if updated.Error != nil {
				return updated.Error
			}
			if updated.RowsAffected != 1 {
				return fmt.Errorf("edit changed during generation; reload before retrying")
			}
			if job.Status != "waiting_agent" {
				return tx.Model(&orm.WorkflowHostAction{}).Where("session_id = ? AND kind = 'artifact_edit' AND execution_id = ?", job.SessionID, job.ID).Update("consumed_at", time.Now().UTC()).Error
			}
			return nil
		})
	}()
	if err != nil {
		common.ReplyErr(w, err.Error(), 409)
		return
	}
	common.ReplyOK(w, actionJobView(job))
}

func loadExternalActionCommit(r *http.Request, target *artifactActionTarget, body *artifactActionPreviewBody) (artifactActionWorkflowVersion, error) {
	token, _ := body.Input["commit_token"].(string)
	if !strings.HasPrefix(token, "external:") {
		return artifactActionWorkflowVersion{}, fmt.Errorf("external preview confirmation required")
	}
	var job orm.WorkflowActionJob
	if err := target.db.WithContext(r.Context()).Where("id = ? AND session_id = ?", strings.TrimPrefix(token, "external:"), target.session.ID).First(&job).Error; err != nil {
		return artifactActionWorkflowVersion{}, err
	}
	index := -1
	if target.revision.ListIndex != nil {
		index = *target.revision.ListIndex
	}
	if job.Status != "ready" || job.SlotID != target.revision.SlotID || job.ListIndex != index || job.BaseRevision != body.BaseRevision || !sameDraftVersion(job.BaseDraftVersion, body.BaseDraftVersion) {
		return artifactActionWorkflowVersion{}, fmt.Errorf("stale or cancelled edit preview")
	}
	var req algo.WorkflowActionInvokeRequest
	var result struct {
		Commit struct {
			Token string `json:"token"`
		} `json:"commit"`
	}
	if json.Unmarshal([]byte(job.RequestJSON), &req) != nil || json.Unmarshal([]byte(job.ResultJSON), &result) != nil || result.Commit.Token == "" {
		return artifactActionWorkflowVersion{}, fmt.Errorf("invalid saved preview")
	}
	body.Input = map[string]any{"commit_token": result.Commit.Token}
	return artifactActionWorkflowVersion{workflowID: req.WorkflowID, revisionID: req.RevisionID, treeHash: req.TreeHash}, nil
}
func sameDraftVersion(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func checkExternalActionPackage(r *http.Request, target *artifactActionTarget, version artifactActionWorkflowVersion, action string) error {
	pkg, err := workflowstore.New(target.db).GetWorkflowPackage(r.Context(), target.session.CreateUserID, target.session.WorkflowRef, version.revisionID)
	if err != nil {
		return err
	}
	var manifest struct {
		Actions map[string]struct {
			External bool `yaml:"external_generation"`
		} `yaml:"artifact_actions"`
	}
	if yaml.Unmarshal(pkg.Files["workflow.yaml"], &manifest) != nil || !manifest.Actions[action].External {
		return fmt.Errorf("Update the workflow package to enable external AI editing")
	}
	return nil
}

// The package declares its companion slot; the host cannot choose a file path.
func attachActionCompanion(r *http.Request, target *artifactActionTarget, req *algo.WorkflowActionInvokeRequest) error {
	pkg, err := workflowstore.New(target.db).GetWorkflowPackage(r.Context(), target.session.CreateUserID, target.session.WorkflowRef, req.RevisionID)
	if err != nil {
		return err
	}
	var manifest struct {
		Actions map[string]struct {
			Companion string `yaml:"companion_slot"`
		} `yaml:"artifact_actions"`
	}
	if err = yaml.Unmarshal(pkg.Files["workflow.yaml"], &manifest); err != nil {
		return err
	}
	slot := manifest.Actions[req.Action].Companion
	if slot == "" {
		return nil
	}
	revision, value, _, err := loadSelectedArtifactValue(r.Context(), target.db, target.session.ID, slot, nil)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}
	var decoded map[string]any
	if json.Unmarshal(value, &decoded) != nil {
		return fmt.Errorf("invalid companion artifact")
	}
	var draft *int64
	if revision.HumanArtifactID != nil {
		var human orm.WorkflowHumanArtifact
		if err := target.db.Where("id = ?", *revision.HumanArtifactID).First(&human).Error; err != nil {
			return err
		}
		draft = &human.DraftVersion
	}
	req.Arguments["native_companion"] = map[string]any{"slot_id": revision.SlotID, "slot": revision.Slot, "revision": revision.Revision, "draft_version": draft, "step_id": revision.StepID, "attempt": revision.Attempt, "value": decoded}
	return nil
}
