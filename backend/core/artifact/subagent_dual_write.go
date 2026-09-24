package artifact

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

type SubAgentSnapshot struct {
	TaskID, ConversationID, TriggerHistoryID, OwnerUserID, WorkspacePath string
	AgentType                                                            string
}

type SubAgentLegacyArtifact struct {
	ID, Slot, ContentType string
	Value                 json.RawMessage
	Seq                   int
	Caption               *string
}

func DualWriteSubAgent(ctx context.Context, svc *Service, task SubAgentSnapshot, row SubAgentLegacyArtifact) (*RevisionView, error) {
	if !Enabled() || svc == nil {
		return nil, ErrDisabled
	}
	if task.AgentType == "workflow_step" || strings.TrimSpace(task.OwnerUserID) == "" || strings.TrimSpace(task.TaskID) == "" || strings.TrimSpace(row.ID) == "" {
		return nil, ErrAccessDenied
	}
	if existing, err := svc.FindByLegacyBinding(ctx, ScopeSubAgentLegacyRow, row.ID); err == nil {
		rev, _, readErr := svc.GetRevision(ctx, task.OwnerUserID, existing.RevisionID)
		if readErr != nil {
			return nil, ErrNotFound
		}
		return &RevisionView{ArtifactID: existing.ArtifactID, RevisionID: rev.ID, RevisionNo: rev.RevisionNo}, nil
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	req, err := SnapshotSubAgentValue(svc.DB, task, row)
	if err != nil {
		return nil, err
	}
	return svc.CommitRevision(ctx, req)
}

// ReplaySubAgentArtifact replays one owner-scoped legacy row through the exact
// same snapshot path as live dual-write. It accepts no path or URL input.
func ReplaySubAgentArtifact(ctx context.Context, db *gorm.DB, ownerUserID, legacyID string) (*RevisionView, error) {
	if db == nil || strings.TrimSpace(ownerUserID) == "" || strings.TrimSpace(legacyID) == "" {
		return nil, ErrAccessDenied
	}
	var row orm.SubAgentArtifact
	if err := db.WithContext(ctx).Where("id = ?", legacyID).Take(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var task orm.SubAgentTask
	if err := db.WithContext(ctx).Where("id = ? AND create_user_id = ?", row.TaskID, ownerUserID).Take(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrAccessDenied
		}
		return nil, err
	}
	return DualWriteSubAgent(ctx, New(db), SubAgentSnapshot{
		TaskID: task.ID, ConversationID: task.ConversationID, TriggerHistoryID: task.TriggerHistoryID,
		OwnerUserID: task.CreateUserID, WorkspacePath: task.WorkspacePath, AgentType: task.AgentType,
	}, SubAgentLegacyArtifact{ID: row.ID, Slot: row.Slot, ContentType: row.ContentType, Value: row.Value, Seq: row.Seq, Caption: row.Caption})
}

func SnapshotSubAgentValue(db *gorm.DB, task SubAgentSnapshot, row SubAgentLegacyArtifact) (CommitRequest, error) {
	if !json.Valid(row.Value) {
		return CommitRequest{}, fmt.Errorf("invalid json")
	}
	metadata := map[string]any{"legacy_content_type": row.ContentType, "legacy_seq": row.Seq, "legacy_slot": row.Slot}
	req := CommitRequest{
		TenantID: task.OwnerUserID, OwnerUserID: task.OwnerUserID,
		LogicalKey: "subagent/" + task.TaskID + "/" + row.ID,
		Title:      subAgentTitle(row), Kind: KindFile,
		IdempotencyKey: "legacy-subagent/" + row.ID,
		ProducerType:   ProducerSubAgent, ProducerID: task.TaskID, ProducerRunID: task.TaskID, ProducerEventID: row.ID,
		Channel: ChannelPublished, ContentType: row.ContentType, Caption: row.Caption,
		Bindings: subAgentBindings(task, row),
	}
	switch row.ContentType {
	case "text":
		req.InlineJSON, req.MIMEType = append(json.RawMessage(nil), row.Value...), "text/plain"
		metadata["snapshot_format"] = "inline"
	case "json":
		req.InlineJSON, req.MIMEType = append(json.RawMessage(nil), row.Value...), "application/json"
		metadata["snapshot_format"] = "inline"
	case "file", "image":
		file, filename, err := resolveSubAgentFile(task.WorkspacePath, row.Value)
		if err != nil {
			return CommitRequest{}, err
		}
		mime := mimeForSubAgentFile(filename, row.ContentType)
		blobID, err := ingestFileBlob(db, task.OwnerUserID, mime, file)
		if err != nil {
			return CommitRequest{}, err
		}
		req.BlobID, req.Title, req.MIMEType = blobID, filename, mime
		metadata["snapshot_format"] = "blob"
	case "file_list":
		data, err := zipSubAgentFiles(task.WorkspacePath, row.Value)
		if err != nil {
			return CommitRequest{}, err
		}
		req.Content, req.Title, req.MIMEType = data, safeSlot(row.Slot)+".zip", "application/zip"
		metadata["snapshot_format"] = "zip"
	default:
		return CommitRequest{}, fmt.Errorf("unsupported artifact content type")
	}
	req.Metadata, _ = json.Marshal(metadata)
	return req, nil
}

func subAgentBindings(task SubAgentSnapshot, row SubAgentLegacyArtifact) []BindingSpec {
	bindings := []BindingSpec{
		{ScopeType: ScopeTask, ScopeID: task.TaskID, Role: RoleOutput, SlotKey: row.Slot, FollowHead: true},
		{ScopeType: ScopeSubAgentLegacyRow, ScopeID: row.ID, Role: RoleOutput, FollowHead: false},
	}
	if task.ConversationID != "" {
		bindings = append(bindings, BindingSpec{ScopeType: ScopeConversation, ScopeID: task.ConversationID, Role: RoleOutput, SlotKey: row.Slot, FollowHead: true})
	}
	if task.TriggerHistoryID != "" {
		bindings = append(bindings, BindingSpec{ScopeType: ScopeHistory, ScopeID: task.TriggerHistoryID, Role: RoleOutput, SlotKey: row.Slot, FollowHead: false})
	}
	return bindings
}

func subAgentTitle(row SubAgentLegacyArtifact) string {
	return safeSlot(row.Slot) + map[string]string{"text": ".txt", "json": ".json"}[row.ContentType]
}
func safeSlot(slot string) string {
	if value := filepath.Base(strings.TrimSpace(slot)); value != "." && value != "" {
		return value
	}
	return "artifact"
}

func resolveSubAgentFile(workspace string, raw json.RawMessage) (string, string, error) {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return "", "", ErrAccessDenied
	}
	path, _ := value["path"].(string)
	file, err := safeWorkspaceFile(workspace, path)
	if err != nil {
		return "", "", err
	}
	name, _ := value["filename"].(string)
	if name = filepath.Base(strings.TrimSpace(name)); name == "." || name == "" {
		name = filepath.Base(file)
	}
	return file, name, nil
}

func safeWorkspaceFile(workspace, path string) (string, error) {
	if strings.TrimSpace(workspace) == "" || strings.TrimSpace(path) == "" || strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") || strings.HasPrefix(path, "data:") {
		return "", ErrAccessDenied
	}
	root, err := filepath.EvalSymlinks(filepath.Clean(workspace))
	if err != nil {
		return "", ErrAccessDenied
	}
	candidate := filepath.Clean(path)
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	if info, err := os.Lstat(candidate); err != nil || info.Mode()&os.ModeSymlink != 0 {
		return "", ErrAccessDenied
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", ErrAccessDenied
	}
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrAccessDenied
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", ErrAccessDenied
	}
	return resolved, nil
}

func zipSubAgentFiles(workspace string, raw json.RawMessage) ([]byte, error) {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return nil, ErrAccessDenied
	}
	paths, ok := value["paths"].([]any)
	if !ok || len(paths) == 0 {
		return nil, ErrAccessDenied
	}
	files := make([]string, 0, len(paths))
	var total int64
	for _, item := range paths {
		path, ok := item.(string)
		if !ok {
			return nil, ErrAccessDenied
		}
		file, err := safeWorkspaceFile(workspace, path)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(file)
		if err != nil {
			return nil, err
		}
		total += info.Size()
		if total > maxShadowBlobBytes {
			return nil, ErrShadowTooLarge
		}
		files = append(files, file)
	}
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	used := map[string]int{}
	for _, file := range files {
		name := filepath.Base(file)
		used[name]++
		if used[name] > 1 {
			ext := filepath.Ext(name)
			name = strings.TrimSuffix(name, ext) + fmt.Sprintf("-%d", used[name]) + ext
		}
		entry, err := writer.Create(name)
		if err != nil {
			return nil, err
		}
		source, err := os.Open(file)
		if err != nil {
			return nil, err
		}
		_, copyErr := io.Copy(entry, source)
		closeErr := source.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func mimeForSubAgentFile(name, contentType string) string {
	if contentType == "image" {
		switch strings.ToLower(filepath.Ext(name)) {
		case ".png":
			return "image/png"
		case ".jpg", ".jpeg":
			return "image/jpeg"
		case ".gif":
			return "image/gif"
		case ".webp":
			return "image/webp"
		}
	}
	return "application/octet-stream"
}
