package distribution

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/skillv2/merge3"
)

const upgradeTaskPrefix = "distribution_upgrade:"

type distributionError string

func (err distributionError) Error() string { return string(err) }

const ErrConflictsRequireReview distributionError = "distribution upgrade conflicts require draft review"
const ErrUpgradeDraftActive distributionError = "distribution upgrade draft is active"
const ErrDraftActive distributionError = "cannot prepare distribution upgrade while draft overlay exists"

func IsUpgradeTaskID(taskID string) bool {
	return strings.HasPrefix(strings.TrimSpace(taskID), upgradeTaskPrefix)
}

func UpgradeTaskID(archiveSHA string) string {
	return upgradeTaskPrefix + strings.TrimSpace(archiveSHA)
}

func failure(format string, args ...any) error {
	return distributionError(fmt.Sprintf(format, args...))
}

type Clock interface {
	Now() time.Time
}

type Blob struct {
	Hash     string
	Size     int64
	Mime     string
	FileType string
	Binary   bool
}

type BlobStore interface {
	StoreDistributionBlob(ctx context.Context, tx *gorm.DB, path string, data []byte, now time.Time) (Blob, error)
	ReadDistributionBlob(ctx context.Context, tx *gorm.DB, hash string) ([]byte, error)
}

type Package struct {
	UID           string
	Version       string
	ArchiveSHA256 string
	TreeSHA256    string
	Files         map[string][]byte
}

type PackageProvider interface {
	Latest(uid string) (Package, bool, error)
}

type ServiceDeps struct {
	DB       *gorm.DB
	Blobs    BlobStore
	Provider PackageProvider
	Clock    Clock
}

type Service struct {
	db       *gorm.DB
	blobs    BlobStore
	provider PackageProvider
	clock    Clock
}

type StatusRequest struct {
	SkillID string
	UserID  string
}

type Status struct {
	Managed              bool       `json:"managed"`
	UpdateAvailable      bool       `json:"update_available"`
	Pending              bool       `json:"pending"`
	CurrentVersion       string     `json:"current_version,omitempty"`
	CurrentArchiveSHA256 string     `json:"current_archive_sha256,omitempty"`
	PendingVersion       string     `json:"pending_version,omitempty"`
	PendingArchiveSHA256 string     `json:"pending_archive_sha256,omitempty"`
	LatestVersion        string     `json:"latest_version,omitempty"`
	LatestArchiveSHA256  string     `json:"latest_archive_sha256,omitempty"`
	Conflicts            []Conflict `json:"conflicts"`
}

type Conflict = merge3.Conflict

type PrepareRequest struct {
	SkillID string
	UserID  string
}

type PrepareResponse struct {
	DraftVersion int64      `json:"draft_version"`
	AutoMerged   bool       `json:"auto_merged"`
	Conflicts    []Conflict `json:"conflicts"`
	Status       Status     `json:"status"`
}

type InitialBinding struct {
	SkillID       string
	RevisionID    string
	BuiltinUID    string
	Version       string
	ArchiveSHA256 string
	TreeSHA256    string
}

type PendingRef struct {
	ArchiveSHA256 string
	ConflictCount int
}

type DraftCommitter interface {
	CommitDraft(ctx context.Context, skillID, userID string, draftVersion int64) (string, error)
}

type AutoApplyResult struct {
	Applied       bool
	PendingReview bool
	RevisionID    string
}

func NewService(deps ServiceDeps) *Service {
	clock := deps.Clock
	if clock == nil {
		clock = systemClock{}
	}
	return &Service{db: deps.DB, blobs: deps.Blobs, provider: deps.Provider, clock: clock}
}

func BindInitialTx(ctx context.Context, tx *gorm.DB, binding InitialBinding, now time.Time) error {
	if strings.TrimSpace(binding.SkillID) == "" || strings.TrimSpace(binding.RevisionID) == "" || strings.TrimSpace(binding.BuiltinUID) == "" || strings.TrimSpace(binding.ArchiveSHA256) == "" {
		return failure("distribution binding is incomplete")
	}
	artifact := artifactRow{ArchiveSHA256: binding.ArchiveSHA256, BuiltinSkillUID: binding.BuiltinUID, Version: binding.Version, TreeSHA256: binding.TreeSHA256, CreatedAt: now}
	if err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&artifact).Error; err != nil {
		return err
	}
	var existingEntries int64
	if err := tx.WithContext(ctx).Model(&artifactEntryRow{}).Where("archive_sha256 = ?", binding.ArchiveSHA256).Count(&existingEntries).Error; err != nil {
		return err
	}
	if existingEntries == 0 {
		var revisionEntries []revisionEntryRow
		if err := tx.WithContext(ctx).Where("revision_id = ?", binding.RevisionID).Order("path ASC").Find(&revisionEntries).Error; err != nil {
			return err
		}
		entries := make([]artifactEntryRow, 0, len(revisionEntries))
		for _, entry := range revisionEntries {
			entries = append(entries, artifactEntryRow{
				ArchiveSHA256: binding.ArchiveSHA256, Path: entry.Path, EntryType: entry.EntryType, BlobHash: entry.BlobHash,
				Size: entry.Size, Mime: entry.Mime, FileType: entry.FileType, Binary: entry.Binary, Mode: entry.Mode,
			})
		}
		if len(entries) > 0 {
			if err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&entries).Error; err != nil {
				return err
			}
		}
	}
	row := bindingRow{
		SkillID: binding.SkillID, BuiltinSkillUID: binding.BuiltinUID, CurrentArchiveSHA256: binding.ArchiveSHA256,
		Conflicts: []byte("[]"), CreatedAt: now, UpdatedAt: now,
	}
	if err := tx.WithContext(ctx).Save(&row).Error; err != nil {
		return err
	}
	return mapRevisionTx(ctx, tx, binding.RevisionID, binding.ArchiveSHA256, now)
}

func (s *Service) GetStatus(ctx context.Context, req StatusRequest) (Status, error) {
	var skill skillRow
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", req.SkillID, req.UserID).Take(&skill).Error; err != nil {
		return Status{}, err
	}
	binding, found, err := s.ensureBinding(ctx, skill)
	if err != nil {
		return Status{}, err
	}
	if !found {
		return Status{Conflicts: []Conflict{}}, nil
	}
	return s.statusForBinding(ctx, s.db, binding)
}

func (s *Service) AutoApply(ctx context.Context, req PrepareRequest, committer DraftCommitter) (AutoApplyResult, error) {
	prepared, err := s.Prepare(ctx, req)
	if err != nil {
		return AutoApplyResult{}, err
	}
	if prepared.Status.Pending && len(prepared.Conflicts) > 0 {
		return AutoApplyResult{PendingReview: true}, nil
	}
	if prepared.DraftVersion == 0 {
		return AutoApplyResult{Applied: !prepared.Status.UpdateAvailable}, nil
	}
	if committer == nil {
		return AutoApplyResult{}, failure("distribution upgrade draft committer is not configured")
	}
	revisionID, err := committer.CommitDraft(ctx, req.SkillID, req.UserID, prepared.DraftVersion)
	if err != nil {
		return AutoApplyResult{}, err
	}
	return AutoApplyResult{Applied: true, RevisionID: revisionID}, nil
}

func (s *Service) Prepare(ctx context.Context, req PrepareRequest) (PrepareResponse, error) {
	if s.blobs == nil || s.provider == nil {
		return PrepareResponse{}, failure("distribution upgrade service is not configured")
	}
	var initialSkill skillRow
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", req.SkillID, req.UserID).Take(&initialSkill).Error; err != nil {
		return PrepareResponse{}, err
	}
	initialBinding, found, err := s.ensureBinding(ctx, initialSkill)
	if err != nil {
		return PrepareResponse{}, err
	}
	if !found {
		return PrepareResponse{}, failure("installed Skill distribution baseline is unavailable")
	}
	latest, found, err := s.provider.Latest(initialBinding.BuiltinSkillUID)
	if err != nil {
		return PrepareResponse{}, err
	}
	if !found {
		return PrepareResponse{}, failure("latest builtin Skill distribution was not found")
	}

	var response PrepareResponse
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var skill skillRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND owner_user_id = ? AND deleted_at IS NULL", req.SkillID, req.UserID).Take(&skill).Error; err != nil {
			return err
		}
		var binding bindingRow
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("skill_id = ?", req.SkillID).Take(&binding).Error; err != nil {
			return err
		}
		if binding.CurrentArchiveSHA256 == latest.ArchiveSHA256 && binding.PendingArchiveSHA256 == "" {
			status, err := s.statusForBinding(ctx, tx, binding)
			response = PrepareResponse{AutoMerged: true, Conflicts: []Conflict{}, Status: status}
			return err
		}
		if binding.PendingArchiveSHA256 != "" {
			var draft draftRow
			if err := tx.Where("skill_id = ?", req.SkillID).Take(&draft).Error; err != nil {
				return err
			}
			if draft.TaskID != UpgradeTaskID(binding.PendingArchiveSHA256) {
				return failure("pending distribution binding has no matching upgrade draft")
			}
			var draftCount int64
			if err := tx.Model(&draftEntryRow{}).Where("skill_id = ?", req.SkillID).Count(&draftCount).Error; err != nil {
				return err
			}
			if draftCount == 0 {
				return failure("pending distribution binding has an empty upgrade draft")
			}
			conflicts, err := decodeConflicts(binding.Conflicts)
			if err != nil {
				return err
			}
			status, err := s.statusForBinding(ctx, tx, binding)
			if err != nil {
				return err
			}
			response = PrepareResponse{DraftVersion: draft.Version, AutoMerged: len(conflicts) == 0, Conflicts: conflicts, Status: status}
			return nil
		}
		var draftCount int64
		if err := tx.Model(&draftEntryRow{}).Where("skill_id = ?", req.SkillID).Count(&draftCount).Error; err != nil {
			return err
		}
		if draftCount > 0 {
			return ErrDraftActive
		}
		if skill.HeadRevisionID == nil || strings.TrimSpace(*skill.HeadRevisionID) == "" {
			return failure("Skill has no head revision")
		}
		now := s.clock.Now()
		if err := s.storeArtifactTx(ctx, tx, latest, now); err != nil {
			return err
		}
		baseFiles, err := s.loadArtifactFiles(ctx, tx, binding.CurrentArchiveSHA256)
		if err != nil {
			return err
		}
		oursFiles, err := s.loadRevisionFiles(ctx, tx, *skill.HeadRevisionID)
		if err != nil {
			return err
		}
		theirsFiles, err := s.loadArtifactFiles(ctx, tx, latest.ArchiveSHA256)
		if err != nil {
			return err
		}
		merged := merge3.MergeTrees(baseFiles, oursFiles, theirsFiles)
		draftVersion, changed, err := s.stageDraftTx(ctx, tx, skill, merged.Files, latest.ArchiveSHA256, now)
		if err != nil {
			return err
		}
		conflicts, err := json.Marshal(merged.Conflicts)
		if err != nil {
			return err
		}
		if !changed {
			if err := promoteBindingTx(ctx, tx, &binding, latest.ArchiveSHA256, *skill.HeadRevisionID, now); err != nil {
				return err
			}
			binding.CurrentArchiveSHA256 = latest.ArchiveSHA256
			binding.PendingArchiveSHA256 = ""
			binding.Conflicts = []byte("[]")
		} else {
			if err := tx.Model(&bindingRow{}).Where("skill_id = ?", req.SkillID).Updates(map[string]any{
				"pending_archive_sha256": latest.ArchiveSHA256,
				"conflicts":              conflicts,
				"updated_at":             now,
			}).Error; err != nil {
				return err
			}
			if err := tx.Model(&skillRow{}).Where("id = ?", req.SkillID).Updates(map[string]any{"update_status": "pending_review", "updated_at": now}).Error; err != nil {
				return err
			}
			binding.PendingArchiveSHA256 = latest.ArchiveSHA256
			binding.Conflicts = conflicts
		}
		status, err := s.statusForBinding(ctx, tx, binding)
		if err != nil {
			return err
		}
		response = PrepareResponse{DraftVersion: draftVersion, AutoMerged: len(merged.Conflicts) == 0, Conflicts: merged.Conflicts, Status: status}
		return nil
	})
	return response, err
}

func (s *Service) ensureBinding(ctx context.Context, skill skillRow) (bindingRow, bool, error) {
	var binding bindingRow
	err := s.db.WithContext(ctx).Where("skill_id = ?", skill.ID).Take(&binding).Error
	if err == nil {
		return binding, true, nil
	}
	if err != gorm.ErrRecordNotFound {
		return bindingRow{}, false, err
	}
	if strings.TrimSpace(skill.OriginBuiltinSkillUID) == "" {
		return bindingRow{}, false, nil
	}
	var revision revisionSourceRow
	if err := s.db.WithContext(ctx).
		Where("skill_id = ? AND source_ref_type = ?", skill.ID, "builtin_package").
		Order("revision_no ASC").Take(&revision).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return bindingRow{}, false, nil
		}
		return bindingRow{}, false, err
	}
	version, archiveSHA, ok := parseBuiltinSourceRef(revision.SourceRefID)
	if !ok {
		return bindingRow{}, false, nil
	}
	treeSHA := ""
	if s.provider != nil {
		if latest, found, err := s.provider.Latest(skill.OriginBuiltinSkillUID); err != nil {
			return bindingRow{}, false, err
		} else if found && latest.ArchiveSHA256 == archiveSHA {
			treeSHA = latest.TreeSHA256
		}
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return BindInitialTx(ctx, tx, InitialBinding{
			SkillID: skill.ID, RevisionID: revision.ID, BuiltinUID: skill.OriginBuiltinSkillUID,
			Version: version, ArchiveSHA256: archiveSHA, TreeSHA256: treeSHA,
		}, s.clock.Now())
	})
	if err != nil {
		return bindingRow{}, false, err
	}
	if err := s.db.WithContext(ctx).Where("skill_id = ?", skill.ID).Take(&binding).Error; err != nil {
		return bindingRow{}, false, err
	}
	return binding, true, nil
}

func parseBuiltinSourceRef(value string) (string, string, bool) {
	hashIndex := strings.LastIndex(value, "#")
	if hashIndex <= 0 || hashIndex >= len(value)-1 {
		return "", "", false
	}
	atIndex := strings.LastIndex(value[:hashIndex], "@")
	if atIndex <= 0 || atIndex >= hashIndex-1 {
		return "", "", false
	}
	return value[atIndex+1 : hashIndex], value[hashIndex+1:], true
}

func (s *Service) statusForBinding(ctx context.Context, db *gorm.DB, binding bindingRow) (Status, error) {
	currentVersion := ""
	if binding.CurrentArchiveSHA256 != "" {
		var artifact artifactRow
		if err := db.WithContext(ctx).Where("archive_sha256 = ?", binding.CurrentArchiveSHA256).Take(&artifact).Error; err != nil {
			return Status{}, err
		}
		currentVersion = artifact.Version
	}
	status := Status{
		Managed: true, Pending: binding.PendingArchiveSHA256 != "", CurrentVersion: currentVersion,
		CurrentArchiveSHA256: binding.CurrentArchiveSHA256, PendingArchiveSHA256: binding.PendingArchiveSHA256, Conflicts: []Conflict{},
	}
	conflicts, err := decodeConflicts(binding.Conflicts)
	if err != nil {
		return Status{}, err
	}
	status.Conflicts = conflicts
	if binding.PendingArchiveSHA256 != "" {
		var pending artifactRow
		if err := db.WithContext(ctx).Where("archive_sha256 = ?", binding.PendingArchiveSHA256).Take(&pending).Error; err != nil {
			return Status{}, err
		}
		status.PendingVersion = pending.Version
	}
	if s.provider != nil {
		latest, found, err := s.provider.Latest(binding.BuiltinSkillUID)
		if err != nil {
			return Status{}, err
		}
		if found {
			status.LatestVersion = latest.Version
			status.LatestArchiveSHA256 = latest.ArchiveSHA256
			status.UpdateAvailable = latest.ArchiveSHA256 != binding.CurrentArchiveSHA256
		}
	}
	return status, nil
}

func decodeConflicts(value []byte) ([]Conflict, error) {
	if len(value) == 0 {
		return []Conflict{}, nil
	}
	var conflicts []Conflict
	if err := json.Unmarshal(value, &conflicts); err != nil {
		return nil, failure("invalid distribution conflict state: %v", err)
	}
	if conflicts == nil {
		conflicts = []Conflict{}
	}
	return conflicts, nil
}

func (s *Service) storeArtifactTx(ctx context.Context, tx *gorm.DB, pkg Package, now time.Time) error {
	return StoreArtifactTx(ctx, tx, s.blobs, pkg, now)
}

func StoreArtifactTx(ctx context.Context, tx *gorm.DB, blobs BlobStore, pkg Package, now time.Time) error {
	if blobs == nil {
		return failure("distribution blob store is required")
	}
	archiveSHA := strings.TrimSpace(pkg.ArchiveSHA256)
	if archiveSHA == "" {
		return failure("distribution binding is incomplete")
	}
	var count int64
	if err := tx.WithContext(ctx).Model(&artifactRow{}).Where("archive_sha256 = ?", archiveSHA).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	artifact := artifactRow{ArchiveSHA256: archiveSHA, BuiltinSkillUID: pkg.UID, Version: pkg.Version, TreeSHA256: pkg.TreeSHA256, CreatedAt: now}
	if err := tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&artifact).Error; err != nil {
		return err
	}
	entries, err := artifactEntriesFromFiles(ctx, tx, blobs, archiveSHA, pkg.Files, now)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return tx.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&entries).Error
	}
	return nil
}

func artifactEntriesFromFiles(ctx context.Context, tx *gorm.DB, blobs BlobStore, archiveSHA string, files map[string][]byte, now time.Time) ([]artifactEntryRow, error) {
	dirs := make(map[string]bool)
	paths := sortedFilePaths(files)
	for _, filePath := range paths {
		for dir := path.Dir(filePath); dir != "." && dir != "/"; dir = path.Dir(dir) {
			dirs[dir] = true
		}
	}
	dirPaths := make([]string, 0, len(dirs))
	for dir := range dirs {
		dirPaths = append(dirPaths, dir)
	}
	sort.Strings(dirPaths)
	entries := make([]artifactEntryRow, 0, len(dirPaths)+len(paths))
	for _, dir := range dirPaths {
		entries = append(entries, artifactEntryRow{ArchiveSHA256: archiveSHA, Path: dir, EntryType: "dir", FileType: "unknown", Mode: 0o755})
	}
	for _, filePath := range paths {
		blob, err := blobs.StoreDistributionBlob(ctx, tx, filePath, files[filePath], now)
		if err != nil {
			return nil, err
		}
		hash := blob.Hash
		entries = append(entries, artifactEntryRow{
			ArchiveSHA256: archiveSHA, Path: filePath, EntryType: "file", BlobHash: &hash,
			Size: blob.Size, Mime: blob.Mime, FileType: blob.FileType, Binary: blob.Binary, Mode: 0o644,
		})
	}
	return entries, nil
}

func (s *Service) loadArtifactFiles(ctx context.Context, tx *gorm.DB, archiveSHA string) (map[string]merge3.File, error) {
	var entries []artifactEntryRow
	if err := tx.Where("archive_sha256 = ? AND entry_type = ?", archiveSHA, "file").Order("path ASC").Find(&entries).Error; err != nil {
		return nil, err
	}
	return s.loadFiles(ctx, tx, artifactFiles(entries))
}

func (s *Service) loadRevisionFiles(ctx context.Context, tx *gorm.DB, revisionID string) (map[string]merge3.File, error) {
	var entries []revisionEntryRow
	if err := tx.Where("revision_id = ? AND entry_type = ?", revisionID, "file").Order("path ASC").Find(&entries).Error; err != nil {
		return nil, err
	}
	return s.loadFiles(ctx, tx, revisionFiles(entries))
}

type contentEntry struct {
	Path     string
	BlobHash string
	Binary   bool
	Mode     int
}

func (s *Service) loadFiles(ctx context.Context, tx *gorm.DB, entries []contentEntry) (map[string]merge3.File, error) {
	files := make(map[string]merge3.File, len(entries))
	for _, entry := range entries {
		content, err := s.blobs.ReadDistributionBlob(ctx, tx, entry.BlobHash)
		if err != nil {
			return nil, err
		}
		files[entry.Path] = merge3.File{Data: content, Binary: entry.Binary, Mode: entry.Mode}
	}
	return files, nil
}

func (s *Service) stageDraftTx(ctx context.Context, tx *gorm.DB, skill skillRow, files map[string]merge3.File, targetArchive string, now time.Time) (int64, bool, error) {
	entries, err := s.entriesFromMergedFiles(ctx, tx, files, now)
	if err != nil {
		return 0, false, err
	}
	var headEntries []revisionEntryRow
	if err := tx.Where("revision_id = ?", *skill.HeadRevisionID).Order("path ASC").Find(&headEntries).Error; err != nil {
		return 0, false, err
	}
	head := make(map[string]revisionEntryRow, len(headEntries))
	for _, entry := range headEntries {
		head[entry.Path] = entry
	}
	paths := unionEntryPaths(head, entries)
	overlays := make([]draftEntryRow, 0, len(paths))
	for _, entryPath := range paths {
		headEntry, headOK := head[entryPath]
		candidate, candidateOK := entries[entryPath]
		if headOK && candidateOK && sameEntry(headEntry, candidate) {
			continue
		}
		if !candidateOK {
			overlays = append(overlays, draftEntryRow{SkillID: skill.ID, Path: entryPath, Op: "delete", UpdatedAt: now})
			continue
		}
		overlays = append(overlays, draftEntryRow{
			SkillID: skill.ID, Path: candidate.Path, Op: "upsert", EntryType: candidate.EntryType, BlobHash: candidate.BlobHash,
			Size: candidate.Size, Mime: candidate.Mime, FileType: candidate.FileType, Binary: candidate.Binary, Mode: candidate.Mode, UpdatedAt: now,
		})
	}
	if len(overlays) == 0 {
		return 0, false, nil
	}
	if err := tx.Where("skill_id = ?", skill.ID).Delete(&draftEntryRow{}).Error; err != nil {
		return 0, false, err
	}
	if err := tx.Create(&overlays).Error; err != nil {
		return 0, false, err
	}
	var draft draftRow
	err = tx.Where("skill_id = ?", skill.ID).Take(&draft).Error
	if err != nil && err != gorm.ErrRecordNotFound {
		return 0, false, err
	}
	if err == gorm.ErrRecordNotFound {
		draft = draftRow{SkillID: skill.ID, BaseRevisionID: skill.HeadRevisionID, DraftStatus: "pending_confirm", TaskID: UpgradeTaskID(targetArchive), Version: 1, DraftUpdatedAt: &now, CreatedAt: now, UpdatedAt: now}
		if err := tx.Create(&draft).Error; err != nil {
			return 0, false, err
		}
	} else {
		draft.Version++
		if err := tx.Model(&draftRow{}).Where("skill_id = ?", skill.ID).Updates(map[string]any{
			"base_revision_id": skill.HeadRevisionID, "draft_status": "pending_confirm", "task_id": UpgradeTaskID(targetArchive),
			"conversation_id": nil, "draft_updated_at": now, "version": draft.Version, "updated_at": now,
		}).Error; err != nil {
			return 0, false, err
		}
	}
	if err := tx.Table("skill_draft_review_sessions").Where("skill_id = ? AND status = ?", skill.ID, "active").Updates(map[string]any{"status": "invalidated", "updated_at": now}).Error; err != nil {
		return 0, false, err
	}
	return draft.Version, true, nil
}

func (s *Service) entriesFromMergedFiles(ctx context.Context, tx *gorm.DB, files map[string]merge3.File, now time.Time) (map[string]revisionEntryRow, error) {
	dirs := make(map[string]bool)
	paths := make([]string, 0, len(files))
	for filePath := range files {
		paths = append(paths, filePath)
		for dir := path.Dir(filePath); dir != "." && dir != "/"; dir = path.Dir(dir) {
			dirs[dir] = true
		}
	}
	sort.Strings(paths)
	entries := make(map[string]revisionEntryRow, len(paths)+len(dirs))
	for dir := range dirs {
		entries[dir] = revisionEntryRow{Path: dir, EntryType: "dir", FileType: "unknown", Mode: 0o755}
	}
	for _, filePath := range paths {
		file := files[filePath]
		blob, err := s.blobs.StoreDistributionBlob(ctx, tx, filePath, file.Data, now)
		if err != nil {
			return nil, err
		}
		hash := blob.Hash
		mode := file.Mode
		if mode == 0 {
			mode = 0o644
		}
		entries[filePath] = revisionEntryRow{Path: filePath, EntryType: "file", BlobHash: &hash, Size: blob.Size, Mime: blob.Mime, FileType: blob.FileType, Binary: blob.Binary, Mode: mode}
	}
	return entries, nil
}

func PendingRefTx(ctx context.Context, tx *gorm.DB, skillID string) (PendingRef, bool, error) {
	if !tx.Migrator().HasTable("skill_distribution_bindings") {
		return PendingRef{}, false, nil
	}
	var binding bindingRow
	if err := tx.WithContext(ctx).Where("skill_id = ?", skillID).Take(&binding).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return PendingRef{}, false, nil
		}
		return PendingRef{}, false, err
	}
	if binding.PendingArchiveSHA256 == "" {
		return PendingRef{}, false, nil
	}
	var artifact artifactRow
	if err := tx.WithContext(ctx).Where("archive_sha256 = ?", binding.PendingArchiveSHA256).Take(&artifact).Error; err != nil {
		return PendingRef{}, false, err
	}
	conflicts, err := decodeConflicts(binding.Conflicts)
	if err != nil {
		return PendingRef{}, false, err
	}
	return PendingRef{ArchiveSHA256: artifact.ArchiveSHA256, ConflictCount: len(conflicts)}, true, nil
}

func PromotePendingTx(ctx context.Context, tx *gorm.DB, skillID, revisionID string, now time.Time) error {
	if !tx.Migrator().HasTable("skill_distribution_bindings") {
		return nil
	}
	var binding bindingRow
	if err := tx.WithContext(ctx).Where("skill_id = ?", skillID).Take(&binding).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil
		}
		return err
	}
	if binding.PendingArchiveSHA256 == "" {
		return nil
	}
	return promoteBindingTx(ctx, tx, &binding, binding.PendingArchiveSHA256, revisionID, now)
}

func promoteBindingTx(ctx context.Context, tx *gorm.DB, binding *bindingRow, archiveSHA, revisionID string, now time.Time) error {
	if err := tx.WithContext(ctx).Model(&bindingRow{}).Where("skill_id = ?", binding.SkillID).Updates(map[string]any{
		"current_archive_sha256": archiveSHA, "pending_archive_sha256": "", "conflicts": []byte("[]"), "updated_at": now,
	}).Error; err != nil {
		return err
	}
	if err := tx.WithContext(ctx).Model(&skillRow{}).Where("id = ?", binding.SkillID).Updates(map[string]any{"update_status": "up_to_date", "updated_at": now}).Error; err != nil {
		return err
	}
	return mapRevisionTx(ctx, tx, revisionID, archiveSHA, now)
}

func CancelPendingTx(ctx context.Context, tx *gorm.DB, skillID string, now time.Time) error {
	if !tx.Migrator().HasTable("skill_distribution_bindings") {
		return nil
	}
	if err := tx.WithContext(ctx).Model(&bindingRow{}).Where("skill_id = ? AND pending_archive_sha256 <> ''", skillID).Updates(map[string]any{
		"pending_archive_sha256": "", "conflicts": []byte("[]"), "updated_at": now,
	}).Error; err != nil {
		return err
	}
	return tx.WithContext(ctx).Model(&skillRow{}).Where("id = ?", skillID).Updates(map[string]any{"update_status": "up_to_date", "updated_at": now}).Error
}

func DeleteBindingTx(ctx context.Context, tx *gorm.DB, skillID string, revisionIDs []string) error {
	if !tx.Migrator().HasTable("skill_distribution_bindings") {
		return nil
	}
	if len(revisionIDs) > 0 {
		if err := tx.WithContext(ctx).Where("revision_id IN ?", revisionIDs).Delete(&revisionDistributionRow{}).Error; err != nil {
			return err
		}
	}
	return tx.WithContext(ctx).Where("skill_id = ?", skillID).Delete(&bindingRow{}).Error
}

func RebindForRevisionTx(ctx context.Context, tx *gorm.DB, skillID, revisionID string, now time.Time) error {
	if !tx.Migrator().HasTable("skill_distribution_bindings") {
		return nil
	}
	current := revisionID
	for current != "" {
		var mapping revisionDistributionRow
		err := tx.WithContext(ctx).Where("revision_id = ?", current).Take(&mapping).Error
		if err == nil {
			if err := tx.WithContext(ctx).Model(&bindingRow{}).Where("skill_id = ?", skillID).Updates(map[string]any{
				"current_archive_sha256": mapping.ArchiveSHA256, "pending_archive_sha256": "", "conflicts": []byte("[]"), "updated_at": now,
			}).Error; err != nil {
				return err
			}
			return tx.WithContext(ctx).Model(&skillRow{}).Where("id = ?", skillID).Updates(map[string]any{"update_status": "up_to_date", "updated_at": now}).Error
		}
		if err != gorm.ErrRecordNotFound {
			return err
		}
		var revision revisionRow
		if err := tx.WithContext(ctx).Select("parent_revision_id").Where("id = ? AND skill_id = ?", current, skillID).Take(&revision).Error; err != nil {
			return err
		}
		if revision.ParentRevisionID == nil {
			break
		}
		current = *revision.ParentRevisionID
	}
	return nil
}

func mapRevisionTx(ctx context.Context, tx *gorm.DB, revisionID, archiveSHA string, now time.Time) error {
	row := revisionDistributionRow{RevisionID: revisionID, ArchiveSHA256: archiveSHA, CreatedAt: now}
	return tx.WithContext(ctx).Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "revision_id"}}, DoUpdates: clause.AssignmentColumns([]string{"archive_sha256"})}).Create(&row).Error
}

func artifactFiles(entries []artifactEntryRow) []contentEntry {
	out := make([]contentEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.BlobHash != nil {
			out = append(out, contentEntry{Path: entry.Path, BlobHash: *entry.BlobHash, Binary: entry.Binary, Mode: entry.Mode})
		}
	}
	return out
}

func revisionFiles(entries []revisionEntryRow) []contentEntry {
	out := make([]contentEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.BlobHash != nil {
			out = append(out, contentEntry{Path: entry.Path, BlobHash: *entry.BlobHash, Binary: entry.Binary, Mode: entry.Mode})
		}
	}
	return out
}

func sameEntry(left, right revisionEntryRow) bool {
	leftHash, rightHash := "", ""
	if left.BlobHash != nil {
		leftHash = *left.BlobHash
	}
	if right.BlobHash != nil {
		rightHash = *right.BlobHash
	}
	return left.EntryType == right.EntryType && leftHash == rightHash && left.Mode == right.Mode
}

func unionEntryPaths(left, right map[string]revisionEntryRow) []string {
	seen := make(map[string]bool, len(left)+len(right))
	for entryPath := range left {
		seen[entryPath] = true
	}
	for entryPath := range right {
		seen[entryPath] = true
	}
	paths := make([]string, 0, len(seen))
	for entryPath := range seen {
		paths = append(paths, entryPath)
	}
	sort.Strings(paths)
	return paths
}

func sortedFilePaths(files map[string][]byte) []string {
	paths := make([]string, 0, len(files))
	for filePath := range files {
		paths = append(paths, filePath)
	}
	sort.Strings(paths)
	return paths
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type artifactRow struct {
	ArchiveSHA256   string    `gorm:"column:archive_sha256;type:varchar(64);primaryKey"`
	BuiltinSkillUID string    `gorm:"column:builtin_skill_uid;type:varchar(64);not null"`
	Version         string    `gorm:"column:version;type:varchar(64);not null"`
	TreeSHA256      string    `gorm:"column:tree_sha256;type:varchar(64);not null"`
	CreatedAt       time.Time `gorm:"column:created_at;not null"`
}

func (artifactRow) TableName() string { return "skill_distribution_artifacts" }

type artifactEntryRow struct {
	ArchiveSHA256 string  `gorm:"column:archive_sha256;type:varchar(64);primaryKey"`
	Path          string  `gorm:"column:path;type:varchar(1024);primaryKey"`
	EntryType     string  `gorm:"column:entry_type;type:varchar(16);not null"`
	BlobHash      *string `gorm:"column:blob_hash;type:varchar(64)"`
	Size          int64   `gorm:"column:size"`
	Mime          string  `gorm:"column:mime;type:varchar(128)"`
	FileType      string  `gorm:"column:file_type;type:varchar(32);not null;default:'unknown'"`
	Binary        bool    `gorm:"column:binary;not null;default:false"`
	Mode          int     `gorm:"column:mode;not null;default:420"`
}

func (artifactEntryRow) TableName() string { return "skill_distribution_entries" }

type bindingRow struct {
	SkillID              string    `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	BuiltinSkillUID      string    `gorm:"column:builtin_skill_uid;type:varchar(64);not null"`
	CurrentArchiveSHA256 string    `gorm:"column:current_archive_sha256;type:varchar(64);not null"`
	PendingArchiveSHA256 string    `gorm:"column:pending_archive_sha256;type:varchar(64);not null;default:''"`
	Conflicts            []byte    `gorm:"column:conflicts;type:json;not null"`
	CreatedAt            time.Time `gorm:"column:created_at;not null"`
	UpdatedAt            time.Time `gorm:"column:updated_at;not null"`
}

func (bindingRow) TableName() string { return "skill_distribution_bindings" }

type revisionDistributionRow struct {
	RevisionID    string    `gorm:"column:revision_id;type:varchar(36);primaryKey"`
	ArchiveSHA256 string    `gorm:"column:archive_sha256;type:varchar(64);not null"`
	CreatedAt     time.Time `gorm:"column:created_at;not null"`
}

func (revisionDistributionRow) TableName() string { return "skill_revision_distributions" }

type skillRow struct {
	ID                    string     `gorm:"column:id;type:varchar(36);primaryKey"`
	OwnerUserID           string     `gorm:"column:owner_user_id;type:varchar(255);not null"`
	OriginBuiltinSkillUID string     `gorm:"column:origin_builtin_skill_uid;type:varchar(64);not null"`
	HeadRevisionID        *string    `gorm:"column:head_revision_id;type:varchar(36)"`
	UpdateStatus          string     `gorm:"column:update_status;type:varchar(32);not null"`
	DeletedAt             *time.Time `gorm:"column:deleted_at"`
}

func (skillRow) TableName() string { return "skills" }

type revisionRow struct {
	ID               string  `gorm:"column:id;type:varchar(36);primaryKey"`
	SkillID          string  `gorm:"column:skill_id;type:varchar(36);not null"`
	ParentRevisionID *string `gorm:"column:parent_revision_id;type:varchar(36)"`
}

func (revisionRow) TableName() string { return "skill_revisions" }

type revisionSourceRow struct {
	ID          string `gorm:"column:id"`
	SourceRefID string `gorm:"column:source_ref_id"`
	RevisionNo  int64  `gorm:"column:revision_no"`
}

func (revisionSourceRow) TableName() string { return "skill_revisions" }

type revisionEntryRow struct {
	RevisionID string  `gorm:"column:revision_id;type:varchar(36);primaryKey"`
	Path       string  `gorm:"column:path;type:varchar(1024);primaryKey"`
	EntryType  string  `gorm:"column:entry_type;type:varchar(16);not null"`
	BlobHash   *string `gorm:"column:blob_hash;type:varchar(64)"`
	Size       int64   `gorm:"column:size"`
	Mime       string  `gorm:"column:mime;type:varchar(128)"`
	FileType   string  `gorm:"column:file_type;type:varchar(32);not null"`
	Binary     bool    `gorm:"column:binary;not null"`
	Mode       int     `gorm:"column:mode;not null"`
}

func (revisionEntryRow) TableName() string { return "skill_revision_entries" }

type draftRow struct {
	SkillID        string     `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	BaseRevisionID *string    `gorm:"column:base_revision_id;type:varchar(36)"`
	DraftStatus    string     `gorm:"column:draft_status;type:varchar(32);not null"`
	DraftUpdatedAt *time.Time `gorm:"column:draft_updated_at"`
	TaskID         string     `gorm:"column:task_id;type:varchar(128);not null"`
	ConversationID *string    `gorm:"column:conversation_id;type:varchar(128)"`
	Version        int64      `gorm:"column:version;not null"`
	CreatedAt      time.Time  `gorm:"column:created_at;not null"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;not null"`
}

func (draftRow) TableName() string { return "skill_drafts" }

type draftEntryRow struct {
	SkillID   string    `gorm:"column:skill_id;type:varchar(36);primaryKey"`
	Path      string    `gorm:"column:path;type:varchar(1024);primaryKey"`
	Op        string    `gorm:"column:op;type:varchar(16);not null"`
	EntryType string    `gorm:"column:entry_type;type:varchar(16)"`
	BlobHash  *string   `gorm:"column:blob_hash;type:varchar(64)"`
	Size      int64     `gorm:"column:size"`
	Mime      string    `gorm:"column:mime;type:varchar(128)"`
	FileType  string    `gorm:"column:file_type;type:varchar(32)"`
	Binary    bool      `gorm:"column:binary"`
	Mode      int       `gorm:"column:mode"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null"`
}

func (draftEntryRow) TableName() string { return "skill_draft_entries" }
