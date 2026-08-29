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
	for _, candidate := range []string{"workflows", "../../workflows", "/app/workflows"} {
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
		return fmt.Errorf("built-in Workflow package directory not found")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	activeRefs := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		ref, err := seedBuiltinWorkflow(ctx, db, filepath.Join(root, entry.Name()))
		if err != nil {
			return fmt.Errorf("seed builtin Workflow %s: %w", entry.Name(), err)
		}
		if ref != "" {
			activeRefs = append(activeRefs, ref)
		}
	}
	return reconcileBuiltinWorkflowCatalog(ctx, db, activeRefs)
}

func seedBuiltinWorkflow(ctx context.Context, db *gorm.DB, root string) (string, error) {
	files, err := readBuiltinPackageFiles(root)
	if err != nil {
		return "", err
	}
	workflowYAML, stateYAML := files["workflow.yaml"], files["scenario/state.yml"]
	if len(workflowYAML) == 0 || len(stateYAML) == 0 {
		return "", nil
	}
	compiled := graphengine.Compile(string(workflowYAML), string(stateYAML),
		string(files["scenario/scenario.md"]), graphengine.ProfilePublish)
	if !compiled.Valid || compiled.Graph == nil {
		return "", fmt.Errorf("invalid package: %v", compiled.Diagnostics)
	}
	var metadata struct {
		ID          string `yaml:"id"`
		Name        string `yaml:"name"`
		Description string `yaml:"description"`
		WhenToUse   string `yaml:"when_to_use"`
	}
	if err := yaml.Unmarshal(workflowYAML, &metadata); err != nil || metadata.ID == "" {
		return "", fmt.Errorf("workflow id is required")
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
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
		// Package bytes alone do not identify an executable revision: compiler
		// upgrades can change the typed graph without changing workflow.yaml. Reuse
		// only the exact package + compiled graph pair, otherwise publish one new
		// immutable built-in revision.
		if tx.Where("plugin_resource_id = ? AND tree_hash = ? AND graph_hash = ?",
			resource.ID, treeHash, compiled.GraphHash).First(&existing).Error == nil {
			return tx.Model(&resource).Updates(map[string]any{"head_revision_id": existing.ID,
				"version": existing.RevisionNo, "plugin_id": metadata.ID, "owner_user_id": "", // workflow-naming: persistence
				"owner_scope": "builtin", "source_type": "builtin",
				"relative_root": "workflows/builtin/" + metadata.ID, "name": metadata.Name,
				"description": metadata.Description, "when_to_use": metadata.WhenToUse,
				"contains_scripts": hasScriptPath(paths), "status": "active", "updated_at": now}).Error
		}
		revisionID := uuid.NewString()
		var maximumRevisionNo int64
		if err := tx.Model(&orm.WorkflowRevision{}).
			Where("plugin_resource_id = ?", resource.ID). // workflow-naming: persistence
			Select("COALESCE(MAX(revision_no), 0)").Scan(&maximumRevisionNo).Error; err != nil {
			return err
		}
		revision := orm.WorkflowRevision{ID: revisionID, WorkflowResourceID: resource.ID,
			ParentRevisionID: resource.HeadRevisionID, RevisionNo: maximumRevisionNo + 1,
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
	return ref, err
}

func readBuiltinPackageFiles(root string) (map[string][]byte, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path != root && ignoredBuiltinPackageDir(entry.Name()) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			// Windows directory junctions can be reported as non-directories by
			// WalkDir. Ignore the entry before attempting to read it as a file.
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if ignoredBuiltinPackageFile(entry.Name()) {
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
		return nil, err
	}
	return files, nil
}

func ignoredBuiltinPackageDir(name string) bool {
	return name == "__pycache__" || name == "node_modules" || strings.HasPrefix(name, ".")
}

func ignoredBuiltinPackageFile(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".pyc") ||
		strings.HasSuffix(name, ".pyo")
}

// reconcileBuiltinWorkflowCatalog makes the repository directory the
// authoritative active catalog. Removed or renamed packages are archived
// instead of deleted so pinned revisions and historical sessions remain
// reproducible, but stale identities can no longer appear in discovery.
func reconcileBuiltinWorkflowCatalog(ctx context.Context, db *gorm.DB, activeRefs []string) error {
	query := db.WithContext(ctx).Model(&orm.WorkflowResource{}).
		Where("source_type = 'builtin' AND owner_user_id = '' AND status = 'active'")
	if len(activeRefs) > 0 {
		query = query.Where("plugin_ref NOT IN ?", activeRefs)
	}
	return query.Updates(map[string]any{
		"status":     "archived",
		"updated_at": time.Now().UTC(),
	}).Error
}

func hasScriptPath(paths []string) bool {
	for _, path := range paths {
		if strings.HasPrefix(path, "scripts/") {
			return true
		}
	}
	return false
}
