package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"

	"gorm.io/gorm"

	"lazymind/core/skillv2/metadata"
	"lazymind/core/skillv2/skillpackage"
	"lazymind/core/skillv2/sourceurl"
)

// MaxSkillInstallRequestBytes includes base64 expansion and JSON fields.
const MaxSkillInstallRequestBytes int64 = (MaxSkillDownloadBytes+2)/3*4 + (1 << 20)

type skillInstallOwnerLockEntry struct {
	token chan struct{}
	refs  int
}

var skillInstallOwnerLocks = struct {
	sync.Mutex
	entries map[string]*skillInstallOwnerLockEntry
}{entries: make(map[string]*skillInstallOwnerLockEntry)}

type InstallSkillRequest struct {
	OwnerUserID   string
	OwnerUserName string
	URL           string
	PackageZIP    []byte
	ExpectedName  string
}

type InstallSkillResponse struct {
	Status    string                          `json:"status"`
	SkillID   string                          `json:"skill_id"`
	SkillKey  string                          `json:"skill_key"`
	SourceURL string                          `json:"source_url,omitempty"`
	Enabled   bool                            `json:"enabled"`
	Warnings  []metadata.NormalizationWarning `json:"warnings,omitempty"`
}

// InstallSkill shares the interactive importer and commits a complete disabled
// package. Preparation happens before the owner lock; the source is rechecked
// while holding the lock so concurrent retries cannot overwrite one another.
func (s *SkillService) InstallSkill(ctx context.Context, req InstallSkillRequest) (InstallSkillResponse, error) {
	if s == nil || s.db == nil || strings.TrimSpace(req.OwnerUserID) == "" {
		return InstallSkillResponse{}, fmt.Errorf("user_id is required")
	}
	rawURL := strings.TrimSpace(req.URL)
	if (rawURL == "") == (len(req.PackageZIP) == 0) {
		return InstallSkillResponse{}, fmt.Errorf("provide exactly one of url or package_zip_base64")
	}
	sourceKey := ""
	if rawURL != "" {
		sourceKey = "url:" + normalizedInstallURL(rawURL)
		if existing, found, err := findInstalledSource(ctx, s.db, req.OwnerUserID, sourceKey); err != nil || found {
			return existing, err
		}
	} else {
		if int64(len(req.PackageZIP)) > MaxSkillDownloadBytes {
			return InstallSkillResponse{}, fmt.Errorf("skill package download exceeds %d bytes", MaxSkillDownloadBytes)
		}
		digest := sha256.Sum256(req.PackageZIP)
		sourceKey = "zip:" + hex.EncodeToString(digest[:])
	}
	local := *s
	source := SourceInput{}
	resolvedKey := ""
	if rawURL != "" {
		resolved, err := ResolveURLSource(ctx, rawURL)
		if err != nil {
			return InstallSkillResponse{}, err
		}
		source = resolved
		// Website adapters intentionally discard tracking parameters. Use the
		// resolved download identity for deduplication while keeping rawURL as
		// provenance in the installed revision.
		if canonical := normalizedInstallURL(source.URL); canonical != "" {
			resolvedKey = "resolved:" + canonical
		}
	} else {
		file, err := os.CreateTemp("", "lazymind-install-*.zip")
		if err != nil {
			return InstallSkillResponse{}, err
		}
		defer os.Remove(file.Name())
		if _, err = file.Write(req.PackageZIP); err != nil {
			file.Close()
			return InstallSkillResponse{}, err
		}
		if err = file.Close(); err != nil {
			return InstallSkillResponse{}, err
		}
		local.uploadStore = installUploadStore{UploadSession{UploadID: sourceKey, OwnerUserID: req.OwnerUserID, State: "completed", StoredPath: file.Name(), Filename: "skill.zip"}}
		source = SourceInput{Type: "uploaded_zip", UploadID: sourceKey}
	}
	prepared, _, _, err := local.filesFromSource(ctx, req.OwnerUserID, source)
	if err != nil {
		return InstallSkillResponse{}, err
	}
	if req.ExpectedName != "" {
		if err := skillpackage.NormalizeSkillDocument(prepared.Files); err != nil {
			return InstallSkillResponse{}, err
		}
		parsed, err := metadata.ParseRequired(prepared.Files["SKILL.md"])
		if err != nil {
			return InstallSkillResponse{}, err
		}
		if parsed.Name != req.ExpectedName {
			return InstallSkillResponse{}, fmt.Errorf("SKILL.md frontmatter name must match expected name %q", req.ExpectedName)
		}
	}
	verifiedKey := sourceKey
	if rawURL != "" {
		original := source.ResolvedSourceURL
		if original == "" {
			original = source.SourceURL
		}
		parsed, _ := url.Parse(original)
		if resolved, matched, err := sourceurl.ResolveGitHubPageURLFromResolvedArchive(parsed, source.URL); matched && err == nil {
			verifiedKey = "github:" + strings.ToLower(resolved.Owner+"/"+resolved.Repository) + "/" + source.PathPrefix
		}
	}
	archive, err := skillpackage.WriteZip(prepared.Files, "")
	if err != nil {
		return InstallSkillResponse{}, err
	}
	defer os.Remove(archive)
	// Reuse CreateSkill's validation and normalization against the prepared bytes.
	// This downloader never performs network I/O inside the transaction.
	local.downloader = installPreparedDownloader{archive: archive, packageRoot: prepared.PackageRoot}
	if rawURL == "" {
		local.uploadStore = installUploadStore{UploadSession{UploadID: sourceKey, OwnerUserID: req.OwnerUserID, State: "completed", StoredPath: archive, Filename: "skill.zip"}}
	}
	var out InstallSkillResponse
	// Keep same-process retries serialized even on MySQL, where the fallback
	// UPDATE below cannot lock an owner that has no skill row yet. PostgreSQL
	// still uses its transaction-scoped advisory lock; SQLite serializes its
	// first write. Multi-instance MySQL deployments should provide an
	// equivalent owner lock at the deployment layer.
	ownerUnlock, err := acquireSkillInstallOwnerLock(ctx, req.OwnerUserID)
	if err != nil {
		return InstallSkillResponse{}, err
	}
	defer ownerUnlock()
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockSkillInstallOwner(tx, req.OwnerUserID); err != nil {
			return err
		}
		keys := []string{sourceKey, verifiedKey}
		if resolvedKey != "" && resolvedKey != sourceKey && resolvedKey != verifiedKey {
			keys = append(keys, resolvedKey)
		}
		existing, found, err := findInstalledSource(ctx, tx, req.OwnerUserID, keys...)
		if err != nil {
			return err
		}
		if found {
			out = existing
			return nil
		}
		local.db = tx
		enabled := false
		created, err := local.CreateSkill(ctx, CreateSkillRequest{OwnerUserID: req.OwnerUserID, OwnerUserName: req.OwnerUserName, CreateUserID: req.OwnerUserID, CreateUserName: req.OwnerUserName, IsEnabled: &enabled, Source: source})
		if err != nil {
			return err
		}
		var row skillRow
		if err := tx.Where("id = ?", created.SkillID).Take(&row).Error; err != nil {
			return err
		}
		ext := map[string]any{}
		if len(row.Ext) > 0 {
			if err := json.Unmarshal(row.Ext, &ext); err != nil {
				return err
			}
		}
		ext["import_source_identity"] = verifiedKey
		ext["import_source_url"] = rawURL
		if resolvedKey != "" {
			ext["import_source_resolved_identity"] = resolvedKey
		}
		encoded, err := json.Marshal(ext)
		if err != nil {
			return err
		}
		if err := tx.Model(&skillRow{}).Where("id = ?", created.SkillID).Update("ext", encoded).Error; err != nil {
			return err
		}
		out = InstallSkillResponse{Status: "installed", SkillID: created.SkillID, SkillKey: path.Join(created.Category, created.SkillName), SourceURL: rawURL, Enabled: false, Warnings: created.Warnings}
		return nil
	})
	return out, err
}

func acquireSkillInstallOwnerLock(ctx context.Context, owner string) (func(), error) {
	skillInstallOwnerLocks.Lock()
	entry := skillInstallOwnerLocks.entries[owner]
	if entry == nil {
		entry = &skillInstallOwnerLockEntry{token: make(chan struct{}, 1)}
		entry.token <- struct{}{}
		skillInstallOwnerLocks.entries[owner] = entry
	}
	entry.refs++
	skillInstallOwnerLocks.Unlock()
	select {
	case <-entry.token:
		return func() {
			entry.token <- struct{}{}
			skillInstallOwnerLocks.Lock()
			entry.refs--
			if entry.refs == 0 {
				delete(skillInstallOwnerLocks.entries, owner)
			}
			skillInstallOwnerLocks.Unlock()
		}, nil
	case <-ctx.Done():
		skillInstallOwnerLocks.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(skillInstallOwnerLocks.entries, owner)
		}
		skillInstallOwnerLocks.Unlock()
		return nil, ctx.Err()
	}
}

func lockSkillInstallOwner(tx *gorm.DB, owner string) error {
	if tx.Dialector.Name() == "postgres" {
		return tx.Exec("SELECT pg_advisory_xact_lock(hashtext(?))", "skill-install:"+owner).Error
	}
	// SQLite's first write acquires the writer lock, including an empty result.
	return tx.Exec("UPDATE skills SET updated_at = updated_at WHERE owner_user_id = ?", owner).Error
}

func findInstalledSource(ctx context.Context, db *gorm.DB, owner string, keys ...string) (InstallSkillResponse, bool, error) {
	var rows []struct {
		ID            string
		Category      string
		SkillName     string
		IsEnabled     bool
		Ext           []byte
		SourceRefID   string `gorm:"column:source_ref_id"`
		SourceRefType string `gorm:"column:source_ref_type"`
	}
	// Only the current head revision carries the provenance for an installed
	// source. Joining every historical revision makes the result nondeterministic
	// after an edit and can report an old SourceURL on an idempotent retry.
	err := db.WithContext(ctx).Table("skills").Select("skills.*, skill_revisions.source_ref_id, skill_revisions.source_ref_type").Joins("LEFT JOIN skill_revisions ON skill_revisions.id = skills.head_revision_id").Where("skills.owner_user_id = ? AND skills.category = ? AND skills.deleted_at IS NULL", owner, metadata.ExternalCategory).Find(&rows).Error
	if err != nil {
		return InstallSkillResponse{}, false, err
	}
	for _, row := range rows {
		ext := map[string]any{}
		_ = json.Unmarshal(row.Ext, &ext)
		for _, key := range keys {
			match := ext["import_source_identity"] == key || ext["import_source_resolved_identity"] == key
			if strings.HasPrefix(key, "url:") {
				match = match || normalizedInstallURL(fmt.Sprint(ext["import_source_url"])) == strings.TrimPrefix(key, "url:") || (row.SourceRefType == "url" && normalizedInstallURL(row.SourceRefID) == strings.TrimPrefix(key, "url:"))
			}
			if match {
				sourceURL := row.SourceRefID
				if raw, ok := ext["import_source_url"].(string); ok && strings.TrimSpace(raw) != "" {
					sourceURL = raw
				}
				return InstallSkillResponse{Status: "already_installed", SkillID: row.ID, SkillKey: path.Join(row.Category, row.SkillName), SourceURL: sourceURL, Enabled: row.IsEnabled}, true, nil
			}
		}
	}
	return InstallSkillResponse{}, false, nil
}

func normalizedInstallURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return strings.TrimSpace(raw)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	if strings.TrimPrefix(parsed.Host, "www.") == "github.com" {
		parsed.Host = "github.com"
		parsed.RawQuery = ""
		parsed.Path = strings.TrimSuffix(parsed.Path, "/")
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) == 2 {
			parsed.Path = "/" + parts[0] + "/" + strings.TrimSuffix(parts[1], ".git")
		}
	}
	return parsed.String()
}

type installUploadStore struct{ session UploadSession }

func (s installUploadStore) Get(_ context.Context, id string) (UploadSession, error) {
	if id != s.session.UploadID {
		return UploadSession{}, fmt.Errorf("upload session not found")
	}
	return s.session, nil
}

type installPreparedDownloader struct{ archive, packageRoot string }

func (d installPreparedDownloader) Download(context.Context, string) (DownloadedZip, error) {
	prefix := ""
	return DownloadedZip{Path: d.archive, PathPrefix: &prefix, PackageRoot: d.packageRoot}, nil
}
func (d installPreparedDownloader) DownloadSource(ctx context.Context, _ SourceInput) (DownloadedZip, error) {
	return d.Download(ctx, "")
}
