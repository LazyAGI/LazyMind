package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/versionfs"
)

var yamlScalarPattern = func(key string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(key) + `:\s*["']?([^"'\n]+?)["']?\s*$`)
}

func yamlScalar(content, key string) string {
	m := yamlScalarPattern(key).FindStringSubmatch(content)
	if len(m) < 2 {
		return ""
	}
	value := strings.TrimSpace(m[1])
	if value != ">" && value != "|" {
		return value
	}
	lines := strings.Split(content[mustIndexAfterLine(content, m[0]):], "\n")
	var parts []string
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if len(parts) > 0 {
				parts = append(parts, "")
			}
			continue
		}
		if len(line) == len(strings.TrimLeft(line, " \t")) {
			break
		}
		parts = append(parts, strings.TrimSpace(line))
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}

func mustIndexAfterLine(content, matched string) int {
	i := strings.Index(content, matched)
	if i < 0 {
		return 0
	}
	i += len(matched)
	if j := strings.Index(content[i:], "\n"); j >= 0 {
		return i + j + 1
	}
	return len(content)
}

func pluginFiles(d orm.PluginDraft) (map[string][]byte, error) {
	if strings.TrimSpace(d.PluginYAMLContent) == "" || strings.TrimSpace(d.StateYAMLContent) == "" || strings.TrimSpace(d.ScenarioContent) == "" {
		return nil, fmt.Errorf("plugin.yaml, state.yml and scenario.md are required")
	}
	files := map[string][]byte{
		"plugin.yaml":          []byte(d.PluginYAMLContent),
		"scenario/state.yml":   []byte(d.StateYAMLContent),
		"scenario/scenario.md": []byte(d.ScenarioContent),
	}
	if strings.TrimSpace(d.ScriptsContent) != "" && strings.TrimSpace(d.ScriptsContent) != "{}" {
		var scripts map[string]string
		if err := json.Unmarshal([]byte(d.ScriptsContent), &scripts); err != nil {
			return nil, fmt.Errorf("invalid scripts_content: %w", err)
		}
		for p, body := range scripts {
			p = strings.TrimPrefix(filepath.ToSlash(filepath.Clean(p)), "./")
			if p == "" || strings.HasPrefix(p, "../") || filepath.IsAbs(p) {
				return nil, fmt.Errorf("invalid script path %q", p)
			}
			if !strings.HasPrefix(p, "scripts/") {
				p = "scripts/" + p
			}
			files[p] = []byte(body)
		}
	}
	return files, nil
}

func ownerScope(userID string) string {
	sum := sha256.Sum256([]byte(userID))
	return "u_" + hex.EncodeToString(sum[:6])
}

func pluginTreeHash(files map[string][]byte) string {
	entries := make(map[string]versionfs.Entry, len(files))
	for p, body := range files {
		sum := sha256.Sum256(body)
		entries[p] = versionfs.Entry{Path: p, EntryType: "file", BlobHash: hex.EncodeToString(sum[:]), Size: int64(len(body)), Mode: 420}
	}
	return versionfs.HashTree(entries)
}

// PublishPluginDraft commits the current draft as an immutable, content-addressed revision.
func PublishPluginDraft(w http.ResponseWriter, r *http.Request) {
	userID, draftID := common.UserID(r), common.PathVar(r, "draft_id")
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var d orm.PluginDraft
	if err := store.DB().Where("id = ? AND created_by = ?", draftID, userID).First(&d).Error; err != nil {
		common.ReplyErr(w, "not found", http.StatusNotFound)
		return
	}
	files, err := pluginFiles(d)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(files) > 3 {
		common.ReplyErr(w, "custom plugin scripts require the administrator publishing workflow", http.StatusForbidden)
		return
	}
	pid := extractPluginID(d.PluginYAMLContent)
	if pid == "" {
		common.ReplyErr(w, "plugin.yaml id required", http.StatusBadRequest)
		return
	}
	ref, scope := "user:"+userID+":"+pid, ownerScope(userID)
	var existing orm.PluginResource
	if err := store.DB().Where("plugin_ref=?", ref).First(&existing).Error; err == nil {
		baseRevisionID := d.BaseRevisionID
		if baseRevisionID == "" {
			baseRevisionID = existing.HeadRevisionID
		}
		var base orm.PluginRevision
		if baseRevisionID != "" && store.DB().Where("id=? AND plugin_resource_id=?", baseRevisionID, existing.ID).First(&base).Error == nil && pluginTreeHash(files) == base.TreeHash {
			common.ReplyErr(w, "plugin draft has no changes from its base revision", http.StatusConflict)
			return
		}
	}
	now := time.Now().UTC()
	var out orm.PluginResource
	err = store.DB().Transaction(func(tx *gorm.DB) error {
		var resource orm.PluginResource
		err := tx.Where("plugin_ref = ?", ref).First(&resource).Error
		if err == gorm.ErrRecordNotFound {
			resource = orm.PluginResource{ID: uuid.NewString(), PluginRef: ref, PluginID: pid, OwnerUserID: userID, OwnerScope: scope, SourceType: d.SourceType, RelativeRoot: "plugins/" + scope + "/" + pid, Name: d.Name, Status: "active", CreatedAt: now, UpdatedAt: now}
			if resource.SourceType == "" {
				resource.SourceType = "user"
			}
			if err := tx.Create(&resource).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		parent, next := resource.HeadRevisionID, resource.Version+1
		revID := uuid.NewString()
		paths := make([]string, 0, len(files))
		hashes := map[string]string{}
		for p, body := range files {
			paths = append(paths, p)
			sum := sha256.Sum256(body)
			hashes[p] = hex.EncodeToString(sum[:])
		}
		sort.Strings(paths)
		versionEntries := make(map[string]versionfs.Entry, len(paths))
		entries := make([]orm.PluginRevisionEntry, 0, len(paths))
		for _, p := range paths {
			body, hash := files[p], hashes[p]
			versionEntries[p] = versionfs.Entry{Path: p, EntryType: "file", BlobHash: hash, Size: int64(len(body)), Mode: 420}
			blob := orm.PluginBlob{Hash: hash, Size: int64(len(body)), Mime: mime.TypeByExtension(filepath.Ext(p)), FileType: strings.TrimPrefix(filepath.Ext(p), "."), Content: body, CreatedAt: now}
			if blob.Mime == "" {
				blob.Mime = "text/plain"
			}
			if blob.FileType == "" {
				blob.FileType = "text"
			}
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&blob).Error; err != nil {
				return err
			}
			h := hash
			entries = append(entries, orm.PluginRevisionEntry{RevisionID: revID, Path: p, EntryType: "file", BlobHash: &h, Size: int64(len(body)), Mime: blob.Mime, FileType: blob.FileType, Mode: 420})
		}
		treeHash := versionfs.HashTree(versionEntries)
		if err := tx.Create(&orm.PluginRevision{ID: revID, PluginResourceID: resource.ID, ParentRevisionID: parent, RevisionNo: next, TreeHash: treeHash, Message: "publish plugin draft", CreatedBy: userID, CreatedAt: now}).Error; err != nil {
			return err
		}
		if err := tx.Create(&entries).Error; err != nil {
			return err
		}
		updates := map[string]any{"head_revision_id": revID, "version": next, "updated_at": now, "name": d.Name, "description": yamlScalar(d.PluginYAMLContent, "description"), "when_to_use": yamlScalar(d.PluginYAMLContent, "when_to_use"), "contains_scripts": len(files) > 3}
		if err := tx.Model(&resource).Updates(updates).Error; err != nil {
			return err
		}
		if err := tx.Model(&d).Update("base_revision_id", revID).Error; err != nil {
			return err
		}
		if next == 1 {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&orm.UserPluginSetting{UserID: userID, PluginRef: ref, Enabled: false, UpdatedAt: now}).Error; err != nil {
				return err
			}
		}
		return tx.Where("id = ?", resource.ID).First(&out).Error
	})
	if err != nil {
		common.ReplyErr(w, "publish failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var setting orm.UserPluginSetting
	enabled := store.DB().Where("user_id=? AND plugin_ref=?", userID, out.PluginRef).First(&setting).Error == nil && setting.Enabled
	common.ReplyOK(w, map[string]any{"plugin_ref": out.PluginRef, "revision_id": out.HeadRevisionID, "revision_no": out.Version, "remote_root": "remote://" + out.RelativeRoot, "enabled": enabled})
}

func RollbackPlugin(w http.ResponseWriter, r *http.Request) {
	userID, ref := common.UserID(r), pluginRefPathVar(r)
	var body struct {
		RevisionID string `json:"revision_id"`
	}
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if json.NewDecoder(r.Body).Decode(&body) != nil || body.RevisionID == "" {
		common.ReplyErr(w, "revision_id required", http.StatusBadRequest)
		return
	}
	now := time.Now().UTC()
	var newRev orm.PluginRevision
	err := store.DB().Transaction(func(tx *gorm.DB) error {
		var p orm.PluginResource
		if err := tx.Where("plugin_ref=? AND owner_user_id=?", ref, userID).First(&p).Error; err != nil {
			return err
		}
		var target orm.PluginRevision
		if err := tx.Where("id=? AND plugin_resource_id=?", body.RevisionID, p.ID).First(&target).Error; err != nil {
			return err
		}
		var entries []orm.PluginRevisionEntry
		if err := tx.Where("revision_id=?", target.ID).Find(&entries).Error; err != nil {
			return err
		}
		newRev = orm.PluginRevision{ID: uuid.NewString(), PluginResourceID: p.ID, ParentRevisionID: p.HeadRevisionID, RevisionNo: p.Version + 1, TreeHash: target.TreeHash, Message: "rollback to " + target.ID, CreatedBy: userID, CreatedAt: now}
		if err := tx.Create(&newRev).Error; err != nil {
			return err
		}
		for i := range entries {
			entries[i].RevisionID = newRev.ID
		}
		if len(entries) > 0 {
			if err := tx.Create(&entries).Error; err != nil {
				return err
			}
		}
		return tx.Model(&p).Updates(map[string]any{"head_revision_id": newRev.ID, "version": newRev.RevisionNo, "updated_at": now}).Error
	})
	if err != nil {
		common.ReplyErr(w, "rollback failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	common.ReplyOK(w, map[string]any{"revision_id": newRev.ID, "revision_no": newRev.RevisionNo, "tree_hash": newRev.TreeHash})
}

func ArchivePlugin(w http.ResponseWriter, r *http.Request) {
	userID, ref := common.UserID(r), pluginRefPathVar(r)
	if userID == "" {
		common.ReplyErr(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	res := store.DB().Model(&orm.PluginResource{}).Where("plugin_ref=? AND owner_user_id=?", ref, userID).Updates(map[string]any{"status": "archived", "updated_at": time.Now().UTC()})
	if res.Error != nil {
		common.ReplyErr(w, res.Error.Error(), http.StatusInternalServerError)
		return
	}
	if res.RowsAffected == 0 {
		common.ReplyErr(w, "plugin not found", http.StatusNotFound)
		return
	}
	common.ReplyOK(w, nil)
}
