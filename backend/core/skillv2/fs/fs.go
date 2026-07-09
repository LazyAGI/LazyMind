package fs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/gorm"

	skillhttperr "lazymind/core/skillv2/httperr"
	skillrevision "lazymind/core/skillv2/revision"
	skillservice "lazymind/core/skillv2/service"
)

type LocalObjectStore = skillservice.LocalObjectStore
type BlobStore = skillservice.BlobStore

func NewLocalObjectStore(root string) *LocalObjectStore {
	return skillservice.NewLocalObjectStore(root)
}

func NewBlobStore(db *gorm.DB, objects *LocalObjectStore) *BlobStore {
	return skillservice.NewBlobStore(db, objects)
}

type DraftFSDeps struct {
	DB        *gorm.DB
	BlobStore *BlobStore
}

type HeadFSDeps struct {
	DB        *gorm.DB
	BlobStore *BlobStore
}

type DraftStoreDeps struct {
	DB *gorm.DB
}

type DraftHandlerDeps struct {
	DB *gorm.DB
}

type DraftFS struct {
	db        *gorm.DB
	blobStore *BlobStore
	clock     clock
}

type HeadFS struct {
	db *gorm.DB
}

type DraftStore struct {
	db *gorm.DB
}

type DraftHandler struct {
	store *DraftStore
}

func NewDraftFS(deps DraftFSDeps) *DraftFS {
	return &DraftFS{db: deps.DB, blobStore: deps.BlobStore, clock: systemClock{}}
}

func NewHeadFS(deps HeadFSDeps) *HeadFS {
	return &HeadFS{db: deps.DB}
}

func NewDraftStore(deps DraftStoreDeps) *DraftStore {
	return &DraftStore{db: deps.DB}
}

func NewDraftHandler(deps DraftHandlerDeps) *DraftHandler {
	return &DraftHandler{store: NewDraftStore(DraftStoreDeps{DB: deps.DB})}
}

type WriteTextRequest struct {
	SkillID              string
	Path                 string
	Content              string
	ExpectedDraftVersion int64
	UserID               string
}

type WriteFileRequest struct {
	SkillID              string
	Path                 string
	Data                 []byte
	ExpectedDraftVersion int64
	UserID               string
}

type MkdirRequest struct {
	SkillID              string
	Path                 string
	ExpectedDraftVersion int64
	UserID               string
}

type DeleteRequest struct {
	SkillID              string
	Path                 string
	Recursive            bool
	ExpectedDraftVersion int64
	UserID               string
}

type MoveRequest struct {
	SkillID              string
	From                 string
	To                   string
	ExpectedDraftVersion int64
	UserID               string
}

type TreeRequest struct {
	SkillID string
}

type WriteFileResponse struct {
	DraftVersion int64
	BlobHash     string
}

type DraftMutationResponse struct {
	DraftVersion int64
}

type TreeNode struct {
	Name     string     `json:"name"`
	Path     string     `json:"path"`
	Type     string     `json:"type"`
	Children []TreeNode `json:"children,omitempty"`
	BlobHash string     `json:"blob_hash,omitempty"`
	Size     int64      `json:"size,omitempty"`
	Mime     string     `json:"mime,omitempty"`
	FileType string     `json:"file_type,omitempty"`
	Binary   bool       `json:"binary,omitempty"`
}

type DraftState struct {
	HasUncommittedDraft bool
	DraftVersion        int64
	BaseRevisionID      string
	TaskID              string
	ConversationID      string
}

func (fs *DraftFS) WriteText(ctx context.Context, req WriteTextRequest) (WriteFileResponse, error) {
	return fs.WriteFile(ctx, WriteFileRequest{
		SkillID:              req.SkillID,
		Path:                 req.Path,
		Data:                 []byte(req.Content),
		ExpectedDraftVersion: req.ExpectedDraftVersion,
		UserID:               req.UserID,
	})
}

func (fs *DraftFS) WriteFile(ctx context.Context, req WriteFileRequest) (WriteFileResponse, error) {
	cleaned, err := cleanSkillPath(req.Path)
	if err != nil {
		return WriteFileResponse{}, err
	}
	var out WriteFileResponse
	err = fs.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		entries, err := draftEntriesForSkill(ctx, tx, req.SkillID)
		if err != nil {
			return err
		}
		if existing, ok := entries[cleaned]; ok && existing.EntryType == "dir" {
			return fmt.Errorf("cannot write file over directory: %s", cleaned)
		}
		for p, entry := range entries {
			if entry.EntryType == "file" && isAncestorPath(p, cleaned) {
				return fmt.Errorf("parent path is a file: %s", p)
			}
		}
		nextVersion, err := advanceDraftVersion(ctx, tx, req.SkillID, req.ExpectedDraftVersion, req.UserID, fs.clock.Now())
		if err != nil {
			return err
		}
		blob, err := fs.blobStore.Put(ctx, tx, cleaned, req.Data, fs.clock)
		if err != nil {
			return err
		}
		hash := blob.Hash
		if err := tx.Save(&skillDraftEntryRow{
			SkillID:   req.SkillID,
			Path:      cleaned,
			Op:        "upsert",
			EntryType: "file",
			BlobHash:  &hash,
			Size:      blob.Size,
			Mime:      blob.Mime,
			FileType:  blob.FileType,
			Binary:    blob.Binary,
			Mode:      0o644,
			UpdatedAt: fs.clock.Now(),
		}).Error; err != nil {
			return err
		}
		out = WriteFileResponse{DraftVersion: nextVersion, BlobHash: hash}
		return nil
	})
	return out, err
}

func (fs *DraftFS) Mkdir(ctx context.Context, req MkdirRequest) (DraftMutationResponse, error) {
	cleaned, err := cleanSkillPath(req.Path)
	if err != nil {
		return DraftMutationResponse{}, err
	}
	var out DraftMutationResponse
	err = fs.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		entries, err := draftEntriesForSkill(ctx, tx, req.SkillID)
		if err != nil {
			return err
		}
		if existing, ok := entries[cleaned]; ok && existing.EntryType == "file" {
			return fmt.Errorf("cannot create directory over file: %s", cleaned)
		}
		for p, entry := range entries {
			if entry.EntryType == "file" && isAncestorPath(p, cleaned) {
				return fmt.Errorf("parent path is a file: %s", p)
			}
		}
		nextVersion, err := advanceDraftVersion(ctx, tx, req.SkillID, req.ExpectedDraftVersion, req.UserID, fs.clock.Now())
		if err != nil {
			return err
		}
		if err := tx.Save(&skillDraftEntryRow{
			SkillID:   req.SkillID,
			Path:      cleaned,
			Op:        "upsert",
			EntryType: "dir",
			FileType:  "directory",
			Mode:      0o755,
			UpdatedAt: fs.clock.Now(),
		}).Error; err != nil {
			return err
		}
		out = DraftMutationResponse{DraftVersion: nextVersion}
		return nil
	})
	return out, err
}

func (fs *DraftFS) Delete(ctx context.Context, req DeleteRequest) (DraftMutationResponse, error) {
	cleaned, err := cleanSkillPath(req.Path)
	if err != nil {
		return DraftMutationResponse{}, err
	}
	var out DraftMutationResponse
	err = fs.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		entries, err := draftEntriesForSkill(ctx, tx, req.SkillID)
		if err != nil {
			return err
		}
		target, ok := entries[cleaned]
		if !ok {
			return fmt.Errorf("path not found: %s", cleaned)
		}
		if target.EntryType == "dir" && !req.Recursive {
			for p := range entries {
				if isDescendantPath(cleaned, p) {
					return fmt.Errorf("directory is not empty: %s", cleaned)
				}
			}
		}
		nextVersion, err := advanceDraftVersion(ctx, tx, req.SkillID, req.ExpectedDraftVersion, req.UserID, fs.clock.Now())
		if err != nil {
			return err
		}
		if target.FromDraft && !target.FromHead {
			if err := tx.Where("skill_id = ? AND (path = ? OR path LIKE ?)", req.SkillID, cleaned, cleaned+"/%").Delete(&skillDraftEntryRow{}).Error; err != nil {
				return err
			}
		} else {
			if req.Recursive {
				if err := tx.Where("skill_id = ? AND path LIKE ?", req.SkillID, cleaned+"/%").Delete(&skillDraftEntryRow{}).Error; err != nil {
					return err
				}
			}
			if err := tx.Save(&skillDraftEntryRow{
				SkillID:   req.SkillID,
				Path:      cleaned,
				Op:        "delete",
				UpdatedAt: fs.clock.Now(),
			}).Error; err != nil {
				return err
			}
		}
		out = DraftMutationResponse{DraftVersion: nextVersion}
		return nil
	})
	return out, err
}

func (fs *DraftFS) Move(ctx context.Context, req MoveRequest) (DraftMutationResponse, error) {
	from, err := cleanSkillPath(req.From)
	if err != nil {
		return DraftMutationResponse{}, err
	}
	to, err := cleanSkillPath(req.To)
	if err != nil {
		return DraftMutationResponse{}, err
	}
	if from == to {
		return DraftMutationResponse{}, fmt.Errorf("source and target path are the same: %s", from)
	}
	var out DraftMutationResponse
	err = fs.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		entries, err := draftEntriesForSkill(ctx, tx, req.SkillID)
		if err != nil {
			return err
		}
		target, ok := entries[from]
		if !ok {
			return fmt.Errorf("path not found: %s", from)
		}
		if _, exists := entries[to]; exists {
			return fmt.Errorf("target path already exists: %s", to)
		}
		if target.EntryType == "dir" && isDescendantPath(from, to) {
			return fmt.Errorf("cannot move directory into itself: %s", from)
		}
		for p, entry := range entries {
			if entry.EntryType == "file" && isAncestorPath(p, to) {
				return fmt.Errorf("parent path is a file: %s", p)
			}
		}
		moveSet := make(map[string]mergedEntry)
		for p, entry := range entries {
			if p == from || isDescendantPath(from, p) {
				newPath := to + strings.TrimPrefix(p, from)
				if _, exists := entries[newPath]; exists {
					return fmt.Errorf("target path already exists: %s", newPath)
				}
				entry.Path = newPath
				moveSet[p] = entry
			}
		}
		nextVersion, err := advanceDraftVersion(ctx, tx, req.SkillID, req.ExpectedDraftVersion, req.UserID, fs.clock.Now())
		if err != nil {
			return err
		}
		for oldPath, entry := range moveSet {
			if entry.FromHead {
				if err := tx.Save(&skillDraftEntryRow{
					SkillID:   req.SkillID,
					Path:      oldPath,
					Op:        "delete",
					UpdatedAt: fs.clock.Now(),
				}).Error; err != nil {
					return err
				}
			} else {
				if err := tx.Where("skill_id = ? AND path = ?", req.SkillID, oldPath).Delete(&skillDraftEntryRow{}).Error; err != nil {
					return err
				}
			}
			hash := entry.BlobHash
			if err := tx.Save(&skillDraftEntryRow{
				SkillID:   req.SkillID,
				Path:      entry.Path,
				Op:        "upsert",
				EntryType: entry.EntryType,
				BlobHash:  hash,
				Size:      entry.Size,
				Mime:      entry.Mime,
				FileType:  entry.FileType,
				Binary:    entry.Binary,
				Mode:      entry.Mode,
				UpdatedAt: fs.clock.Now(),
			}).Error; err != nil {
				return err
			}
		}
		out = DraftMutationResponse{DraftVersion: nextVersion}
		return nil
	})
	return out, err
}

func (fs *DraftFS) Tree(ctx context.Context, req TreeRequest) (TreeNode, error) {
	entries, err := draftEntriesForSkill(ctx, fs.db, req.SkillID)
	if err != nil {
		return TreeNode{}, err
	}
	return buildTree(entries), nil
}

func (fs *HeadFS) Tree(ctx context.Context, req TreeRequest) (TreeNode, error) {
	entries, err := headEntriesForSkill(ctx, fs.db, req.SkillID)
	if err != nil {
		return TreeNode{}, err
	}
	return buildTree(entries), nil
}

func (s *DraftStore) HasUncommittedDraft(ctx context.Context, skillID string) (DraftState, error) {
	var draft skillDraftRow
	err := s.db.WithContext(ctx).Where("skill_id = ?", skillID).Take(&draft).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return DraftState{}, err
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&skillDraftEntryRow{}).Where("skill_id = ?", skillID).Count(&count).Error; err != nil {
		return DraftState{}, err
	}
	state := DraftState{
		HasUncommittedDraft: count > 0,
		DraftVersion:        draft.Version,
		TaskID:              draft.TaskID,
	}
	if draft.BaseRevisionID != nil {
		state.BaseRevisionID = *draft.BaseRevisionID
	}
	if draft.ConversationID != nil {
		state.ConversationID = *draft.ConversationID
	}
	return state, nil
}

func (h *DraftHandler) DraftExists(w http.ResponseWriter, r *http.Request) {
	skillID := mux.Vars(r)["skillID"]
	state, err := h.store.HasUncommittedDraft(r.Context(), skillID)
	if err != nil {
		skillhttperr.Reply(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"data": map[string]any{
			"has_uncommitted_draft": state.HasUncommittedDraft,
			"draft_version":         state.DraftVersion,
			"base_revision_id":      state.BaseRevisionID,
			"task_id":               state.TaskID,
			"conversation_id":       state.ConversationID,
		},
	})
}

type RevisionServiceDeps struct {
	DB        *gorm.DB
	BlobStore *BlobStore
}

type RevisionService struct {
	db        *gorm.DB
	blobStore *BlobStore
}

type CommitDraftRequest struct {
	SkillID      string
	UserID       string
	DraftVersion int64
}

type CommitDraftResponse struct {
	RevisionID string
}

func NewRevisionService(deps RevisionServiceDeps) *RevisionService {
	return &RevisionService{db: deps.DB, blobStore: deps.BlobStore}
}

func (s *RevisionService) CommitDraft(ctx context.Context, req CommitDraftRequest) (CommitDraftResponse, error) {
	resp, err := skillrevision.NewService(skillrevision.ServiceDeps{
		DB:        s.db,
		BlobStore: revisionBlobStore{store: s.blobStore},
	}).CommitDraft(ctx, skillrevision.CommitDraftRequest{
		SkillID:      req.SkillID,
		UserID:       req.UserID,
		DraftVersion: req.DraftVersion,
	})
	if err != nil {
		return CommitDraftResponse{}, err
	}
	return CommitDraftResponse{RevisionID: resp.RevisionID}, nil
}

type revisionBlobStore struct {
	store *BlobStore
}

func (s revisionBlobStore) EnsureBlobs(ctx context.Context, tx *gorm.DB, hashes []string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func (s revisionBlobStore) DeleteBlob(ctx context.Context, tx *gorm.DB, hash string) error {
	if s.store == nil {
		return nil
	}
	return s.store.DeleteBlob(ctx, tx, hash)
}

func (s revisionBlobStore) DownloadURL(key string) string {
	if s.store == nil {
		return ""
	}
	return s.store.DownloadURL(key)
}

type clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type skillRow struct {
	ID             string  `gorm:"column:id;type:varchar(36);primaryKey"`
	HeadRevisionID *string `gorm:"column:head_revision_id;type:varchar(36)"`
}

func (skillRow) TableName() string { return "skills" }

type skillDraftRow struct {
	SkillID        string     `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	BaseRevisionID *string    `gorm:"column:base_revision_id;type:varchar(36)"`
	TaskID         string     `gorm:"column:task_id;type:text;not null;default:''"`
	ConversationID *string    `gorm:"column:conversation_id;type:varchar(36)"`
	UpdatedBy      *string    `gorm:"column:updated_by;type:varchar(36)"`
	Version        int64      `gorm:"column:version;not null;default:1"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;not null"`
	DraftUpdatedAt *time.Time `gorm:"column:draft_updated_at"`
}

func (skillDraftRow) TableName() string { return "skill_drafts" }

type skillDraftEntryRow struct {
	SkillID   string    `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	Path      string    `gorm:"column:path;type:text;primaryKey"`
	Op        string    `gorm:"column:op;type:text;not null"`
	EntryType string    `gorm:"column:entry_type;type:text"`
	BlobHash  *string   `gorm:"column:blob_hash;type:text"`
	Size      int64     `gorm:"column:size"`
	Mime      string    `gorm:"column:mime;type:text"`
	FileType  string    `gorm:"column:file_type;type:text"`
	Binary    bool      `gorm:"column:binary"`
	Mode      int       `gorm:"column:mode"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (skillDraftEntryRow) TableName() string { return "skill_draft_entries" }

type skillRevisionEntryRow struct {
	RevisionID string  `gorm:"column:revision_id;type:varchar(36);primaryKey"`
	Path       string  `gorm:"column:path;type:text;primaryKey"`
	EntryType  string  `gorm:"column:entry_type;type:text;not null"`
	BlobHash   *string `gorm:"column:blob_hash;type:text"`
	Size       int64   `gorm:"column:size"`
	Mime       string  `gorm:"column:mime;type:text"`
	FileType   string  `gorm:"column:file_type;type:text;not null;default:'unknown'"`
	Binary     bool    `gorm:"column:binary;not null;default:false"`
	Mode       int     `gorm:"column:mode;not null;default:420"`
}

func (skillRevisionEntryRow) TableName() string { return "skill_revision_entries" }

type mergedEntry struct {
	Path      string
	EntryType string
	BlobHash  *string
	Size      int64
	Mime      string
	FileType  string
	Binary    bool
	Mode      int
	FromHead  bool
	FromDraft bool
}

func advanceDraftVersion(ctx context.Context, tx *gorm.DB, skillID string, expectedVersion int64, userID string, now time.Time) (int64, error) {
	updates := map[string]any{
		"version":          gorm.Expr("version + 1"),
		"updated_at":       now,
		"draft_updated_at": now,
	}
	if userID != "" {
		updates["updated_by"] = userID
	}
	result := tx.WithContext(ctx).Model(&skillDraftRow{}).Where("skill_id = ? AND version = ?", skillID, expectedVersion).Updates(updates)
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected != 1 {
		return 0, fmt.Errorf("stale draft version")
	}
	return expectedVersion + 1, nil
}

func draftEntriesForSkill(ctx context.Context, db *gorm.DB, skillID string) (map[string]mergedEntry, error) {
	entries, err := headEntriesForSkill(ctx, db, skillID)
	if err != nil {
		return nil, err
	}
	var overlays []skillDraftEntryRow
	if err := db.WithContext(ctx).Where("skill_id = ?", skillID).Order("path ASC").Find(&overlays).Error; err != nil {
		return nil, err
	}
	for _, overlay := range overlays {
		if overlay.Op == "delete" {
			for p := range entries {
				if p == overlay.Path || isDescendantPath(overlay.Path, p) {
					delete(entries, p)
				}
			}
			continue
		}
		hash := overlay.BlobHash
		entries[overlay.Path] = mergedEntry{
			Path:      overlay.Path,
			EntryType: overlay.EntryType,
			BlobHash:  hash,
			Size:      overlay.Size,
			Mime:      overlay.Mime,
			FileType:  overlay.FileType,
			Binary:    overlay.Binary,
			Mode:      overlay.Mode,
			FromHead:  entries[overlay.Path].FromHead,
			FromDraft: true,
		}
	}
	return entries, nil
}

func headEntriesForSkill(ctx context.Context, db *gorm.DB, skillID string) (map[string]mergedEntry, error) {
	var skill skillRow
	if err := db.WithContext(ctx).Where("id = ?", skillID).Take(&skill).Error; err != nil {
		return nil, err
	}
	if skill.HeadRevisionID == nil {
		return nil, fmt.Errorf("skill has no head revision")
	}
	var rows []skillRevisionEntryRow
	if err := db.WithContext(ctx).Where("revision_id = ?", *skill.HeadRevisionID).Order("path ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	entries := make(map[string]mergedEntry, len(rows))
	for _, row := range rows {
		hash := row.BlobHash
		entries[row.Path] = mergedEntry{
			Path:      row.Path,
			EntryType: row.EntryType,
			BlobHash:  hash,
			Size:      row.Size,
			Mime:      row.Mime,
			FileType:  row.FileType,
			Binary:    row.Binary,
			Mode:      row.Mode,
			FromHead:  true,
		}
	}
	return entries, nil
}

func buildTree(entries map[string]mergedEntry) TreeNode {
	root := TreeNode{Name: "", Path: "", Type: "dir"}
	nodeByPath := map[string]*TreeNode{"": &root}
	for _, entry := range sortedEntries(entries) {
		parts := strings.Split(entry.Path, "/")
		parentPath := ""
		for i, part := range parts {
			currentPath := strings.Join(parts[:i+1], "/")
			if _, ok := nodeByPath[currentPath]; ok {
				parentPath = currentPath
				continue
			}
			nodeType := "dir"
			if i == len(parts)-1 {
				nodeType = entry.EntryType
			}
			node := TreeNode{Name: part, Path: currentPath, Type: nodeType}
			if i == len(parts)-1 {
				if entry.BlobHash != nil {
					node.BlobHash = *entry.BlobHash
				}
				node.Size = entry.Size
				node.Mime = entry.Mime
				node.FileType = entry.FileType
				node.Binary = entry.Binary
			}
			parent := nodeByPath[parentPath]
			parent.Children = append(parent.Children, node)
			nodeByPath[currentPath] = &parent.Children[len(parent.Children)-1]
			parentPath = currentPath
		}
	}
	sortTree(root.Children)
	return root
}

func sortedEntries(entries map[string]mergedEntry) []mergedEntry {
	out := make([]mergedEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

func sortTree(nodes []TreeNode) {
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Path < nodes[j].Path })
	for i := range nodes {
		sortTree(nodes[i].Children)
	}
}

func cleanSkillPath(name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, `\`) || strings.Contains(name, "//") {
		return "", fmt.Errorf("unsafe path %q", name)
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned != name || strings.HasPrefix(cleaned, "../") || cleaned == ".." {
		return "", fmt.Errorf("unsafe path %q", name)
	}
	for _, part := range strings.Split(cleaned, "/") {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("unsafe path %q", name)
		}
	}
	return cleaned, nil
}

func isAncestorPath(ancestor, child string) bool {
	return child != ancestor && strings.HasPrefix(child, ancestor+"/")
}

func isDescendantPath(parent, child string) bool {
	return child != parent && strings.HasPrefix(child, parent+"/")
}
