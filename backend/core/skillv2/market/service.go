package market

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	skillsearch "lazymind/core/skillv2/search"
)

type ServiceDeps struct {
	DB        *gorm.DB
	BlobStore *BlobStore
}

type AdminServiceDeps = ServiceDeps

type Service struct {
	db        *gorm.DB
	blobStore *BlobStore
}

type AdminService = Service

func NewService(deps ServiceDeps) *Service {
	return &Service{db: deps.DB, blobStore: deps.BlobStore}
}

func NewAdminService(deps AdminServiceDeps) *AdminService {
	return (*AdminService)(NewService(ServiceDeps(deps)))
}

type InstallRequest struct {
	MarketItemID string
	UserID       string
	UserName     string
}

type InstallResponse struct {
	SkillID string
}

type GetInstalledTreeRequest struct {
	SkillID string
	UserID  string
}

type PublishRequest struct {
	AdminUserID string
	Name        string
	Category    string
	Source      SourceInput
}

type SourceInput struct {
	Type       string
	UploadID   string
	StoredPath string
	Filename   string
}

type PublishResponse struct {
	MarketItemID  string
	SourceSkillID string
}

type EditRequest struct {
	AdminUserID  string
	MarketItemID string
	VersionNote  string
}

type EditResponse struct {
	MarketItemID string
}

type UnpublishRequest struct {
	AdminUserID  string
	MarketItemID string
}

type UnpublishResponse struct {
	MarketItemID string
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

func (s *Service) Install(ctx context.Context, req InstallRequest) (InstallResponse, error) {
	var out InstallResponse
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var item skillMarketItemRow
		if err := tx.Where("id = ? AND status = ?", req.MarketItemID, "published").Take(&item).Error; err != nil {
			return err
		}
		now := time.Now()
		installedSkillID, err := existingInstalledSkillID(ctx, tx, item.ID, req.UserID)
		if err != nil {
			return err
		}
		if installedSkillID != "" {
			out.SkillID = installedSkillID
			return nil
		}
		sourceOwned, err := sourceSkillOwnedByUser(ctx, tx, item.SourceSkillID, req.UserID)
		if err != nil {
			return err
		}
		if sourceOwned {
			if err := recordMarketInstall(ctx, tx, item.ID, req.UserID, item.SourceSkillID, now); err != nil {
				return err
			}
			out.SkillID = item.SourceSkillID
			return nil
		}
		skillID, _, err := copyHeadRevision(ctx, tx, item.SourceSkillID, req.UserID, req.UserName, "market_install", req.UserID)
		if err != nil {
			return err
		}
		if err := skillsearch.RebuildSkillTx(ctx, tx, skillID, now); err != nil {
			return err
		}
		if err := recordMarketInstall(ctx, tx, item.ID, req.UserID, skillID, now); err != nil {
			return err
		}
		out.SkillID = skillID
		return nil
	})
	return out, err
}

func existingInstalledSkillID(ctx context.Context, tx *gorm.DB, marketItemID, userID string) (string, error) {
	var row skillMarketInstallRow
	result := tx.WithContext(ctx).
		Table("skill_market_installs AS installs").
		Select("installs.*").
		Joins("JOIN skills AS skills ON skills.id = installs.skill_id AND skills.owner_user_id = installs.user_id AND skills.deleted_at IS NULL").
		Where("installs.market_item_id = ? AND installs.user_id = ?", marketItemID, userID).
		Limit(1).
		Find(&row)
	if result.Error != nil {
		return "", result.Error
	}
	if result.RowsAffected == 0 {
		return "", nil
	}
	return row.SkillID, nil
}

func sourceSkillOwnedByUser(ctx context.Context, tx *gorm.DB, skillID, userID string) (bool, error) {
	var source skillRow
	result := tx.WithContext(ctx).
		Select("id").
		Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", skillID, userID).
		Limit(1).
		Find(&source)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func recordMarketInstall(ctx context.Context, tx *gorm.DB, marketItemID, userID, skillID string, now time.Time) error {
	row := skillMarketInstallRow{
		MarketItemID: marketItemID,
		UserID:       userID,
		SkillID:      skillID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	return tx.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "market_item_id"}, {Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"skill_id":   skillID,
			"updated_at": now,
		}),
	}).Create(&row).Error
}

func (s *Service) GetInstalledTree(ctx context.Context, req GetInstalledTreeRequest) (TreeNode, error) {
	var skill skillRow
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", req.SkillID, req.UserID).Take(&skill).Error; err != nil {
		return TreeNode{}, err
	}
	if skill.HeadRevisionID == nil {
		return TreeNode{}, fmt.Errorf("skill has no head revision")
	}
	var entries []skillRevisionEntryRow
	if err := s.db.WithContext(ctx).Where("revision_id = ?", *skill.HeadRevisionID).Order("path ASC").Find(&entries).Error; err != nil {
		return TreeNode{}, err
	}
	return buildTree(entries), nil
}

func (s *Service) Publish(ctx context.Context, req PublishRequest) (PublishResponse, error) {
	var out PublishResponse
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		sourceSkillID := newID()
		revisionID := newID()
		marketItemID := newID()
		now := time.Now()
		tags, _ := json.Marshal([]string{})
		adminID := req.AdminUserID
		if err := tx.Create(&skillRow{
			ID:                 sourceSkillID,
			OwnerUserID:        req.AdminUserID,
			OwnerUserName:      req.AdminUserID,
			CreateUserID:       req.AdminUserID,
			CreateUserName:     req.AdminUserID,
			Category:           req.Category,
			SkillName:          req.Name,
			Tags:               tags,
			RelativeRoot:       path.Join(req.Category, req.Name),
			SkillMDPath:        "SKILL.md",
			HeadRevisionID:     &revisionID,
			Version:            1,
			AutoEvoApplyStatus: "idle",
			IsEnabled:          true,
			UpdateStatus:       "up_to_date",
			CreatedAt:          now,
			UpdatedAt:          now,
		}).Error; err != nil {
			return err
		}
		files, err := filesFromSource(req)
		if err != nil {
			return err
		}
		entries, treeHash, err := entriesFromFiles(ctx, tx, revisionID, files, s.blobStore)
		if err != nil {
			return err
		}
		if err := tx.Create(&skillRevisionRow{
			ID:            revisionID,
			SkillID:       sourceSkillID,
			RevisionNo:    1,
			TreeHash:      treeHash,
			ChangeSource:  "market_publish",
			SourceRefType: req.Source.Type,
			SourceRefID:   req.Source.UploadID,
			CreatedBy:     &adminID,
			CreatedAt:     now,
		}).Error; err != nil {
			return err
		}
		if len(entries) > 0 {
			if err := tx.Create(&entries).Error; err != nil {
				return err
			}
		}
		if err := createDraft(tx, sourceSkillID, revisionID, now); err != nil {
			return err
		}
		if err := tx.Create(&skillMarketItemRow{
			ID:            marketItemID,
			SourceSkillID: sourceSkillID,
			Status:        "published",
			CreatedBy:     &adminID,
			UpdatedBy:     &adminID,
			PublishedAt:   &now,
			CreatedAt:     now,
			UpdatedAt:     now,
		}).Error; err != nil {
			return err
		}
		if err := skillsearch.RebuildSkillTx(ctx, tx, sourceSkillID, now); err != nil {
			return err
		}
		if strings.TrimSpace(req.AdminUserID) != "" {
			if err := recordMarketInstall(ctx, tx, marketItemID, req.AdminUserID, sourceSkillID, now); err != nil {
				return err
			}
		}
		out = PublishResponse{MarketItemID: marketItemID, SourceSkillID: sourceSkillID}
		return nil
	})
	return out, err
}

func (s *Service) Edit(ctx context.Context, req EditRequest) (EditResponse, error) {
	now := time.Now()
	updates := map[string]any{"version_note": req.VersionNote, "updated_by": req.AdminUserID, "updated_at": now}
	err := s.db.WithContext(ctx).Model(&skillMarketItemRow{}).Where("id = ?", req.MarketItemID).Updates(updates).Error
	return EditResponse{MarketItemID: req.MarketItemID}, err
}

func (s *Service) Unpublish(ctx context.Context, req UnpublishRequest) (UnpublishResponse, error) {
	now := time.Now()
	updates := map[string]any{"status": "unpublished", "updated_by": req.AdminUserID, "updated_at": now}
	err := s.db.WithContext(ctx).Model(&skillMarketItemRow{}).Where("id = ?", req.MarketItemID).Updates(updates).Error
	return UnpublishResponse{MarketItemID: req.MarketItemID}, err
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

type BlobStore struct {
	db      *gorm.DB
	objects *LocalObjectStore
}

func NewBlobStore(db *gorm.DB, objects *LocalObjectStore) *BlobStore {
	return &BlobStore{db: db, objects: objects}
}

func (s *BlobStore) put(ctx context.Context, tx *gorm.DB, filePath string, data []byte) (blobInfo, error) {
	if tx == nil {
		tx = s.db
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])
	mime, fileType, binary := classifyFile(filePath, data)
	info := blobInfo{Hash: hash, Size: int64(len(data)), Mime: mime, FileType: fileType, Binary: binary}
	var existing skillBlobRow
	result := tx.Where("hash = ?", hash).Limit(1).Find(&existing)
	if result.Error != nil {
		return blobInfo{}, result.Error
	}
	if result.RowsAffected > 0 {
		return blobInfo{Hash: hash, Size: existing.Size, Mime: existing.Mime, FileType: existing.FileType, Binary: existing.Binary}, nil
	}
	row := skillBlobRow{Hash: hash, Size: int64(len(data)), Mime: mime, FileType: fileType, Binary: binary, CreatedAt: time.Now()}
	if binary {
		key := strings.Join([]string{"skillv2", hash[:2], hash}, "/")
		if err := s.objects.Put(ctx, key, data); err != nil {
			return blobInfo{}, err
		}
		row.StorageBackend = "local_file"
		row.StorageKey = &key
	} else {
		row.StorageBackend = "postgres"
		row.Content = data
	}
	if err := tx.Create(&row).Error; err != nil {
		return blobInfo{}, err
	}
	return info, nil
}

type blobInfo struct {
	Hash     string
	Size     int64
	Mime     string
	FileType string
	Binary   bool
}

func copyHeadRevision(ctx context.Context, tx *gorm.DB, sourceSkillID, ownerUserID, ownerUserName, changeSource, createdBy string) (string, string, error) {
	var source skillRow
	if err := tx.Where("id = ?", sourceSkillID).Take(&source).Error; err != nil {
		return "", "", err
	}
	if source.HeadRevisionID == nil {
		return "", "", fmt.Errorf("source skill has no head revision")
	}
	var conflicts int64
	if err := tx.Model(&skillRow{}).Where("owner_user_id = ? AND category = ? AND skill_name = ? AND deleted_at IS NULL", ownerUserID, source.Category, source.SkillName).Count(&conflicts).Error; err != nil {
		return "", "", err
	}
	if conflicts > 0 {
		return "", "", fmt.Errorf("skill name conflict")
	}
	var sourceRev skillRevisionRow
	if err := tx.Where("id = ? AND skill_id = ?", *source.HeadRevisionID, source.ID).Take(&sourceRev).Error; err != nil {
		return "", "", err
	}
	var sourceEntries []skillRevisionEntryRow
	if err := tx.Where("revision_id = ?", sourceRev.ID).Order("path ASC").Find(&sourceEntries).Error; err != nil {
		return "", "", err
	}
	now := time.Now()
	skillID := newID()
	revisionID := newID()
	var createdByPtr *string
	if createdBy != "" {
		createdByPtr = &createdBy
	}
	copy := source
	copy.ID = skillID
	copy.OwnerUserID = ownerUserID
	copy.OwnerUserName = ownerUserName
	copy.CreateUserID = createdBy
	copy.CreateUserName = ownerUserName
	copy.HeadRevisionID = &revisionID
	copy.RelativeRoot = path.Join(source.Category, source.SkillName)
	copy.Version = 1
	copy.CreatedAt = now
	copy.UpdatedAt = now
	if err := tx.Create(&copy).Error; err != nil {
		return "", "", err
	}
	entries := make([]skillRevisionEntryRow, 0, len(sourceEntries))
	for _, entry := range sourceEntries {
		entry.RevisionID = revisionID
		entries = append(entries, entry)
	}
	treeHash := hashEntries(entries)
	if err := tx.Create(&skillRevisionRow{
		ID:               revisionID,
		SkillID:          skillID,
		ParentRevisionID: nil,
		RevisionNo:       1,
		TreeHash:         treeHash,
		ChangeSource:     changeSource,
		SourceRefType:    "skill",
		SourceRefID:      sourceSkillID,
		CreatedBy:        createdByPtr,
		CreatedAt:        now,
	}).Error; err != nil {
		return "", "", err
	}
	if len(entries) > 0 {
		if err := tx.Create(&entries).Error; err != nil {
			return "", "", err
		}
	}
	if err := createDraft(tx, skillID, revisionID, now); err != nil {
		return "", "", err
	}
	return skillID, revisionID, nil
}

func createDraft(tx *gorm.DB, skillID, revisionID string, now time.Time) error {
	return tx.Create(&skillDraftRow{SkillID: skillID, BaseRevisionID: &revisionID, Version: 1, CreatedAt: now, UpdatedAt: now}).Error
}

func entriesFromFiles(ctx context.Context, tx *gorm.DB, revisionID string, files map[string][]byte, blobs *BlobStore) ([]skillRevisionEntryRow, string, error) {
	paths := make([]string, 0, len(files))
	dirs := map[string]bool{}
	for filePath := range files {
		paths = append(paths, filePath)
		for dir := path.Dir(filePath); dir != "." && dir != "/"; dir = path.Dir(dir) {
			dirs[dir] = true
		}
	}
	sort.Strings(paths)
	dirPaths := make([]string, 0, len(dirs))
	for dir := range dirs {
		dirPaths = append(dirPaths, dir)
	}
	sort.Strings(dirPaths)
	entries := make([]skillRevisionEntryRow, 0, len(dirPaths)+len(paths))
	for _, dir := range dirPaths {
		entries = append(entries, skillRevisionEntryRow{RevisionID: revisionID, Path: dir, EntryType: "dir", FileType: "unknown", Mode: 0o755})
	}
	for _, filePath := range paths {
		blob, err := blobs.put(ctx, tx, filePath, files[filePath])
		if err != nil {
			return nil, "", err
		}
		hash := blob.Hash
		entries = append(entries, skillRevisionEntryRow{RevisionID: revisionID, Path: filePath, EntryType: "file", BlobHash: &hash, Size: blob.Size, Mime: blob.Mime, FileType: blob.FileType, Binary: blob.Binary, Mode: 0o644})
	}
	return entries, hashEntries(entries), nil
}

func buildTree(entries []skillRevisionEntryRow) TreeNode {
	root := TreeNode{Name: "", Path: "", Type: "dir"}
	nodeByPath := map[string]*TreeNode{"": &root}
	for _, entry := range entries {
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

func sortTree(nodes []TreeNode) {
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Path < nodes[j].Path })
	for i := range nodes {
		sortTree(nodes[i].Children)
	}
}

func hashEntries(entries []skillRevisionEntryRow) string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		hash := ""
		if entry.BlobHash != nil {
			hash = *entry.BlobHash
		}
		lines = append(lines, entry.Path+"\x00"+entry.EntryType+"\x00"+hash)
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

func filesFromSource(req PublishRequest) (map[string][]byte, error) {
	if strings.TrimSpace(req.Source.StoredPath) == "" {
		return defaultSkillFiles(req.Name), nil
	}
	files, err := readZipFiles(req.Source.StoredPath)
	if err != nil {
		return nil, err
	}
	if _, ok := files["SKILL.md"]; !ok {
		return nil, fmt.Errorf("skill package must contain SKILL.md")
	}
	return files, nil
}

func readZipFiles(zipPath string) (map[string][]byte, error) {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	files := map[string][]byte{}
	for _, entry := range reader.File {
		if entry.FileInfo().IsDir() {
			if _, err := cleanSkillPath(strings.TrimSuffix(entry.Name, "/")); err != nil {
				return nil, err
			}
			continue
		}
		name, err := cleanSkillPath(entry.Name)
		if err != nil {
			return nil, err
		}
		rc, err := entry.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		files[name] = data
	}
	return normalizeSkillPackageRoot(files), nil
}

func normalizeSkillPackageRoot(files map[string][]byte) map[string][]byte {
	if _, ok := files["SKILL.md"]; ok {
		return files
	}
	root := ""
	for filePath := range files {
		parts := strings.SplitN(filePath, "/", 2)
		if len(parts) != 2 || parts[1] == "" {
			return files
		}
		if root == "" {
			root = parts[0]
			continue
		}
		if root != parts[0] {
			return files
		}
	}
	if root == "" {
		return files
	}
	normalized := make(map[string][]byte, len(files))
	prefix := root + "/"
	for filePath, data := range files {
		relPath := strings.TrimPrefix(filePath, prefix)
		normalized[relPath] = data
	}
	if _, ok := normalized["SKILL.md"]; ok {
		return normalized
	}
	return files
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

func defaultSkillFiles(name string) map[string][]byte {
	return map[string][]byte{
		"SKILL.md":        []byte("# " + name + "\n\n用于阅读和总结论文。\n"),
		"references/a.md": []byte("# 参考资料\n\n这是参考资料。\n"),
		"scripts/run.py":  []byte("print(\"hello skill\")\n"),
	}
}

func classifyFile(filePath string, data []byte) (string, string, bool) {
	ext := strings.ToLower(path.Ext(filePath))
	switch ext {
	case ".md", ".markdown":
		return "text/markdown", "markdown", false
	case ".png":
		return "image/png", "image", true
	case ".py", ".txt", ".json", ".yaml", ".yml", ".toml", ".js", ".ts":
		return "text/plain", "text", false
	}
	if utf8.Valid(data) {
		return "text/plain", "text", false
	}
	return "application/octet-stream", "binary", true
}

func newID() string {
	if id, err := uuid.NewRandom(); err == nil {
		return id.String()
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

type skillRow struct {
	ID                    string     `gorm:"column:id;type:varchar(36);primaryKey"`
	OwnerUserID           string     `gorm:"column:owner_user_id;type:text;not null"`
	OwnerUserName         string     `gorm:"column:owner_user_name;type:text;not null;default:''"`
	CreateUserID          string     `gorm:"column:create_user_id;type:text;not null"`
	CreateUserName        string     `gorm:"column:create_user_name;type:text;not null;default:''"`
	Category              string     `gorm:"column:category;type:text;not null"`
	SkillName             string     `gorm:"column:skill_name;type:text;not null"`
	OriginBuiltinSkillUID string     `gorm:"column:origin_builtin_skill_uid;type:text;not null;default:''"`
	Description           string     `gorm:"column:description;type:text"`
	Tags                  []byte     `gorm:"column:tags;type:json"`
	RelativeRoot          string     `gorm:"column:relative_root;type:text;not null"`
	SkillMDPath           string     `gorm:"column:skill_md_path;type:text;not null;default:'SKILL.md'"`
	HeadRevisionID        *string    `gorm:"column:head_revision_id;type:varchar(36)"`
	Version               int64      `gorm:"column:version;not null;default:1"`
	AutoEvo               bool       `gorm:"column:auto_evo;not null;default:false"`
	AutoEvoApplyStatus    string     `gorm:"column:auto_evo_apply_status;type:text;not null;default:'idle'"`
	AutoEvoGeneration     int64      `gorm:"column:auto_evo_generation;not null;default:0"`
	AutoEvoStartedAt      *time.Time `gorm:"column:auto_evo_started_at"`
	AutoEvoFinishedAt     *time.Time `gorm:"column:auto_evo_finished_at"`
	AutoEvoError          string     `gorm:"column:auto_evo_error;type:text;not null;default:''"`
	IsEnabled             bool       `gorm:"column:is_enabled;not null;default:true"`
	UpdateStatus          string     `gorm:"column:update_status;type:text;not null;default:'up_to_date'"`
	Ext                   []byte     `gorm:"column:ext;type:json"`
	CreatedAt             time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt             time.Time  `gorm:"column:updated_at;not null"`
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
	DraftStatus    string     `gorm:"column:draft_status;type:text;not null;default:''"`
	DraftUpdatedAt *time.Time `gorm:"column:draft_updated_at"`
	TaskID         string     `gorm:"column:task_id;type:text;not null;default:''"`
	ConversationID *string    `gorm:"column:conversation_id;type:varchar(128)"`
	UpdatedBy      *string    `gorm:"column:updated_by;type:varchar(36)"`
	Version        int64      `gorm:"column:version;not null;default:1"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;not null"`
}

func (skillDraftRow) TableName() string { return "skill_drafts" }

type skillMarketItemRow struct {
	ID            string     `gorm:"column:id;type:varchar(36);primaryKey"`
	SourceSkillID string     `gorm:"column:source_skill_id;type:varchar(36);not null"`
	Status        string     `gorm:"column:status;type:text;not null;default:'draft'"`
	Icon          string     `gorm:"column:icon;type:text;not null;default:''"`
	SortOrder     int        `gorm:"column:sort_order;not null;default:0"`
	VersionNote   string     `gorm:"column:version_note;type:text;not null;default:''"`
	CreatedBy     *string    `gorm:"column:created_by;type:varchar(36)"`
	UpdatedBy     *string    `gorm:"column:updated_by;type:varchar(36)"`
	PublishedAt   *time.Time `gorm:"column:published_at"`
	CreatedAt     time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt     time.Time  `gorm:"column:updated_at;not null"`
}

func (skillMarketItemRow) TableName() string { return "skill_market_items" }

type skillMarketInstallRow struct {
	MarketItemID string    `gorm:"column:market_item_id;type:varchar(36);primaryKey"`
	UserID       string    `gorm:"column:user_id;type:text;primaryKey"`
	SkillID      string    `gorm:"column:skill_id;type:varchar(36);not null"`
	CreatedAt    time.Time `gorm:"column:created_at;not null"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null"`
}

func (skillMarketInstallRow) TableName() string { return "skill_market_installs" }
