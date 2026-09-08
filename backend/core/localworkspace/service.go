package localworkspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/systemdeps"
)

const (
	StatusActive          = "active"
	StatusRevoked         = "revoked"
	StatusPathUnavailable = "path_unavailable"
	ReadPolicyAllow       = "allow"
	WritePolicyAllow      = "allow"
	PermissionAlwaysAsk   = "always_ask"
	PermissionAskAsNeeded = "ask_as_needed"
	PermissionAllowAll    = "allow_all"
)

type PublicWorkspace struct {
	WorkspaceID       string     `json:"workspace_id"`
	DisplayName       string     `json:"display_name"`
	Path              string     `json:"path"`
	Status            string     `json:"status"`
	Version           int64      `json:"version"`
	Source            string     `json:"source"`
	ReadPolicy        string     `json:"read_policy"`
	WritePolicy       string     `json:"write_policy"`
	AffectedTaskCount int64      `json:"affected_task_count"`
	AuthorizedAt      time.Time  `json:"authorized_at"`
	LastUsedAt        time.Time  `json:"last_used_at"`
	RevokedAt         *time.Time `json:"revoked_at,omitempty"`
}

type BindingView struct {
	Status            string           `json:"status"`
	WorkspaceID       string           `json:"workspace_id,omitempty"`
	Workspace         *PublicWorkspace `json:"workspace,omitempty"`
	AffectedTaskCount int64            `json:"affected_task_count,omitempty"`
	PermissionMode    string           `json:"permission_mode,omitempty"`
	PermissionVersion int64            `json:"permission_version,omitempty"`
}

type RegisterInput struct {
	DisplayName       string `json:"display_name"`
	CanonicalPath     string `json:"canonical_path"`
	DirectoryIdentity string `json:"directory_identity"`
	Source            string `json:"source"`
}

func Enabled() bool { return systemdeps.IsLocalRuntime() }

func ValidPermissionMode(value string) bool {
	return value == PermissionAlwaysAsk || value == PermissionAskAsNeeded || value == PermissionAllowAll
}

func Register(ctx context.Context, db *gorm.DB, userID string, input RegisterInput) (PublicWorkspace, error) {
	if db == nil {
		return PublicWorkspace{}, errors.New("store not initialized")
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	input.CanonicalPath = filepath.Clean(strings.TrimSpace(input.CanonicalPath))
	input.DirectoryIdentity = strings.TrimSpace(input.DirectoryIdentity)
	input.Source = strings.ToLower(strings.TrimSpace(input.Source))
	if userID == "" || input.DisplayName == "" || !filepath.IsAbs(input.CanonicalPath) ||
		(input.Source != "local" && input.Source != "desktop") {
		return PublicWorkspace{}, Error("path_invalid", 400, "invalid request")
	}
	identity, err := currentDirectoryIdentity(input.CanonicalPath)
	if err != nil || (input.DirectoryIdentity != "" && identity != input.DirectoryIdentity) {
		return PublicWorkspace{}, Error("path_invalid", 400, "invalid request")
	}
	now := time.Now().UTC()
	var row orm.LocalWorkspace
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Where("create_user_id = ? AND canonical_path = ? AND directory_identity = ? AND status = ?",
			userID, input.CanonicalPath, identity, StatusActive).First(&row).Error
		if err == nil {
			return tx.Model(&row).Updates(map[string]any{"display_name": input.DisplayName, "last_used_at": now, "updated_at": now}).Error
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		id, err := newID()
		if err != nil {
			return err
		}
		row = orm.LocalWorkspace{ID: id, CreateUserID: userID, DisplayName: input.DisplayName,
			CanonicalPath: input.CanonicalPath, DirectoryIdentity: identity, Status: StatusActive,
			Version: 1, Source: input.Source, ReadPolicy: ReadPolicyAllow, WritePolicy: WritePolicyAllow,
			AuthorizedAt: now, LastUsedAt: now, CreatedAt: now, UpdatedAt: now}
		return tx.Create(&row).Error
	})
	if err != nil {
		return PublicWorkspace{}, err
	}
	row.DisplayName, row.LastUsedAt, row.UpdatedAt = input.DisplayName, now, now
	return publicWorkspace(row), nil
}

func ResolveActiveForBinding(ctx context.Context, db *gorm.DB, userID, workspaceID string) (orm.LocalWorkspace, error) {
	var row orm.LocalWorkspace
	if db == nil {
		return row, errors.New("store not initialized")
	}
	err := db.WithContext(ctx).Where("id = ? AND create_user_id = ?", workspaceID, userID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return row, Error("workspace_not_found", 404, "resource not found")
	}
	if err != nil {
		return row, err
	}
	switch row.Status {
	case StatusRevoked:
		return row, Error("revoked", 409, "conflict")
	case StatusPathUnavailable:
		return row, Error("path_unavailable", 409, "conflict")
	case StatusActive:
		if identity, identityErr := currentDirectoryIdentity(row.CanonicalPath); identityErr == nil && identity == row.DirectoryIdentity {
			return row, nil
		}
		result := db.WithContext(ctx).Model(&orm.LocalWorkspace{}).
			Where("id = ? AND status = ? AND version = ?", row.ID, StatusActive, row.Version).
			Updates(map[string]any{"status": StatusPathUnavailable, "version": gorm.Expr("version + 1"), "updated_at": time.Now().UTC()})
		if result.Error != nil {
			return row, result.Error
		}
		return row, Error("path_unavailable", 409, "conflict")
	default:
		return row, Error("path_unavailable", 409, "conflict")
	}
}

func publicWorkspace(row orm.LocalWorkspace) PublicWorkspace {
	return PublicWorkspace{WorkspaceID: row.ID, DisplayName: row.DisplayName, Path: row.CanonicalPath,
		Status: row.Status, Version: row.Version, Source: row.Source, ReadPolicy: row.ReadPolicy,
		WritePolicy: row.WritePolicy, AuthorizedAt: row.AuthorizedAt, LastUsedAt: row.LastUsedAt, RevokedAt: row.RevokedAt}
}

func newID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "lws_" + hex.EncodeToString(buf), nil
}
