package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/cloudpackage"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

var ErrCloudWorkflowAlreadyExists = errors.New("cloud Workflow already exists locally")

type CloudWorkflowImportRequest struct {
	DB          *gorm.DB
	OwnerUserID string
	Files       map[string][]byte
}

type CloudWorkflowImportResult struct {
	WorkflowRef string `json:"workflow_ref"`
	RevisionID  string `json:"revision_id"`
	GraphHash   string `json:"graph_hash"`
	Enabled     bool   `json:"enabled"`
}

func ImportCloudWorkflowPackage(ctx context.Context, request CloudWorkflowImportRequest) (CloudWorkflowImportResult, error) {
	owner := strings.TrimSpace(request.OwnerUserID)
	if request.DB == nil || owner == "" {
		return CloudWorkflowImportResult{}, errors.New("cloud Workflow import request is incomplete")
	}
	packageFiles := make(map[string]cloudpackage.File, len(request.Files))
	for path, body := range request.Files {
		if isWorkflowRuntimeFile(path) {
			return CloudWorkflowImportResult{}, fmt.Errorf("runtime-only Workflow file is not importable: %s", path)
		}
		packageFiles[path] = cloudpackage.File{Data: body}
	}
	workflowYAML := string(request.Files["workflow.yaml"])
	workflowID := extractWorkflowID(workflowYAML)
	if workflowID == "" {
		return CloudWorkflowImportResult{}, errors.New("cloud Workflow package requires workflow.yaml id")
	}
	name := yamlScalar(workflowYAML, "name")
	if name == "" {
		name = workflowID
	}
	prepared, err := cloudpackage.Prepare(cloudpackage.PrepareInput{
		ResourceType: "workflow", ResourceName: name, ClientResourceKey: "cloud-import:" + workflowID,
		DesktopVersion: "0.0.0", Files: packageFiles,
	})
	if err != nil {
		return CloudWorkflowImportResult{}, err
	}
	compiled := graphengine.Compile(
		string(prepared.Files["workflow.yaml"].Data),
		string(prepared.Files["scenario/state.yml"].Data),
		string(prepared.Files["scenario/scenario.md"].Data),
		graphengine.ProfilePublish,
	)
	if !compiled.Valid || compiled.Graph == nil {
		return CloudWorkflowImportResult{}, errors.New("cloud Workflow package failed local compilation")
	}

	workflowRef := "user:" + owner + ":" + workflowID
	var existing orm.WorkflowResource
	err = request.DB.WithContext(ctx).Where("plugin_ref = ?", workflowRef).First(&existing).Error
	if err == nil {
		return CloudWorkflowImportResult{}, ErrCloudWorkflowAlreadyExists
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return CloudWorkflowImportResult{}, err
	}

	now := time.Now().UTC()
	resourceID, revisionID := uuid.NewString(), uuid.NewString()
	result := CloudWorkflowImportResult{WorkflowRef: workflowRef, RevisionID: revisionID, GraphHash: compiled.GraphHash, Enabled: false}
	err = request.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		resource := orm.WorkflowResource{
			ID: resourceID, WorkflowRef: workflowRef, WorkflowID: workflowID,
			OwnerUserID: owner, OwnerScope: ownerScope(owner), SourceType: "cloud",
			RelativeRoot: "workflows/" + ownerScope(owner) + "/" + workflowID,
			Name:         name, Description: yamlScalar(workflowYAML, "description"),
			WhenToUse:      yamlScalar(workflowYAML, "when_to_use"),
			HeadRevisionID: revisionID, Version: 1, Status: "active",
			ContainsScripts: containsWorkflowScripts(prepared.Files), CreatedAt: now, UpdatedAt: now,
		}
		if err := tx.Create(&resource).Error; err != nil {
			return err
		}
		paths := make([]string, 0, len(prepared.Files))
		for path := range prepared.Files {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		entries := make([]orm.WorkflowRevisionEntry, 0, len(paths))
		plainFiles := make(map[string][]byte, len(paths))
		for _, path := range paths {
			body := prepared.Files[path].Data
			plainFiles[path] = body
			sum := sha256.Sum256(body)
			hash := hex.EncodeToString(sum[:])
			contentType := mime.TypeByExtension(filepath.Ext(path))
			if contentType == "" {
				contentType = "text/plain"
			}
			fileType := strings.TrimPrefix(filepath.Ext(path), ".")
			if fileType == "" {
				fileType = "text"
			}
			blob := orm.WorkflowBlob{Hash: hash, Size: int64(len(body)), Mime: contentType, FileType: fileType, Content: body, CreatedAt: now}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&blob).Error; err != nil {
				return err
			}
			blobHash := hash
			entries = append(entries, orm.WorkflowRevisionEntry{
				RevisionID: revisionID, Path: path, EntryType: "file", BlobHash: &blobHash,
				Size: int64(len(body)), Mime: contentType, FileType: fileType, Mode: 0o644,
			})
		}
		revision := orm.WorkflowRevision{
			ID: revisionID, WorkflowResourceID: resourceID, RevisionNo: 1,
			TreeHash: workflowTreeHash(plainFiles), CompiledGraph: compiled.Graph.JSON(),
			GraphHash: compiled.GraphHash, GraphSchemaVersion: graphengine.SchemaVersion,
			Message: "import from LazyMind Cloud", CreatedBy: owner, CreatedAt: now,
		}
		if err := tx.Create(&revision).Error; err != nil {
			return err
		}
		if err := tx.Create(&entries).Error; err != nil {
			return err
		}
		return tx.Create(&orm.UserWorkflowSetting{UserID: owner, WorkflowRef: workflowRef, Enabled: false, UpdatedAt: now}).Error
	})
	if err != nil {
		return CloudWorkflowImportResult{}, err
	}
	return result, nil
}

func containsWorkflowScripts(files map[string]cloudpackage.File) bool {
	for path := range files {
		if strings.HasPrefix(path, "scripts/") {
			return true
		}
	}
	return false
}
