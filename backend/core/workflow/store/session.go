package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"lazymind/core/common/orm"
)

var ErrInvalidSessionQuery = repositoryError("INVALID_SESSION_QUERY")

type conversationScopeKey struct{}

func WithConversationScope(ctx context.Context, conversationID string) context.Context {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return ctx
	}
	return context.WithValue(ctx, conversationScopeKey{}, conversationID)
}

func ConversationScope(ctx context.Context) string {
	conversationID, _ := ctx.Value(conversationScopeKey{}).(string)
	return strings.TrimSpace(conversationID)
}

func (r *Repository) AuthorizeConversation(ctx context.Context, conversationID, owner string) error {
	if r == nil || r.db == nil || strings.TrimSpace(conversationID) == "" || strings.TrimSpace(owner) == "" {
		return ErrPermissionDenied
	}
	var count int64
	if err := r.db.WithContext(ctx).Model(&orm.Conversation{}).
		Where("id = ? AND create_user_id = ?", strings.TrimSpace(conversationID), strings.TrimSpace(owner)).
		Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return ErrPermissionDenied
	}
	return nil
}

type SessionListQuery struct {
	Status    string
	PageSize  int
	PageToken string
}

type SessionSummary struct {
	SessionID          string    `json:"session_id"`
	ConversationID     string    `json:"conversation_id,omitempty"`
	WorkflowID         string    `json:"workflow_id"`
	WorkflowRef        string    `json:"workflow_ref,omitempty"`
	WorkflowRevisionID string    `json:"workflow_revision_id,omitempty"`
	WorkflowRevisionNo int64     `json:"workflow_revision_no,omitempty"`
	Status             string    `json:"status"`
	CurrentStepID      string    `json:"current_step_id,omitempty"`
	StateVersion       int64     `json:"state_version"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

type SessionPage struct {
	Sessions      []SessionSummary `json:"sessions"`
	NextPageToken string           `json:"next_page_token,omitempty"`
}

type sessionCursor struct {
	UpdatedAt      time.Time `json:"updated_at"`
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id,omitempty"`
	Status         string    `json:"status,omitempty"`
}

func (r *Repository) ListExternalSessions(ctx context.Context, owner string, query SessionListQuery) (SessionPage, error) {
	owner = strings.TrimSpace(owner)
	conversationID := ConversationScope(ctx)
	query.Status = strings.ToLower(strings.TrimSpace(query.Status))
	if r == nil || r.db == nil || owner == "" || !validSessionStatus(query.Status) {
		return SessionPage{}, ErrInvalidSessionQuery
	}
	limit := query.PageSize
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return SessionPage{}, ErrInvalidSessionQuery
	}
	db := r.db.WithContext(ctx).Model(&orm.WorkflowSession{}).
		Where("create_user_id = ? AND origin_host = ? AND controller_host = ? AND dismissed = ?", owner, "external-agent", "external-agent", false)
	if conversationID != "" {
		db = db.Where("conversation_id = ?", conversationID)
	}
	if query.Status != "" {
		db = db.Where("status = ?", query.Status)
	}
	if strings.TrimSpace(query.PageToken) != "" {
		cursor, err := decodeSessionCursor(query.PageToken)
		if err != nil || cursor.Status != query.Status || cursor.ConversationID != conversationID {
			return SessionPage{}, ErrInvalidSessionQuery
		}
		db = db.Where("updated_at < ? OR (updated_at = ? AND id < ?)", cursor.UpdatedAt, cursor.UpdatedAt, cursor.ID)
	}
	var sessions []orm.WorkflowSession
	if err := db.Order("updated_at DESC, id DESC").Limit(limit + 1).Find(&sessions).Error; err != nil {
		return SessionPage{}, err
	}
	page := SessionPage{Sessions: make([]SessionSummary, 0, min(len(sessions), limit))}
	for _, session := range sessions[:min(len(sessions), limit)] {
		page.Sessions = append(page.Sessions, SessionSummary{
			SessionID: session.ID, ConversationID: session.ConversationID,
			WorkflowID: session.WorkflowID, WorkflowRef: session.WorkflowRef,
			WorkflowRevisionID: session.WorkflowRevisionID, WorkflowRevisionNo: session.WorkflowRevisionNo,
			Status: session.Status, CurrentStepID: session.CurrentStepID, StateVersion: session.StateVersion,
			CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
		})
	}
	if len(sessions) > limit {
		last := sessions[limit-1]
		page.NextPageToken = encodeSessionCursor(sessionCursor{UpdatedAt: last.UpdatedAt, ID: last.ID,
			ConversationID: conversationID, Status: query.Status})
	}
	return page, nil
}

func validSessionStatus(status string) bool {
	switch status {
	case "", "active", "waiting", "stopped", "failed", "completed":
		return true
	default:
		return false
	}
}

func encodeSessionCursor(cursor sessionCursor) string {
	value, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(value)
}

func decodeSessionCursor(value string) (sessionCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return sessionCursor{}, ErrInvalidSessionQuery
	}
	var cursor sessionCursor
	if json.Unmarshal(decoded, &cursor) != nil || cursor.UpdatedAt.IsZero() || strings.TrimSpace(cursor.ID) == "" || !validSessionStatus(cursor.Status) {
		return sessionCursor{}, ErrInvalidSessionQuery
	}
	return cursor, nil
}
