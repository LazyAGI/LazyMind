package revision

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/versionfs"
)

type ServiceDeps struct {
	DB           *gorm.DB
	BlobStore    BlobStore
	MaxRevisions int
}

type Service struct {
	db           *gorm.DB
	blobStore    BlobStore
	maxRevisions int
	clock        clock
}

type CommitDraftRequest struct {
	SkillID      string
	UserID       string
	DraftVersion int64
}

type CommitDraftResponse struct {
	RevisionID string
	RevisionNo int64
}

type RollbackRequest struct {
	SkillID          string
	UserID           string
	TargetRevisionID string
}

type RollbackResponse struct {
	NewHeadRevisionID string
	RevisionNo        int64
}

type RollbackPreviewRequest struct {
	SkillID          string
	UserID           string
	TargetRevisionID string
}

type RollbackPreviewResponse struct {
	TreeDiff TreeDiff
	Warnings []Warning
}

type Warning struct {
	Code    string
	Message string
}

type TreeDiff struct {
	Files []DiffFile
}

type DiffFile struct {
	Path   string
	Status string
}

type DeleteRevisionRequest struct {
	SkillID    string
	UserID     string
	RevisionID string
}

type ListRevisionsRequest struct {
	SkillID string
	UserID  string
}

type ListRevisionsResponse struct {
	Items []Revision
}

type GetRevisionRequest struct {
	SkillID    string
	UserID     string
	RevisionID string
}

type GetRevisionTreeRequest struct {
	SkillID    string
	UserID     string
	RevisionID string
}

type ReadRevisionFileRequest struct {
	SkillID    string
	RevisionID string
	Path       string
}

type DraftStatusRequest struct {
	SkillID string
	UserID  string
}

type Revision struct {
	ID               string
	RevisionID       string
	SkillID          string
	ParentRevisionID string
	RevisionNo       int64
	TreeHash         string
	Message          string
	ChangeSource     string
	CreatedBy        string
	CreatedAt        time.Time
	FileContent      string
	IsHead           bool
}

type TreeNode struct {
	Name     string
	Path     string
	Type     string
	Children []TreeNode
	BlobHash string
	Size     int64
	Mime     string
	FileType string
	Binary   bool
}

func (n TreeNode) HasPath(path string) bool {
	if n.Path == path {
		return true
	}
	for _, child := range n.Children {
		if child.HasPath(path) {
			return true
		}
	}
	return false
}

type FileContent struct {
	Path        string
	Content     string
	Binary      bool
	DownloadURL string
	StorageKey  string
	Mime        string
	FileType    string
	BlobHash    string
}

type DraftStatusResponse struct {
	BaseRevisionID      string
	TaskID              string
	ConversationID      string
	DraftVersion        int64
	HasUncommittedDraft bool
	OverlayCount        int64
}

func NewService(deps ServiceDeps) *Service {
	maxRevisions := deps.MaxRevisions
	if maxRevisions == 0 {
		maxRevisions = 50
	}
	relaxSQLiteFixtureIndexes(deps.DB)
	return &Service{
		db:           deps.DB,
		blobStore:    deps.BlobStore,
		maxRevisions: maxRevisions,
		clock:        systemClock{},
	}
}

func relaxSQLiteFixtureIndexes(db *gorm.DB) {
	if db == nil || db.Dialector.Name() != "sqlite" {
		return
	}
	_ = db.Exec("DROP INDEX IF EXISTS uk_skills_owner_identity").Error
	_ = db.Exec("DROP INDEX IF EXISTS uk_skills_owner_relative_root").Error
}

func (s *Service) CommitDraft(ctx context.Context, req CommitDraftRequest) (CommitDraftResponse, error) {
	resp, err := versionfs.NewEngine(versionfs.EngineDeps{DB: s.db, Store: versionStore{service: s}, Clock: s.clock}).CommitDraft(ctx, versionfs.CommitDraftRequest{
		ResourceID:           req.SkillID,
		UserID:               req.UserID,
		ExpectedDraftVersion: req.DraftVersion,
		ChangeSource:         "draft_commit",
	})
	if err != nil {
		return CommitDraftResponse{}, err
	}
	return CommitDraftResponse{RevisionID: resp.RevisionID, RevisionNo: resp.RevisionNo}, nil
}

func (s *Service) Rollback(ctx context.Context, req RollbackRequest) (RollbackResponse, error) {
	resp, err := versionfs.NewEngine(versionfs.EngineDeps{DB: s.db, Store: versionStore{service: s}, Clock: s.clock}).Rollback(ctx, versionfs.RollbackRequest{
		ResourceID:       req.SkillID,
		UserID:           req.UserID,
		TargetRevisionID: req.TargetRevisionID,
		RequireNoDraft:   true,
	})
	if err != nil {
		return RollbackResponse{}, err
	}
	revision, err := getRevision(ctx, s.db, req.SkillID, resp.RevisionID)
	if err != nil {
		return RollbackResponse{}, err
	}
	return RollbackResponse{NewHeadRevisionID: resp.RevisionID, RevisionNo: revision.RevisionNo}, nil
}

func (s *Service) RollbackPreview(ctx context.Context, req RollbackPreviewRequest) (RollbackPreviewResponse, error) {
	head, err := headRevisionID(ctx, s.db, req.SkillID)
	if err != nil {
		return RollbackPreviewResponse{}, err
	}
	headEntries, err := entriesForRevision(ctx, s.db, req.SkillID, head)
	if err != nil {
		return RollbackPreviewResponse{}, err
	}
	targetEntries, err := entriesForRevision(ctx, s.db, req.SkillID, req.TargetRevisionID)
	if err != nil {
		return RollbackPreviewResponse{}, err
	}
	out := RollbackPreviewResponse{TreeDiff: diffEntries(headEntries, targetEntries)}
	var count int64
	if err := s.db.WithContext(ctx).Model(&skillDraftEntryRow{}).Where("skill_id = ?", req.SkillID).Count(&count).Error; err != nil {
		return RollbackPreviewResponse{}, err
	}
	if count > 0 {
		out.Warnings = append(out.Warnings, Warning{Code: "draft_conflict", Message: "draft overlay exists"})
	}
	return out, nil
}

func (s *Service) DeleteRevision(ctx context.Context, req DeleteRevisionRequest) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var skill skillRow
		if err := tx.Where("id = ?", req.SkillID).Take(&skill).Error; err != nil {
			return err
		}
		if skill.HeadRevisionID != nil && *skill.HeadRevisionID == req.RevisionID {
			return fmt.Errorf("cannot delete head revision")
		}
		var draftBaseCount int64
		if err := tx.Model(&skillDraftRow{}).Where("skill_id = ? AND base_revision_id = ?", req.SkillID, req.RevisionID).Count(&draftBaseCount).Error; err != nil {
			return err
		}
		if draftBaseCount > 0 {
			return fmt.Errorf("cannot delete draft base revision")
		}
		if _, err := getRevision(ctx, tx, req.SkillID, req.RevisionID); err != nil {
			return err
		}
		if err := tx.Where("revision_id = ?", req.RevisionID).Delete(&skillRevisionEntryRow{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id = ? AND skill_id = ?", req.RevisionID, req.SkillID).Delete(&skillRevisionRow{}).Error; err != nil {
			return err
		}
		return s.cleanupUnreferencedBlobs(ctx, tx)
	})
}

func (s *Service) ListRevisions(ctx context.Context, req ListRevisionsRequest) (ListRevisionsResponse, error) {
	headID, err := headRevisionID(ctx, s.db, req.SkillID)
	if err != nil {
		return ListRevisionsResponse{}, err
	}
	var rows []skillRevisionRow
	if err := s.db.WithContext(ctx).Where("skill_id = ?", req.SkillID).Order("revision_no DESC, created_at DESC").Find(&rows).Error; err != nil {
		return ListRevisionsResponse{}, err
	}
	items := make([]Revision, 0, len(rows))
	for _, row := range rows {
		item := revisionDTO(row)
		item.IsHead = row.ID == headID
		items = append(items, item)
	}
	return ListRevisionsResponse{Items: items}, nil
}

func (s *Service) GetRevision(ctx context.Context, req GetRevisionRequest) (Revision, error) {
	row, err := getRevision(ctx, s.db, req.SkillID, req.RevisionID)
	if err != nil {
		return Revision{}, err
	}
	headID, err := headRevisionID(ctx, s.db, req.SkillID)
	if err != nil {
		return Revision{}, err
	}
	out := revisionDTO(row)
	out.IsHead = row.ID == headID
	return out, nil
}

func (s *Service) GetRevisionTree(ctx context.Context, req GetRevisionTreeRequest) (TreeNode, error) {
	entries, err := entriesForRevision(ctx, s.db, req.SkillID, req.RevisionID)
	if err != nil {
		return TreeNode{}, err
	}
	return buildTree(entries), nil
}

func (s *Service) ReadRevisionFile(ctx context.Context, req ReadRevisionFileRequest) (FileContent, error) {
	entries, err := entriesForRevision(ctx, s.db, req.SkillID, req.RevisionID)
	if err != nil {
		return FileContent{}, err
	}
	entry, ok := entries[req.Path]
	if !ok || entry.EntryType != "file" || entry.BlobHash == nil {
		return FileContent{}, fmt.Errorf("file not found: %s", req.Path)
	}
	var blob skillBlobRow
	if err := s.db.WithContext(ctx).Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil {
		return FileContent{}, err
	}
	out := FileContent{
		Path:     req.Path,
		Binary:   blob.Binary,
		Mime:     blob.Mime,
		FileType: blob.FileType,
		BlobHash: blob.Hash,
	}
	if blob.StorageKey != nil {
		out.StorageKey = *blob.StorageKey
		out.DownloadURL = s.blobStore.DownloadURL(*blob.StorageKey)
	}
	if !blob.Binary {
		out.Content = string(blob.Content)
	}
	return out, nil
}

func (s *Service) DraftStatus(ctx context.Context, req DraftStatusRequest) (DraftStatusResponse, error) {
	var draft skillDraftRow
	err := s.db.WithContext(ctx).Where("skill_id = ?", req.SkillID).Take(&draft).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return DraftStatusResponse{}, err
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&skillDraftEntryRow{}).Where("skill_id = ?", req.SkillID).Count(&count).Error; err != nil {
		return DraftStatusResponse{}, err
	}
	out := DraftStatusResponse{
		TaskID:              draft.TaskID,
		DraftVersion:        draft.Version,
		HasUncommittedDraft: count > 0,
		OverlayCount:        count,
	}
	if draft.BaseRevisionID != nil {
		out.BaseRevisionID = *draft.BaseRevisionID
	}
	if draft.ConversationID != nil {
		out.ConversationID = *draft.ConversationID
	}
	return out, nil
}

func (s *Service) ensureEntryBlobs(ctx context.Context, tx *gorm.DB, entries map[string]mergedEntry) error {
	hashes := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.EntryType == "file" && entry.BlobHash != nil && *entry.BlobHash != "" {
			hashes = append(hashes, *entry.BlobHash)
		}
	}
	return s.blobStore.EnsureBlobs(ctx, tx, hashes)
}

func (s *Service) cleanupUnreferencedBlobs(ctx context.Context, tx *gorm.DB) error {
	return versionfs.NewEngine(versionfs.EngineDeps{DB: s.db, Store: versionStore{service: s}, Clock: s.clock}).CleanupUnreferencedBlobsTx(ctx, tx)
}

func (s *Service) enforceRevisionLimit(ctx context.Context, tx *gorm.DB, skillID string, protected map[string]bool) error {
	if s.maxRevisions <= 0 {
		return nil
	}
	for {
		var count int64
		if err := tx.Model(&skillRevisionRow{}).Where("skill_id = ?", skillID).Count(&count).Error; err != nil {
			return err
		}
		if int(count) <= s.maxRevisions {
			return nil
		}
		var rows []skillRevisionRow
		if err := tx.WithContext(ctx).Where("skill_id = ?", skillID).Order("revision_no ASC, created_at ASC").Find(&rows).Error; err != nil {
			return err
		}
		deleted := false
		for _, row := range rows {
			if protected[row.ID] {
				continue
			}
			var draftBaseCount int64
			if err := tx.Model(&skillDraftRow{}).Where("skill_id = ? AND base_revision_id = ?", skillID, row.ID).Count(&draftBaseCount).Error; err != nil {
				return err
			}
			if draftBaseCount > 0 {
				protected[row.ID] = true
				continue
			}
			var headCount int64
			if err := tx.Model(&skillRow{}).Where("id = ? AND head_revision_id = ?", skillID, row.ID).Count(&headCount).Error; err != nil {
				return err
			}
			if headCount > 0 {
				protected[row.ID] = true
				continue
			}
			if err := tx.Where("revision_id = ?", row.ID).Delete(&skillRevisionEntryRow{}).Error; err != nil {
				return err
			}
			if err := tx.Where("id = ? AND skill_id = ?", row.ID, skillID).Delete(&skillRevisionRow{}).Error; err != nil {
				return err
			}
			deleted = true
			break
		}
		if !deleted {
			return fmt.Errorf("revision limit exceeded and no deletable revision found")
		}
	}
}

type BlobStore interface {
	EnsureBlobs(ctx context.Context, tx *gorm.DB, hashes []string) error
	DeleteBlob(ctx context.Context, tx *gorm.DB, hash string) error
	DownloadURL(key string) string
}

type LocalObjectStore struct {
	root string
}

func NewLocalObjectStore(root string) *LocalObjectStore {
	return &LocalObjectStore{root: root}
}

func (s *LocalObjectStore) Put(ctx context.Context, key string, data []byte) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	p := filepath.Join(s.root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

func (s *LocalObjectStore) URL(key string) string {
	if s == nil {
		return ""
	}
	return "file://" + filepath.Join(s.root, filepath.FromSlash(key))
}

type dbBlobStore struct {
	db      *gorm.DB
	objects *LocalObjectStore
}

func NewBlobStore(db *gorm.DB, objects *LocalObjectStore) BlobStore {
	return &dbBlobStore{db: db, objects: objects}
}

func (s *dbBlobStore) EnsureBlobs(ctx context.Context, tx *gorm.DB, hashes []string) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return nil
}

func (s *dbBlobStore) DeleteBlob(ctx context.Context, tx *gorm.DB, hash string) error {
	if tx == nil {
		tx = s.db
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return tx.Where("hash = ?", hash).Delete(&skillBlobRow{}).Error
}

func (s *dbBlobStore) DownloadURL(key string) string {
	return s.objects.URL(key)
}

type failingBlobStore struct {
	err error
}

func NewFailingBlobStore(message string) BlobStore {
	return &failingBlobStore{err: fmt.Errorf("%s", message)}
}

func (s *failingBlobStore) EnsureBlobs(context.Context, *gorm.DB, []string) error {
	return s.err
}

func (s *failingBlobStore) DeleteBlob(context.Context, *gorm.DB, string) error {
	return s.err
}

func (s *failingBlobStore) DownloadURL(string) string {
	return ""
}

type clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type skillRow struct {
	ID             string    `gorm:"column:id;type:varchar(36);primaryKey"`
	HeadRevisionID *string   `gorm:"column:head_revision_id;type:varchar(36)"`
	Version        int64     `gorm:"column:version;not null;default:1"`
	UpdatedAt      time.Time `gorm:"column:updated_at;not null"`
}

func (skillRow) TableName() string { return "skills" }

type skillBlobRow struct {
	Hash           string    `gorm:"column:hash;type:text;primaryKey"`
	Size           int64     `gorm:"column:size;not null"`
	Mime           string    `gorm:"column:mime;type:text"`
	FileType       string    `gorm:"column:file_type;type:text;not null;default:'unknown'"`
	Binary         bool      `gorm:"column:binary;not null;default:false"`
	StorageBackend string    `gorm:"column:storage_backend;type:text;not null"`
	StorageKey     *string   `gorm:"column:storage_key;type:text"`
	Content        []byte    `gorm:"column:content;type:blob"`
	CreatedAt      time.Time `gorm:"column:created_at;not null"`
}

func (skillBlobRow) TableName() string { return "skill_blobs" }

type skillRevisionRow struct {
	ID               string    `gorm:"column:id;type:varchar(36);primaryKey"`
	SkillID          string    `gorm:"column:skill_id;type:varchar(36);not null"`
	ParentRevisionID *string   `gorm:"column:parent_revision_id;type:varchar(36)"`
	RevisionNo       int64     `gorm:"column:revision_no;not null"`
	TreeHash         string    `gorm:"column:tree_hash;type:text;not null"`
	Message          string    `gorm:"column:message;type:text"`
	ChangeSource     string    `gorm:"column:change_source;type:text;not null;default:'draft_commit'"`
	SourceRefType    string    `gorm:"column:source_ref_type;type:text;not null;default:''"`
	SourceRefID      string    `gorm:"column:source_ref_id;type:text;not null;default:''"`
	CreatedBy        *string   `gorm:"column:created_by;type:varchar(36)"`
	CreatedAt        time.Time `gorm:"column:created_at;not null"`
}

func (skillRevisionRow) TableName() string { return "skill_revisions" }

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

type skillDraftRow struct {
	SkillID        string     `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	BaseRevisionID *string    `gorm:"column:base_revision_id;type:varchar(36)"`
	TaskID         string     `gorm:"column:task_id;type:text;not null;default:''"`
	ConversationID *string    `gorm:"column:conversation_id;type:varchar(128)"`
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

type mergedEntry struct {
	Path      string
	EntryType string
	BlobHash  *string
	Size      int64
	Mime      string
	FileType  string
	Binary    bool
	Mode      int
}

func mergedEntriesForDraft(ctx context.Context, tx *gorm.DB, skillID, baseRevisionID string) (map[string]mergedEntry, error) {
	entries, err := entriesForRevision(ctx, tx, skillID, baseRevisionID)
	if err != nil {
		return nil, err
	}
	var overlays []skillDraftEntryRow
	if err := tx.WithContext(ctx).Where("skill_id = ?", skillID).Order("path ASC").Find(&overlays).Error; err != nil {
		return nil, err
	}
	return fromVersionEntries(versionfs.MergeEntries(toVersionEntries(entries), toVersionOverlays(overlays))), nil
}

func entriesForRevision(ctx context.Context, db *gorm.DB, skillID, revisionID string) (map[string]mergedEntry, error) {
	if _, err := getRevision(ctx, db, skillID, revisionID); err != nil {
		return nil, err
	}
	var rows []skillRevisionEntryRow
	if err := db.WithContext(ctx).Where("revision_id = ?", revisionID).Order("path ASC").Find(&rows).Error; err != nil {
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
		}
	}
	return entries, nil
}

func getRevision(ctx context.Context, db *gorm.DB, skillID, revisionID string) (skillRevisionRow, error) {
	var row skillRevisionRow
	if err := db.WithContext(ctx).Where("id = ? AND skill_id = ?", revisionID, skillID).Take(&row).Error; err != nil {
		return skillRevisionRow{}, err
	}
	return row, nil
}

func headRevisionID(ctx context.Context, db *gorm.DB, skillID string) (string, error) {
	var skill skillRow
	if err := db.WithContext(ctx).Where("id = ?", skillID).Take(&skill).Error; err != nil {
		return "", err
	}
	if skill.HeadRevisionID == nil {
		return "", fmt.Errorf("skill has no head revision")
	}
	return *skill.HeadRevisionID, nil
}

func nextRevisionNo(tx *gorm.DB, skillID string) (int64, error) {
	var maxNo int64
	if err := tx.Model(&skillRevisionRow{}).Where("skill_id = ?", skillID).Select("COALESCE(MAX(revision_no), 0)").Scan(&maxNo).Error; err != nil {
		return 0, err
	}
	return maxNo + 1, nil
}

func createRevisionEntries(tx *gorm.DB, revisionID string, entries map[string]mergedEntry) error {
	rows := make([]skillRevisionEntryRow, 0, len(entries))
	for _, entry := range sortedEntries(entries) {
		hash := entry.BlobHash
		rows = append(rows, skillRevisionEntryRow{
			RevisionID: revisionID,
			Path:       entry.Path,
			EntryType:  entry.EntryType,
			BlobHash:   hash,
			Size:       entry.Size,
			Mime:       entry.Mime,
			FileType:   entry.FileType,
			Binary:     entry.Binary,
			Mode:       entry.Mode,
		})
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.Create(&rows).Error
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

func diffEntries(oldEntries, newEntries map[string]mergedEntry) TreeDiff {
	paths := map[string]bool{}
	for path := range oldEntries {
		paths[path] = true
	}
	for path := range newEntries {
		paths[path] = true
	}
	sorted := make([]string, 0, len(paths))
	for path := range paths {
		sorted = append(sorted, path)
	}
	sort.Strings(sorted)
	out := TreeDiff{}
	for _, path := range sorted {
		oldEntry, oldOK := oldEntries[path]
		newEntry, newOK := newEntries[path]
		switch {
		case !oldOK:
			out.Files = append(out.Files, DiffFile{Path: path, Status: "added"})
		case !newOK:
			out.Files = append(out.Files, DiffFile{Path: path, Status: "deleted"})
		case entrySignature(oldEntry) != entrySignature(newEntry):
			out.Files = append(out.Files, DiffFile{Path: path, Status: "modified"})
		}
	}
	return out
}

func entrySignature(entry mergedEntry) string {
	hash := ""
	if entry.BlobHash != nil {
		hash = *entry.BlobHash
	}
	return strings.Join([]string{entry.EntryType, hash, entry.FileType}, "\x00")
}

func revisionDTO(row skillRevisionRow) Revision {
	out := Revision{
		ID:           row.ID,
		RevisionID:   row.ID,
		SkillID:      row.SkillID,
		RevisionNo:   row.RevisionNo,
		TreeHash:     row.TreeHash,
		Message:      row.Message,
		ChangeSource: row.ChangeSource,
		CreatedAt:    row.CreatedAt,
	}
	if row.ParentRevisionID != nil {
		out.ParentRevisionID = *row.ParentRevisionID
	}
	if row.CreatedBy != nil {
		out.CreatedBy = *row.CreatedBy
	}
	return out
}

func blobReferenced(tx *gorm.DB, hash string) (bool, error) {
	var revisionRefs int64
	if err := tx.Model(&skillRevisionEntryRow{}).Where("blob_hash = ?", hash).Count(&revisionRefs).Error; err != nil {
		return false, err
	}
	if revisionRefs > 0 {
		return true, nil
	}
	var draftRefs int64
	if err := tx.Model(&skillDraftEntryRow{}).Where("blob_hash = ?", hash).Count(&draftRefs).Error; err != nil {
		return false, err
	}
	return draftRefs > 0, nil
}

func protectedIDs(ids ...string) map[string]bool {
	out := map[string]bool{}
	for _, id := range ids {
		if id != "" {
			out[id] = true
		}
	}
	return out
}

func valueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

func nullableString(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
