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

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/subagent"
)

const maxConversationArtifactBytes = 2 * 1024 * 1024
const conversationArtifactFileDirectory = "chat-artifacts"

// ConversationArtifactDTO is the common download-card shape for both main-Agent
// and SubAgent artifacts.
type ConversationArtifactDTO struct {
	ArtifactID     string          `json:"artifact_id"`
	ConversationID string          `json:"conversation_id"`
	HistoryID      string          `json:"history_id"`
	ProducerType   string          `json:"producer_type"`
	ProducerID     string          `json:"producer_id,omitempty"`
	Filename       string          `json:"filename,omitempty"`
	Slot           string          `json:"slot"`
	ContentType    string          `json:"content_type"`
	Seq            int             `json:"seq"`
	Value          json.RawMessage `json:"value"`
	Caption        *string         `json:"caption,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
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
			return &ConversationArtifactDTO{
				ArtifactID: row.ID, ConversationID: row.ConversationID, HistoryID: row.HistoryID,
				ProducerType: "main_agent", Filename: row.Filename, Slot: row.Slot,
				ContentType: row.ContentType, Seq: 1,
				Value:     conversationArtifactResponseValue(userID, conversationID, row),
				Caption:   row.Caption,
				CreatedAt: row.CreatedAt,
			}, nil
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
	return &ConversationArtifactDTO{
		ArtifactID: row.ID, ConversationID: row.ConversationID, HistoryID: row.HistoryID,
		ProducerType: "main_agent", Filename: row.Filename, Slot: row.Slot,
		ContentType: row.ContentType, Seq: 1,
		Value:     conversationArtifactResponseValue(userID, conversationID, row),
		Caption:   row.Caption,
		CreatedAt: row.CreatedAt,
	}, nil
}

// ListConversationArtifacts handles GET /conversations/{conversation_id}/artifacts.
func ListConversationArtifacts(w http.ResponseWriter, r *http.Request) {
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
	for _, artifact := range direct {
		out = append(out, ConversationArtifactDTO{
			ArtifactID: artifact.ID, ConversationID: conversationID, HistoryID: artifact.HistoryID,
			ProducerType: "main_agent", Filename: artifact.Filename, Slot: artifact.Slot,
			ContentType: artifact.ContentType, Seq: 1,
			Value:   conversationArtifactResponseValue(userID, conversationID, artifact),
			Caption: artifact.Caption, CreatedAt: artifact.CreatedAt,
		})
	}

	subagentArtifacts, err := subagent.ListArtifactsByConversationForUser(
		r.Context(), db, conversationID, userID,
	)
	if err != nil {
		common.ReplyErr(w, "query subagent artifacts failed", http.StatusInternalServerError)
		return
	}
	for _, artifact := range subagentArtifacts {
		out = append(out, ConversationArtifactDTO{
			ArtifactID: artifact.ArtifactID, ConversationID: conversationID,
			HistoryID: artifact.TriggerHistoryID, ProducerType: "subagent", ProducerID: artifact.TaskID,
			Slot: artifact.Slot, ContentType: artifact.ContentType, Seq: artifact.Seq,
			Value: subagent.SignArtifactValue(
				artifact.ContentType, artifact.Value, artifact.WorkspacePath,
			),
			Caption: artifact.Caption, CreatedAt: artifact.CreatedAt,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ArtifactID < out[j].ArtifactID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	common.ReplyOK(w, map[string]any{"artifacts": out})
}
