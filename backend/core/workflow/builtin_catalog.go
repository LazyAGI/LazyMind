package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

func builtinWorkflowRoot() string {
	if value := strings.TrimSpace(os.Getenv("LAZYMIND_WORKFLOW_BUILTIN_ROOT")); value != "" {
		return value
	}
	for _, candidate := range []string{"plugins", "../../plugins", "/app/plugins"} {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
	}
	return ""
}

// SeedBuiltinWorkflows imports built-in packages into the same immutable
// revision/blob store used by every Host. It is content-addressed and safe to
// run at every startup; Python Chat is not involved.
func SeedBuiltinWorkflows(ctx context.Context, db *gorm.DB) error {
	root := builtinWorkflowRoot()
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if err := seedBuiltinWorkflow(ctx, db, filepath.Join(root, entry.Name())); err != nil {
			return fmt.Errorf("seed builtin Workflow %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func seedBuiltinWorkflow(ctx context.Context, db *gorm.DB, root string) error {
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = body
		return nil
	})
	if err != nil {
		return err
	}
	workflowYAML, stateYAML := files["plugin.yaml"], files["scenario/state.yml"]
	if len(workflowYAML) == 0 || len(stateYAML) == 0 {
		return nil
	}
	compiled := graphengine.Compile(string(workflowYAML), string(stateYAML),
		string(files["scenario/scenario.md"]), graphengine.ProfilePublish)
	if !compiled.Valid || compiled.Graph == nil {
		return fmt.Errorf("invalid package: %v", compiled.Diagnostics)
	}
	var metadata struct {
		ID          string `yaml:"id"`
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		WhenToUse   string `yaml:"when_to_use"`
	}
	if err := yaml.Unmarshal(workflowYAML, &metadata); err != nil || metadata.ID == "" {
		return fmt.Errorf("workflow id is required")
	}
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	tree := sha256.New()
	for _, path := range paths {
		sum := sha256.Sum256(files[path])
		_, _ = tree.Write([]byte(path + "\x00" + hex.EncodeToString(sum[:]) + "\n"))
	}
	treeHash := hex.EncodeToString(tree.Sum(nil))
	ref := "builtin:" + metadata.ID
	now := time.Now().UTC()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var resource orm.WorkflowResource
		err := tx.Where("plugin_ref = ?", ref).First(&resource).Error
		if err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		if err == gorm.ErrRecordNotFound {
			resource = orm.WorkflowResource{ID: uuid.NewString(), WorkflowRef: ref, WorkflowID: metadata.ID,
				OwnerUserID: "", OwnerScope: "builtin", SourceType: "builtin",
				RelativeRoot: "workflows/builtin/" + metadata.ID, Name: metadata.Name,
				Description: metadata.Description, WhenToUse: metadata.WhenToUse,
				Status: "active", CreatedAt: now, UpdatedAt: now}
			if resource.Name == "" {
				resource.Name = metadata.ID
			}
			if err := tx.Create(&resource).Error; err != nil {
				return err
			}
		}
		var existing orm.WorkflowRevision
		if tx.Where("plugin_resource_id = ? AND tree_hash = ?", resource.ID, treeHash).First(&existing).Error == nil {
			return tx.Model(&resource).Updates(map[string]any{"head_revision_id": existing.ID,
				"version": existing.RevisionNo, "status": "active", "updated_at": now}).Error
		}
		revisionID := uuid.NewString()
		revision := orm.WorkflowRevision{ID: revisionID, WorkflowResourceID: resource.ID,
			ParentRevisionID: resource.HeadRevisionID, RevisionNo: resource.Version + 1,
			TreeHash: treeHash, CompiledGraph: compiled.Graph.JSON(), GraphHash: compiled.GraphHash,
			GraphSchemaVersion: compiled.SchemaVersion, Message: "built-in package import",
			CreatedBy: "system", CreatedAt: now}
		if err := tx.Create(&revision).Error; err != nil {
			return err
		}
		for _, path := range paths {
			body := files[path]
			sum := sha256.Sum256(body)
			hash := hex.EncodeToString(sum[:])
			contentType := mime.TypeByExtension(filepath.Ext(path))
			if contentType == "" {
				contentType = "application/octet-stream"
			}
			blob := orm.WorkflowBlob{Hash: hash, Size: int64(len(body)), Mime: contentType,
				FileType: strings.TrimPrefix(filepath.Ext(path), "."), Content: body, CreatedAt: now}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&blob).Error; err != nil {
				return err
			}
			blobHash := hash
			if err := tx.Create(&orm.WorkflowRevisionEntry{RevisionID: revisionID, Path: path,
				EntryType: "file", BlobHash: &blobHash, Size: int64(len(body)), Mime: contentType,
				FileType: blob.FileType, Mode: 0o644}).Error; err != nil {
				return err
			}
		}
		return tx.Model(&resource).Updates(map[string]any{"head_revision_id": revision.ID,
			"version": revision.RevisionNo, "name": metadata.Name, "description": metadata.Description,
			"when_to_use": metadata.WhenToUse, "contains_scripts": hasScriptPath(paths),
			"status": "active", "updated_at": now}).Error
	})
}

func hasScriptPath(paths []string) bool {
	for _, path := range paths {
		if strings.HasPrefix(path, "scripts/") {
			return true
		}
	}
	return false
}
