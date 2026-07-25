package currentmemory

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"

	"lazymind/core/common/orm"
)

const (
	SoulAvatarPath    = "memory/agents/avatar"
	ProfileAvatarPath = "memory/users/avatar"
	AvatarMaxSize     = 2 << 20
)

type AvatarKind string

const (
	AvatarKindSoul    AvatarKind = "soul"
	AvatarKindProfile AvatarKind = "profile"
)

var ErrCorruptAvatar = errors.New("stored current memory avatar is invalid")

type CurrentMemoryAvatarData struct {
	Kind        AvatarKind `json:"kind"`
	ContentType string     `json:"content_type"`
	Size        int64      `json:"size"`
	UpdatedAt   int64      `json:"updated_at"`
}

func AvatarPath(kind AvatarKind) (string, bool) {
	switch kind {
	case AvatarKindSoul:
		return SoulAvatarPath, true
	case AvatarKindProfile:
		return ProfileAvatarPath, true
	default:
		return "", false
	}
}

func DetectAvatarContentType(content []byte) (string, bool) {
	if len(content) == 0 {
		return "", false
	}
	detected := strings.ToLower(strings.TrimSpace(http.DetectContentType(content)))
	switch detected {
	case "image/png", "image/jpeg", "image/webp":
		return detected, true
	default:
		return "", false
	}
}

func (m *Module) GetAvatar(
	ctx context.Context,
	userID string,
	kind AvatarKind,
) (orm.MemoryCurrentEntry, error) {
	entryPath, ok := AvatarPath(kind)
	if !ok {
		return orm.MemoryCurrentEntry{}, ErrInvalidRequest
	}
	if err := m.repository.EnsureInitialized(ctx, userID); err != nil {
		return orm.MemoryCurrentEntry{}, err
	}
	entry, err := m.repository.GetEntry(ctx, userID, entryPath)
	if err != nil {
		return orm.MemoryCurrentEntry{}, err
	}
	contentType, supported := DetectAvatarContentType(entry.Content)
	if !supported ||
		entry.EntryType != EntryFile ||
		!entry.Binary ||
		entry.Size != int64(len(entry.Content)) ||
		entry.Size > AvatarMaxSize ||
		entry.Mime != contentType {
		return orm.MemoryCurrentEntry{}, ErrCorruptAvatar
	}
	return entry, nil
}

func (m *Module) PutAvatar(
	ctx context.Context,
	userID string,
	kind AvatarKind,
	content []byte,
) (CurrentMemoryAvatarData, error) {
	entryPath, ok := AvatarPath(kind)
	if !ok {
		return CurrentMemoryAvatarData{}, ErrInvalidRequest
	}
	if len(content) == 0 || len(content) > AvatarMaxSize {
		return CurrentMemoryAvatarData{}, ErrInvalidRequest
	}
	contentType, supported := DetectAvatarContentType(content)
	if !supported {
		return CurrentMemoryAvatarData{}, ErrInvalidRequest
	}
	if err := m.repository.EnsureInitialized(ctx, userID); err != nil {
		return CurrentMemoryAvatarData{}, err
	}
	now := m.clock().UTC()
	entry := orm.MemoryCurrentEntry{
		UserID:    strings.TrimSpace(userID),
		Path:      entryPath,
		EntryType: EntryFile,
		Content:   bytes.Clone(content),
		Size:      int64(len(content)),
		Mime:      contentType,
		FileType:  strings.TrimPrefix(contentType, "image/"),
		Binary:    true,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := m.repository.UpsertEntry(ctx, entry); err != nil {
		return CurrentMemoryAvatarData{}, err
	}
	return CurrentMemoryAvatarData{
		Kind:        kind,
		ContentType: contentType,
		Size:        entry.Size,
		UpdatedAt:   formatUpdatedAt(now),
	}, nil
}

func (m *Module) DeleteAvatar(
	ctx context.Context,
	userID string,
	kind AvatarKind,
) error {
	entryPath, ok := AvatarPath(kind)
	if !ok {
		return ErrInvalidRequest
	}
	if err := m.repository.EnsureInitialized(ctx, userID); err != nil {
		return err
	}
	return m.repository.DeletePath(ctx, userID, entryPath)
}
