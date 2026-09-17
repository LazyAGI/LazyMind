package chat

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/artifact"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/store"
	"lazymind/core/subagent"
)

const maxConversationArtifactBytes = 2 * 1024 * 1024
const conversationArtifactFileDirectory = "chat-artifacts"

const (
	artifactPublicationPublished = "published"
	artifactPublicationInput     = "input"
)

// ConversationArtifactDTO is the common download-card shape for both main-Agent
// and SubAgent artifacts.
type ConversationArtifactDTO struct {
	ArtifactID        string          `json:"artifact_id"`
	RevisionID        string          `json:"revision_id"`
	Revision          int             `json:"revision"`
	ConversationID    string          `json:"conversation_id"`
	HistoryID         string          `json:"history_id"`
	Name              string          `json:"name"`
	SourceType        string          `json:"source_type"`
	ProducerType      string          `json:"producer_type"`
	ProducerID        string          `json:"producer_id,omitempty"`
	Filename          string          `json:"filename,omitempty"`
	Slot              string          `json:"slot"`
	ContentType       string          `json:"content_type"`
	Seq               int             `json:"seq"`
	Value             json.RawMessage `json:"value"`
	Caption           *string         `json:"caption,omitempty"`
	PublicationStatus string          `json:"publication_status"`
	CreatedAt         time.Time       `json:"created_at"`
	V2ArtifactID      string          `json:"v2_artifact_id,omitempty"`
	LogicalKey        string          `json:"logical_key,omitempty"`
	ChangeSummary     string          `json:"change_summary,omitempty"`
	RevisionCount     int             `json:"revision_count,omitempty"`
	HeadVersion       int64           `json:"head_version,omitempty"`
}

func validArtifactFilename(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || utf8.RuneCountInString(name) > 255 ||
		strings.ContainsAny(name, "/\\") {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func artifactScopeHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	// Keep this in lockstep with algorithm/lazymind/chat/engine/tools/chat_artifact.py.
	// The algorithm publishes files below the first 128 bits of the SHA-256 digest
	// to keep packaged Windows paths comfortably below MAX_PATH.
	return fmt.Sprintf("%x", sum[:16])
}

func legacyArtifactScopeHash(value string) string {
	// Read-only compatibility for artifacts created before workspace hashes were
	// shortened for Windows. New paths must continue to use artifactScopeHash.
	return fmt.Sprintf("%x", sha256.Sum256([]byte(value)))
}

func conversationArtifactConversationRootWithHash(
	userID, conversationID string, scopeHash func(string) string,
) string {
	return filepath.Join(
		subagent.WorkspaceRoot(),
		conversationArtifactFileDirectory,
		scopeHash(userID),
		scopeHash(conversationID),
	)
}

func conversationArtifactFileRoot(userID, conversationID, artifactID string) string {
	return filepath.Join(
		conversationArtifactConversationRoot(userID, conversationID), artifactID,
	)
}

func legacyConversationArtifactFileRoot(userID, conversationID, artifactID string) string {
	return filepath.Join(
		legacyConversationArtifactConversationRoot(userID, conversationID), artifactID,
	)
}

func conversationArtifactFileRoots(userID, conversationID, artifactID string) []string {
	return []string{
		conversationArtifactFileRoot(userID, conversationID, artifactID),
		legacyConversationArtifactFileRoot(userID, conversationID, artifactID),
	}
}

func matchingConversationArtifactFileRoot(
	userID, conversationID, artifactID, filename, actualAbs string,
) (string, string) {
	for _, root := range conversationArtifactFileRoots(userID, conversationID, artifactID) {
		candidate, err := filepath.Abs(filepath.Join(root, filename))
		if err == nil && filepath.Clean(actualAbs) == filepath.Clean(candidate) {
			return root, candidate
		}
	}
	return "", ""
}

func conversationArtifactConversationRoot(userID, conversationID string) string {
	return conversationArtifactConversationRootWithHash(
		userID, conversationID, artifactScopeHash,
	)
}

func legacyConversationArtifactConversationRoot(userID, conversationID string) string {
	return conversationArtifactConversationRootWithHash(
		userID, conversationID, legacyArtifactScopeHash,
	)
}

func conversationAgentWorkspaceRoots(userID, conversationID string) []string {
	root := strings.TrimSpace(os.Getenv("LAZYMIND_AGENTIC_WORKSPACE"))
	if root == "" {
		return nil
	}
	return []string{
		filepath.Join(
			root, conversationArtifactFileDirectory,
			artifactScopeHash(userID), artifactScopeHash(conversationID),
		),
		filepath.Join(
			root, conversationArtifactFileDirectory,
			legacyArtifactScopeHash(userID), legacyArtifactScopeHash(conversationID),
		),
	}
}

func conversationArtifactRoots(userID, conversationID string) []string {
	roots := []string{
		conversationArtifactConversationRoot(userID, conversationID),
		legacyConversationArtifactConversationRoot(userID, conversationID),
	}
	roots = append(roots, conversationAgentWorkspaceRoots(userID, conversationID)...)
	seen := make(map[string]struct{}, len(roots))
	unique := make([]string, 0, len(roots))
	for _, root := range roots {
		root = filepath.Clean(root)
		if _, ok := seen[root]; ok {
			continue
		}
		seen[root] = struct{}{}
		unique = append(unique, root)
	}
	return unique
}

func removeConversationArtifactFiles(userID, conversationID string) error {
	for _, root := range conversationArtifactRoots(userID, conversationID) {
		if err := os.RemoveAll(root); err != nil {
			return err
		}
	}
	return nil
}

func canonicalConversationFileValue(
	userID, conversationID, artifactID, filename string, raw json.RawMessage,
) (json.RawMessage, error) {
	var value map[string]any
	if json.Unmarshal(raw, &value) != nil {
		return nil, errors.New("file artifact value must be an object")
	}
	valueFilename, ok := value["filename"].(string)
	if !ok || strings.TrimSpace(valueFilename) != filename {
		return nil, errors.New("file artifact filename does not match metadata")
	}
	storedPath, ok := value["path"].(string)
	if !ok || strings.TrimSpace(storedPath) == "" {
		return nil, errors.New("file artifact value must contain path")
	}
	actualAbs, err := filepath.Abs(strings.TrimSpace(storedPath))
	if err != nil {
		return nil, errors.New("file artifact path is invalid")
	}
	_, expectedAbs := matchingConversationArtifactFileRoot(
		userID, conversationID, artifactID, filename, actualAbs,
	)
	if expectedAbs == "" {
		return nil, errors.New("file artifact path is outside its conversation workspace")
	}
	resolvedPath, err := filepath.EvalSymlinks(actualAbs)
	if err != nil {
		return nil, errors.New("file artifact does not exist")
	}
	resolvedRoot, err := filepath.EvalSymlinks(filepath.Dir(expectedAbs))
	if err != nil || filepath.Dir(resolvedPath) != resolvedRoot {
		return nil, errors.New("file artifact path escapes its conversation workspace")
	}
	info, err := os.Stat(resolvedPath)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("file artifact must be a regular file")
	}
	canonical, err := json.Marshal(map[string]any{
		"filename": filename,
		"path":     resolvedPath,
		"size":     info.Size(),
	})
	if err != nil {
		return nil, errors.New("file artifact value is invalid")
	}
	return canonical, nil
}

func conversationArtifactResponseValue(
	userID, conversationID string, artifact orm.ConversationArtifact,
) json.RawMessage {
	if artifact.ContentType != "file" {
		return artifact.Value
	}
	workspaceRoot := conversationArtifactFileRoot(userID, conversationID, artifact.ID)
	var value map[string]any
	if json.Unmarshal(artifact.Value, &value) == nil {
		if storedPath, ok := value["path"].(string); ok {
			if actualAbs, err := filepath.Abs(strings.TrimSpace(storedPath)); err == nil {
				if matchedRoot, _ := matchingConversationArtifactFileRoot(
					userID, conversationID, artifact.ID, artifact.Filename, actualAbs,
				); matchedRoot != "" {
					workspaceRoot = matchedRoot
				}
			}
		}
	}
	return subagent.SignArtifactValue(
		artifact.ContentType,
		artifact.Value,
		workspaceRoot,
	)
}

func persistConversationArtifact(
	ctx context.Context, db *gorm.DB, conversationID, historyID, userID string,
	event *ArtifactCreatedEvent,
) (*ConversationArtifactDTO, error) {
	if event == nil {
		return nil, errors.New("artifact event is required")
	}
	artifactID := strings.TrimSpace(event.ArtifactID)
	if _, err := uuid.Parse(artifactID); err != nil {
		return nil, errors.New("invalid artifact id")
	}
	if len(artifactID) > 36 ||
		conversationID == "" || historyID == "" || userID == "" ||
		!validArtifactFilename(event.Filename) {
		return nil, errors.New("invalid artifact metadata")
	}
	contentType := strings.ToLower(strings.TrimSpace(event.ContentType))
	if contentType != "text" && contentType != "json" && contentType != "file" {
		return nil, errors.New("unsupported artifact content type")
	}
	if len(event.Value) == 0 || len(event.Value) > maxConversationArtifactBytes || !json.Valid(event.Value) {
		return nil, errors.New("invalid artifact value")
	}
	if contentType == "file" {
		canonical, err := canonicalConversationFileValue(
			userID, conversationID, artifactID, strings.TrimSpace(event.Filename), event.Value,
		)
		if err != nil {
			return nil, err
		}
		event.Value = canonical
	} else {
		var value map[string]any
		if json.Unmarshal(event.Value, &value) != nil {
			return nil, errors.New("artifact value must be an object")
		}
		if contentType == "text" {
			if _, ok := value["text"].(string); !ok {
				return nil, errors.New("text artifact value must contain text")
			}
		} else if _, ok := value["data"]; !ok {
			return nil, errors.New("json artifact value must contain data")
		}
	}
	if event.Caption != nil && utf8.RuneCountInString(*event.Caption) > 2000 {
		return nil, errors.New("artifact caption is too long")
	}
	now := time.Now().UTC()
	row := orm.ConversationArtifact{
		ID:             artifactID,
		ConversationID: conversationID,
		HistoryID:      historyID,
		Filename:       strings.TrimSpace(event.Filename),
		Slot:           strings.TrimSpace(event.Filename),
		ContentType:    contentType,
		Value:          event.Value,
		Caption:        event.Caption,
		CreateUserID:   userID,
		CreatedAt:      now,
	}
	if event.ReplaceExisting {
		var existing orm.ConversationArtifact
		err := db.WithContext(ctx).First(&existing, "id = ?", artifactID).Error
		if err == nil {
			if existing.ConversationID != conversationID || existing.CreateUserID != userID {
				return nil, errors.New("artifact replacement scope mismatch")
			}
			row.HistoryID = existing.HistoryID
			row.CreatedAt = existing.CreatedAt
			result := db.WithContext(ctx).Model(&orm.ConversationArtifact{}).
				Where("id = ? AND conversation_id = ? AND create_user_id = ?",
					artifactID, conversationID, userID).
				Updates(map[string]any{
					"filename":     row.Filename,
					"slot":         row.Slot,
					"content_type": row.ContentType,
					"value":        row.Value,
					"caption":      row.Caption,
				})
			if result.Error != nil {
				return nil, result.Error
			}
			return conversationArtifactDTO(ctx, db, userID, conversationID, row, event), nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	result := db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected != 1 {
		return nil, errors.New("artifact id already exists")
	}
	return conversationArtifactDTO(ctx, db, userID, conversationID, row, event), nil
}

func conversationArtifactDTO(
	ctx context.Context, db *gorm.DB, userID, conversationID string, row orm.ConversationArtifact, event *ArtifactCreatedEvent,
) *ConversationArtifactDTO {
	dto := &ConversationArtifactDTO{
		ArtifactID: row.ID, RevisionID: row.ID, Revision: 1,
		ConversationID: row.ConversationID, HistoryID: row.HistoryID,
		Name: row.Filename, SourceType: "main_chat",
		ProducerType: "main_agent", Filename: row.Filename, Slot: row.Slot,
		ContentType: row.ContentType, Seq: 1,
		Value:             conversationArtifactResponseValue(userID, conversationID, row),
		Caption:           row.Caption,
		PublicationStatus: artifactPublicationPublished,
		CreatedAt:         row.CreatedAt,
	}
	if event != nil {
		dto.LogicalKey = strings.TrimSpace(event.LogicalKey)
		dto.ChangeSummary = strings.TrimSpace(event.ChangeSummary)
	}
	if maybeDualWriteConversationArtifact(ctx, db, conversationID, row.HistoryID, userID, event, row) == nil {
		enrichConversationArtifactDTO(ctx, db, userID, dto)
	}
	return dto
}

func maybeDualWriteConversationArtifact(
	ctx context.Context, db *gorm.DB, conversationID, historyID, userID string,
	event *ArtifactCreatedEvent, row orm.ConversationArtifact,
) error {
	if db == nil || !artifact.Enabled() {
		return nil
	}
	meta := artifact.MainChatWrite{}
	if event != nil {
		meta.LogicalKey = event.LogicalKey
		meta.IdempotencyKey = event.IdempotencyKey
		meta.ChangeSummary = event.ChangeSummary
	}
	return artifact.DualWriteMainChat(ctx, artifact.New(db), conversationID, historyID, userID, meta, row)
}

func enrichConversationArtifactDTO(ctx context.Context, db *gorm.DB, userID string, dto *ConversationArtifactDTO) {
	if dto == nil || dto.SourceType == "user_upload" {
		return
	}
	if !artifact.Enabled() {
		return
	}
	if db == nil {
		return
	}
	proj := artifact.EnrichLegacyDTO(ctx, artifact.New(db), userID, dto.ArtifactID)
	if proj.V2ArtifactID == "" {
		dto.Revision = int(proj.RevisionNo)
		dto.RevisionCount = 1
		return
	}
	dto.V2ArtifactID = proj.V2ArtifactID
	dto.RevisionID = proj.RevisionID
	dto.Revision = int(proj.RevisionNo)
	dto.RevisionCount = proj.Count
	dto.LogicalKey = firstNonEmptyArtifact(dto.LogicalKey, proj.LogicalKey)
	dto.ChangeSummary = firstNonEmptyArtifact(dto.ChangeSummary, proj.ChangeSummary)
	dto.HeadVersion = proj.HeadVersion
	if len(proj.InlineJSON) > 0 {
		dto.Value = proj.InlineJSON
	} else if len(proj.OverlayValue) > 0 {
		dto.Value = proj.OverlayValue
	}
}

func firstNonEmptyArtifact(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

type workflowArtifactProjectionRef struct {
	RevisionID string `gorm:"column:revision_id"`
	Revision   int    `gorm:"column:revision"`
	TaskID     string `gorm:"column:task_id"`
	Slot       string `gorm:"column:slot"`
	Seq        int    `gorm:"column:seq"`
}

func workflowArtifactProjectionRefs(
	ctx context.Context, db *gorm.DB, conversationID, userID string,
) (map[string]workflowArtifactProjectionRef, error) {
	var rows []workflowArtifactProjectionRef
	err := db.WithContext(ctx).Table("plugin_slot_revisions AS revision").
		Select(`revision.id AS revision_id, revision.revision, step.task_id,
			revision.slot, revision.artifact_seq AS seq`).
		Joins("JOIN plugin_sessions AS session ON session.id = revision.session_id").
		Joins(`JOIN plugin_session_steps AS step ON step.session_id = revision.session_id
			AND step.step_id = revision.step_id AND step.attempt = revision.attempt`).
		Where(`session.conversation_id = ? AND session.create_user_id = ?
			AND revision.selected = ? AND revision.validity = ? AND revision.artifact_seq IS NOT NULL`,
			conversationID, userID, true, "effective").
		Order("revision.revision DESC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	refs := make(map[string]workflowArtifactProjectionRef, len(rows))
	for _, row := range rows {
		key := fmt.Sprintf("%s\x00%s\x00%d", row.TaskID, row.Slot, row.Seq)
		if _, exists := refs[key]; !exists {
			refs[key] = row
		}
	}
	return refs, nil
}

type conversationArtifactInput struct {
	InputType string `json:"input_type"`
	URI       string `json:"uri"`
	Filename  string `json:"filename"`
	Name      string `json:"name"`
}

type conversationArtifactHistoryExt struct {
	Input []conversationArtifactInput `json:"input"`
}

// conversationUserUploadArtifacts projects only files that a user supplied to a
// main-chat turn. It deliberately omits text, data URLs, and remote URLs because
// the conversation files drawer may only expose Core-owned, ACL-checked bytes.
func conversationUserUploadArtifacts(
	conversationID, userID string, histories []orm.ChatHistory,
) []ConversationArtifactDTO {
	out := make([]ConversationArtifactDTO, 0)
	seenURIs := make(map[string]struct{})
	for _, history := range histories {
		var ext conversationArtifactHistoryExt
		if json.Unmarshal(history.Ext, &ext) != nil {
			continue
		}
		for index, input := range ext.Input {
			kind := strings.ToLower(strings.TrimSpace(input.InputType))
			if kind != "file" && kind != "image" {
				continue
			}
			uri := strings.TrimSpace(input.URI)
			if uri == "" || strings.HasPrefix(uri, "data:") {
				continue
			}
			if _, alreadyProjected := seenURIs[uri]; alreadyProjected {
				continue
			}
			url := doc.StaticFileURLForUploadOwner(uri, userID)
			if url == "" {
				continue
			}
			seenURIs[uri] = struct{}{}

			filename := strings.TrimSpace(input.Filename)
			if filename == "" {
				filename = strings.TrimSpace(input.Name)
			}
			if filename == "" {
				filename = filepath.Base(strings.SplitN(uri, "?", 2)[0])
			}
			if !validArtifactFilename(filename) {
				continue
			}
			value, err := json.Marshal(map[string]any{
				"url":      url,
				"filename": filename,
			})
			if err != nil {
				continue
			}
			contentType := "file"
			if kind == "image" {
				contentType = "image"
			}
			out = append(out, ConversationArtifactDTO{
				ArtifactID:        fmt.Sprintf("upload:%s:%d", history.ID, index),
				RevisionID:        fmt.Sprintf("upload:%s:%d", history.ID, index),
				Revision:          1,
				ConversationID:    conversationID,
				HistoryID:         history.ID,
				Name:              filename,
				SourceType:        "user_upload",
				ProducerType:      "user",
				Filename:          filename,
				Slot:              filename,
				ContentType:       contentType,
				Seq:               1,
				Value:             value,
				PublicationStatus: artifactPublicationInput,
				CreatedAt:         history.CreateTime,
			})
		}
	}
	return out
}

func collapseVersionedArtifacts(items []ConversationArtifactDTO) []ConversationArtifactDTO {
	latest := make(map[string]int)
	for i, item := range items {
		if item.V2ArtifactID == "" {
			continue
		}
		prev, ok := latest[item.V2ArtifactID]
		if !ok || !items[prev].CreatedAt.After(item.CreatedAt) {
			latest[item.V2ArtifactID] = i
		}
	}
	if len(latest) == 0 {
		return items
	}
	out := make([]ConversationArtifactDTO, 0, len(items))
	seen := make(map[string]struct{}, len(latest))
	for i, item := range items {
		if item.V2ArtifactID == "" {
			out = append(out, item)
			continue
		}
		if latest[item.V2ArtifactID] != i {
			continue
		}
		if _, dup := seen[item.V2ArtifactID]; dup {
			continue
		}
		seen[item.V2ArtifactID] = struct{}{}
		out = append(out, item)
	}
	return out
}

// conversationSubAgentArtifacts exposes completed ordinary SubAgent outputs.
// With V2 off this matches the pre-V2 conversation artifacts API. With V2 on,
// workflow rows stay out of the panel, but a failed shadow write still leaves
// the authoritative legacy row visible.
func conversationSubAgentArtifacts(
	ctx context.Context, db *gorm.DB, conversationID, userID string,
) []ConversationArtifactDTO {
	if db == nil {
		return nil
	}
	if !artifact.Enabled() {
		return legacyConversationSubAgentArtifacts(ctx, db, conversationID, userID)
	}
	var tasks []orm.SubAgentTask
	if err := db.WithContext(ctx).Where(
		"conversation_id = ? AND create_user_id = ? AND agent_type <> ? AND status = ?",
		conversationID, userID, "workflow_step", subagent.StatusSucceeded,
	).Find(&tasks).Error; err != nil || len(tasks) == 0 {
		return nil
	}
	taskByID := make(map[string]orm.SubAgentTask, len(tasks))
	taskIDs := make([]string, 0, len(tasks))
	for _, task := range tasks {
		taskByID[task.ID] = task
		taskIDs = append(taskIDs, task.ID)
	}
	var rows []orm.SubAgentArtifact
	if err := db.WithContext(ctx).Where("task_id IN ? AND hidden = ?", taskIDs, false).
		Order("created_at ASC, id ASC").Find(&rows).Error; err != nil {
		return nil
	}
	svc := artifact.New(db)
	out := make([]ConversationArtifactDTO, 0, len(rows))
	for _, row := range rows {
		task := taskByID[row.TaskID]
		proj := artifact.EnrichLegacyDTOByBinding(
			ctx, svc, userID, artifact.ScopeSubAgentLegacyRow, row.ID,
		)
		filename := subAgentArtifactFilename(row)
		dto := ConversationArtifactDTO{
			ArtifactID: row.ID, RevisionID: row.ID, Revision: 1,
			ConversationID: conversationID, HistoryID: task.TriggerHistoryID,
			Name: filename, SourceType: "subagent", ProducerType: "subagent", ProducerID: task.ID,
			Filename: filename, Slot: row.Slot, ContentType: row.ContentType, Seq: row.Seq,
			Value:   subagent.SignArtifactValue(row.ContentType, row.Value, task.WorkspacePath),
			Caption: row.Caption, PublicationStatus: artifactPublicationPublished, CreatedAt: row.CreatedAt,
		}
		if proj.V2ArtifactID != "" {
			dto.RevisionID = proj.RevisionID
			dto.Revision = int(proj.RevisionNo)
			dto.V2ArtifactID = proj.V2ArtifactID
			dto.LogicalKey = proj.LogicalKey
			dto.ChangeSummary = proj.ChangeSummary
			dto.RevisionCount = proj.Count
			dto.HeadVersion = proj.HeadVersion
			// file_list stays on signed legacy paths so the panel can expand
			// individual files; V2 stores a zip snapshot for history/download.
			if row.ContentType != "file_list" {
				if len(proj.InlineJSON) > 0 {
					dto.Value = proj.InlineJSON
				} else if len(proj.OverlayValue) > 0 {
					dto.Value = proj.OverlayValue
				}
			}
		}
		out = append(out, dto)
	}
	return out
}

func legacyConversationSubAgentArtifacts(
	ctx context.Context, db *gorm.DB, conversationID, userID string,
) []ConversationArtifactDTO {
	records, err := subagent.ListArtifactsByConversationForUser(ctx, db, conversationID, userID)
	if err != nil {
		return nil
	}
	out := make([]ConversationArtifactDTO, 0, len(records))
	for _, row := range records {
		filename := row.Slot
		if validArtifactFilename(filepath.Base(row.Slot)) {
			filename = filepath.Base(row.Slot)
		}
		out = append(out, ConversationArtifactDTO{
			ArtifactID: row.ArtifactID, RevisionID: row.ArtifactID, Revision: 1,
			ConversationID: conversationID, HistoryID: row.TriggerHistoryID,
			Name: filename, SourceType: "subagent", ProducerType: "subagent", ProducerID: row.TaskID,
			Filename: filename, Slot: row.Slot, ContentType: row.ContentType, Seq: row.Seq,
			Value:   subagent.SignArtifactValue(row.ContentType, row.Value, row.WorkspacePath),
			Caption: row.Caption, PublicationStatus: artifactPublicationPublished, CreatedAt: row.CreatedAt,
		})
	}
	return out
}

func subAgentArtifactFilename(row orm.SubAgentArtifact) string {
	var value map[string]any
	if json.Unmarshal(row.Value, &value) == nil {
		if filename, _ := value["filename"].(string); validArtifactFilename(filename) {
			return filename
		}
		if path, _ := value["path"].(string); validArtifactFilename(filepath.Base(path)) {
			return filepath.Base(path)
		}
	}
	return row.Slot
}

// ListConversationArtifacts returns the conversation-facing projection: user
// inputs and main-chat artifacts that were delivered to the user. Task and
// workflow working artifacts deliberately remain in their own workspaces.
func ListConversationArtifacts(w http.ResponseWriter, r *http.Request) {
	listConversationArtifacts(w, r)
}

func ListConversationArtifactProjection(w http.ResponseWriter, r *http.Request) {
	listConversationArtifacts(w, r)
}

func listConversationArtifacts(w http.ResponseWriter, r *http.Request) {
	conversationID := common.PathVar(r, "conversation_id")
	if conversationID == "" {
		common.ReplyErr(w, "conversation_id required", http.StatusBadRequest)
		return
	}
	db := store.DB()
	if db == nil {
		common.ReplyErr(w, "store not initialized", http.StatusInternalServerError)
		return
	}
	userID := store.UserID(r)
	if userID == "" {
		userID = "0"
	}
	var conversation orm.Conversation
	if err := db.WithContext(r.Context()).Where(
		"id = ? AND create_user_id = ?", conversationID, userID,
	).First(&conversation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ReplyErr(w, "conversation not found", http.StatusNotFound)
		} else {
			common.ReplyErr(w, "query conversation failed", http.StatusInternalServerError)
		}
		return
	}

	out := make([]ConversationArtifactDTO, 0)
	var direct []orm.ConversationArtifact
	if err := db.WithContext(r.Context()).Where(
		"conversation_id = ? AND create_user_id = ?", conversationID, userID,
	).Find(&direct).Error; err != nil {
		common.ReplyErr(w, "query conversation artifacts failed", http.StatusInternalServerError)
		return
	}
	for _, artifactRow := range direct {
		dto := ConversationArtifactDTO{
			ArtifactID: artifactRow.ID, RevisionID: artifactRow.ID, Revision: 1,
			ConversationID: conversationID, HistoryID: artifactRow.HistoryID,
			Name: artifactRow.Filename, SourceType: "main_chat",
			ProducerType: "main_agent", Filename: artifactRow.Filename, Slot: artifactRow.Slot,
			ContentType: artifactRow.ContentType, Seq: 1,
			Value:             conversationArtifactResponseValue(userID, conversationID, artifactRow),
			Caption:           artifactRow.Caption,
			PublicationStatus: artifactPublicationPublished,
			CreatedAt:         artifactRow.CreatedAt,
		}
		enrichConversationArtifactDTO(r.Context(), db, userID, &dto)
		out = append(out, dto)
	}

	var histories []orm.ChatHistory
	if err := db.WithContext(r.Context()).Select("id, conversation_id, ext, create_time").Where(
		"conversation_id = ?", conversationID,
	).Order("seq ASC, create_time ASC, id ASC").Find(&histories).Error; err != nil {
		common.ReplyErr(w, "query conversation uploads failed", http.StatusInternalServerError)
		return
	}
	out = append(out, conversationUserUploadArtifacts(conversationID, userID, histories)...)
	out = append(out, conversationSubAgentArtifacts(r.Context(), db, conversationID, userID)...)
	out = collapseVersionedArtifacts(out)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ArtifactID < out[j].ArtifactID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	common.ReplyOK(w, map[string]any{"artifacts": out})
}
