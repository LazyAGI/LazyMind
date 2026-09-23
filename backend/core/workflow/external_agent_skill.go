package workflow

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
	skillmetadata "lazymind/core/skillv2/metadata"
	skillservice "lazymind/core/skillv2/service"
	skillurl "lazymind/core/skillv2/sourceurl"
)

type externalAgentWorkflowSkillSource struct {
	RequestSkill externalAgentWorkflowSkillInput
	SourceType   string
	SourceKey    string
	SourceURL    string
	ZipContent   []byte
}

type externalAgentSkillResolution struct {
	SkillID       string
	SourceType    string
	SourceKey     string
	InstallStatus string
}

type externalSkillSourceError struct {
	Code       string
	Message    string
	Suggestion string
}

func (e externalSkillSourceError) Error() string { return e.Message }

func normalizeExternalAgentWorkflowSkillSource(ctx context.Context, in externalAgentWorkflowSkillInput) (externalAgentWorkflowSkillSource, error) {
	name := strings.TrimSpace(in.Name)
	rawURL := strings.TrimSpace(in.URL)
	encodedZip := strings.TrimSpace(in.ZipBase64)
	providedHash := strings.TrimSpace(in.ZipSHA256)
	if name == "" {
		return externalAgentWorkflowSkillSource{}, externalSkillSourceError{Code: "SKILL_SOURCE_REQUIRED", Message: "skill.name is required", Suggestion: "Provide skill.name and either skill.url or skill.zip_base64."}
	}
	if rawURL == "" && encodedZip == "" {
		return externalAgentWorkflowSkillSource{}, externalSkillSourceError{Code: "SKILL_SOURCE_REQUIRED", Message: "skill.url or skill.zip_base64 is required", Suggestion: "Provide exactly one Skill source: URL or zip_base64."}
	}
	if rawURL != "" && encodedZip != "" {
		return externalAgentWorkflowSkillSource{}, externalSkillSourceError{Code: "SKILL_SOURCE_INVALID", Message: "skill.url and skill.zip_base64 are mutually exclusive", Suggestion: "Provide either skill.url or skill.zip_base64, not both."}
	}
	if rawURL != "" {
		parsed, err := url.ParseRequestURI(rawURL)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil {
			return externalAgentWorkflowSkillSource{}, externalSkillSourceError{Code: "SKILL_SOURCE_INVALID", Message: "skill.url must be an HTTP/HTTPS URL without embedded credentials", Suggestion: "Provide an HTTP/HTTPS SkillHub, GitHub, or direct zip URL."}
		}
		if providedHash != "" {
			return externalAgentWorkflowSkillSource{}, externalSkillSourceError{Code: "INVALID_REQUEST", Message: "skill.zip_sha256 requires skill.zip_base64", Suggestion: "Omit zip_sha256 for URL sources."}
		}
		_, _, resolveErr := skillurl.ResolveSkillHubPageURL(parsed)
		if resolveErr != nil {
			return externalAgentWorkflowSkillSource{}, externalSkillSourceError{Code: "SKILL_SOURCE_INVALID", Message: resolveErr.Error(), Suggestion: "Provide an HTTP/HTTPS SkillHub, GitHub, or direct zip URL."}
		}
		rawURL = canonicalExternalSkillURL(rawURL)
		sourceKey := "sha256:" + sha256Hex([]byte(rawURL))
		return externalAgentWorkflowSkillSource{
			RequestSkill: externalAgentWorkflowSkillInput{Name: name, URL: rawURL},
			SourceType:   "url",
			SourceKey:    sourceKey,
			SourceURL:    rawURL,
		}, nil
	}
	content, err := base64.StdEncoding.DecodeString(encodedZip)
	if err != nil {
		return externalAgentWorkflowSkillSource{}, externalSkillSourceError{Code: "SKILL_ZIP_INVALID", Message: "skill.zip_base64 is invalid", Suggestion: "Base64-encode the Skill zip package and retry."}
	}
	if len(content) == 0 {
		return externalAgentWorkflowSkillSource{}, externalSkillSourceError{Code: "SKILL_ZIP_INVALID", Message: "skill.zip_base64 is empty", Suggestion: "Provide a non-empty Skill zip package."}
	}
	if int64(len(content)) > maxExternalAgentSkillDownloadBytes {
		return externalAgentWorkflowSkillSource{}, externalSkillSourceError{Code: "SKILL_ZIP_TOO_LARGE", Message: "Skill ZIP must not exceed 20 MiB", Suggestion: "Remove unnecessary files from the Skill package."}
	}
	hash := "sha256:" + sha256Hex(content)
	if providedHash != "" && providedHash != hash {
		return externalAgentWorkflowSkillSource{}, externalSkillSourceError{Code: "SKILL_ZIP_HASH_MISMATCH", Message: "skill.zip_sha256 does not match zip_base64 content", Suggestion: "Omit zip_sha256 or provide the SHA256 of the decoded zip content."}
	}
	return externalAgentWorkflowSkillSource{
		RequestSkill: externalAgentWorkflowSkillInput{Name: name, ZipBase64: encodedZip, ZipSHA256: hash},
		SourceType:   "zip",
		SourceKey:    hash,
		ZipContent:   content,
	}, nil
}

func resolveExternalAgentSkillSource(ctx context.Context, db *gorm.DB, ownerUserID, ownerUserName string, in externalAgentWorkflowSkillInput) (externalAgentSkillResolution, error) {
	source, err := normalizeExternalAgentWorkflowSkillSource(ctx, in)
	if err != nil {
		return externalAgentSkillResolution{}, err
	}
	if skillID := findExternalAgentSkillBySource(ctx, db, ownerUserID, source); skillID != "" {
		if err := recordExternalAgentSkillSource(ctx, db, ownerUserID, source, skillID, "reused"); err != nil {
			return externalAgentSkillResolution{}, err
		}
		return externalAgentSkillResolution{SkillID: skillID, SourceType: source.SourceType, SourceKey: source.SourceKey, InstallStatus: "reused"}, nil
	}
	skillID, err := installExternalAgentSkillSource(ctx, db, ownerUserID, ownerUserName, source)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "skill already exists") {
			if existing := findExternalAgentSkillBySource(ctx, db, ownerUserID, source); existing != "" {
				if recordErr := recordExternalAgentSkillSource(ctx, db, ownerUserID, source, existing, "reused"); recordErr != nil {
					return externalAgentSkillResolution{}, recordErr
				}
				return externalAgentSkillResolution{SkillID: existing, SourceType: source.SourceType, SourceKey: source.SourceKey, InstallStatus: "reused"}, nil
			}
			return externalAgentSkillResolution{}, externalSkillSourceError{Code: "SKILL_NAME_CONFLICT", Message: "A different Skill source already uses this name", Suggestion: "Provide a distinct skill.name or the original source URL/ZIP."}
		}
		return externalAgentSkillResolution{}, externalSkillSourceError{Code: "SKILL_INSTALL_FAILED", Message: err.Error(), Suggestion: "Confirm the Skill source is accessible, valid, and contains SKILL.md."}
	}
	if err := recordExternalAgentSkillSource(ctx, db, ownerUserID, source, skillID, "installed"); err != nil {
		return externalAgentSkillResolution{}, err
	}
	return externalAgentSkillResolution{SkillID: skillID, SourceType: source.SourceType, SourceKey: source.SourceKey, InstallStatus: "installed"}, nil
}

func findExternalAgentSkillBySource(ctx context.Context, db *gorm.DB, ownerUserID string, source externalAgentWorkflowSkillSource) string {
	var mapping orm.ExternalAgentSkillSource
	err := db.WithContext(ctx).
		Joins("JOIN skills ON skills.id = external_agent_skill_sources.resolved_skill_id AND skills.owner_user_id = external_agent_skill_sources.owner_user_id AND skills.deleted_at IS NULL").
		Where("external_agent_skill_sources.owner_user_id=? AND external_agent_skill_sources.source_type=? AND external_agent_skill_sources.source_key=?",
			ownerUserID, source.SourceType, source.SourceKey).
		First(&mapping).Error
	if err == nil && mapping.ResolvedSkillID != "" {
		return mapping.ResolvedSkillID
	}
	refType, refID := sourceRevisionRef(source)
	if refID != "" {
		var revision orm.SkillV2Revision
		err = db.WithContext(ctx).
			Joins("JOIN skills ON skills.id = skill_revisions.skill_id AND skills.owner_user_id = ? AND skills.deleted_at IS NULL", ownerUserID).
			Where("skill_revisions.source_ref_type=? AND skill_revisions.source_ref_id=?", refType, refID).
			Order("skill_revisions.created_at DESC").
			First(&revision).Error
		if err == nil && revision.SkillID != "" {
			return revision.SkillID
		}
	}
	return ""
}

func installExternalAgentSkillSource(ctx context.Context, db *gorm.DB, ownerUserID, ownerUserName string, source externalAgentWorkflowSkillSource) (string, error) {
	uploadStore := skillservice.UploadStore(nil)
	serviceSource := skillservice.SourceInput{}
	switch source.SourceType {
	case "url":
		downloadURL, prefix, err := normalizeExternalSkillImportURL(ctx, source.SourceURL)
		if err != nil {
			return "", err
		}
		serviceSource = skillservice.SourceInput{Type: "url", URL: downloadURL, SourceURL: source.SourceURL, PathPrefix: prefix}
	case "zip":
		zipPath, err := writeExternalAgentSkillZip(source.ZipContent)
		if err != nil {
			return "", err
		}
		defer os.Remove(zipPath)
		uploadStore = externalAgentZipUploadStore{session: skillservice.UploadSession{
			UploadID: source.SourceKey, OwnerUserID: ownerUserID, State: "completed",
			StoredPath: zipPath, Filename: externalSkillArchiveFilename(source.RequestSkill.Name),
		}}
		serviceSource = skillservice.SourceInput{Type: "uploaded_zip", UploadID: source.SourceKey, Filename: externalSkillArchiveFilename(source.RequestSkill.Name)}
	default:
		return "", fmt.Errorf("unsupported skill source type %q", source.SourceType)
	}
	service := skillservice.NewSkillService(skillservice.SkillServiceDeps{
		DB:          db,
		UploadStore: uploadStore,
		Downloader:  skillservice.HTTPZipDownloader{},
		BlobStore:   skillservice.NewBlobStore(db, skillservice.NewLocalObjectStore(externalSkillObjectRoot())),
	})
	resp, err := service.CreateSkill(ctx, skillservice.CreateSkillRequest{
		OwnerUserID: ownerUserID, OwnerUserName: ownerUserName,
		CreateUserID: ownerUserID, CreateUserName: ownerUserName,
		Name: source.RequestSkill.Name, Category: skillmetadata.ExternalCategory,
		Description: "Imported by external Agent Skill-to-Workflow task.",
		IsEnabled:   boolPtr(true), Source: serviceSource,
	})
	if err != nil {
		return "", err
	}
	return resp.SkillID, nil
}

func recordExternalAgentSkillSource(ctx context.Context, db *gorm.DB, ownerUserID string, source externalAgentWorkflowSkillSource, skillID, status string) error {
	now := time.Now().UTC()
	row := orm.ExternalAgentSkillSource{
		ID: uuid.NewString(), OwnerUserID: ownerUserID, SourceType: source.SourceType, SourceKey: source.SourceKey,
		SourceName: source.RequestSkill.Name, SourceURL: source.SourceURL, ResolvedSkillID: skillID,
		InstallStatus: status, CreatedAt: now, UpdatedAt: now,
	}
	return db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "owner_user_id"}, {Name: "source_type"}, {Name: "source_key"}},
		DoUpdates: clause.Assignments(map[string]any{
			"source_name": row.SourceName, "source_url": row.SourceURL, "resolved_skill_id": row.ResolvedSkillID,
			"install_status": row.InstallStatus, "updated_at": row.UpdatedAt,
		}),
	}).Create(&row).Error
}

func normalizeExternalSkillImportURL(ctx context.Context, rawURL string) (string, string, error) {
	parsed, err := url.ParseRequestURI(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", "", fmt.Errorf("invalid skill import URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", "", fmt.Errorf("skill import URL must use HTTP or HTTPS")
	}
	resolution, matched, resolveErr := skillurl.ResolveSkillHubPageURL(parsed)
	if resolveErr != nil {
		return "", "", resolveErr
	}
	if matched {
		return resolution.DownloadURL, "", nil
	}
	githubResolution, matched, resolveErr := skillurl.ResolveGitHubPageURL(ctx, parsed, &http.Client{Timeout: 10 * time.Second}, skillurl.GitHubAPIBaseURL)
	if resolveErr != nil {
		return "", "", resolveErr
	}
	if matched {
		return githubResolution.DownloadURL, githubResolution.PathPrefix, nil
	}
	return rawURL, "", nil
}

func canonicalExternalSkillURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return strings.TrimSpace(rawURL)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	parsed.Fragment = ""
	return parsed.String()
}

func sourceRevisionRef(source externalAgentWorkflowSkillSource) (string, string) {
	switch source.SourceType {
	case "url":
		return "url", source.SourceURL
	case "zip":
		return "upload", source.SourceKey
	default:
		return "", ""
	}
}

func writeExternalAgentSkillZip(content []byte) (string, error) {
	f, err := os.CreateTemp("", "lazymind-external-skill-*.zip")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

func externalSkillArchiveFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "external-agent-skill"
	}
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, `\`, "-")
	return name + ".zip"
}

func externalSkillObjectRoot() string {
	if v := strings.TrimSpace(os.Getenv("LAZYMIND_SKILL_OBJECT_ROOT")); v != "" {
		return strings.TrimRight(v, "/")
	}
	if v := strings.TrimSpace(os.Getenv("LAZYMIND_UPLOAD_ROOT")); v != "" {
		return filepath.Join(strings.TrimRight(v, "/"), "skill-objects")
	}
	return filepath.Join("/var/lib/lazymind/uploads", "skill-objects")
}

type externalAgentZipUploadStore struct {
	session skillservice.UploadSession
}

func (s externalAgentZipUploadStore) Get(_ context.Context, uploadID string) (skillservice.UploadSession, error) {
	if uploadID != s.session.UploadID {
		return skillservice.UploadSession{}, fmt.Errorf("upload session not found")
	}
	return s.session, nil
}

const maxExternalAgentSkillDownloadBytes = skillservice.MaxSkillDownloadBytes
