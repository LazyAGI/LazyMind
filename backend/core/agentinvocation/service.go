package agentinvocation

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
	"lazymind/core/externalcontext"
)

const (
	StatusRunning     = "running"
	StatusSucceeded   = "succeeded"
	StatusFailed      = "failed"
	StatusInterrupted = "interrupted"

	maxSummaryBytes = 16 << 10
	defaultPageSize = 50
	maxPageSize     = 100
)

var (
	ErrInvalidInput = errors.New("invalid agent invocation")
	ErrNotFound     = errors.New("agent invocation not found")
	ErrConflict     = errors.New("agent invocation conflicts with an existing record")
)

type Service struct{ db *gorm.DB }

type StartInput struct {
	ID                  string                  `json:"-"`
	ExternalRef         string                  `json:"-"`
	ConversationID      string                  `json:"-"`
	ClientName          string                  `json:"client_name"`
	ClientVersion       string                  `json:"client_version,omitempty"`
	ConnectorName       string                  `json:"connector_name"`
	ConnectorVersion    string                  `json:"connector_version,omitempty"`
	ConnectorInstanceID string                  `json:"connector_instance_id"`
	ProtocolVersion     string                  `json:"protocol_version,omitempty"`
	Transport           string                  `json:"transport"`
	ToolName            string                  `json:"tool_name"`
	ReadOnly            bool                    `json:"read_only"`
	RequestHash         string                  `json:"request_hash"`
	RequestSummary      json.RawMessage         `json:"request_summary,omitempty"`
	Source              *externalcontext.Source `json:"source,omitempty"`
}

type StartResult struct {
	Invocation orm.AgentInvocation   `json:"invocation"`
	Created    bool                  `json:"created"`
	Source     *externalcontext.Link `json:"source,omitempty"`
}

type TurnSyncInput struct {
	Source externalcontext.Source `json:"source"`
	Answer string                 `json:"answer"`
}

type FinishInput struct {
	Status        string          `json:"status"`
	ResultSummary json.RawMessage `json:"result_summary,omitempty"`
	ErrorCode     string          `json:"error_code,omitempty"`
	Retryable     bool            `json:"retryable,omitempty"`
	WorkflowID    string          `json:"workflow_id,omitempty"`
	SessionID     string          `json:"session_id,omitempty"`
	StepID        string          `json:"step_id,omitempty"`
	AttemptID     string          `json:"attempt_id,omitempty"`
	ResourceID    string          `json:"resource_id,omitempty"`
	ArtifactID    string          `json:"artifact_id,omitempty"`
	CommandID     string          `json:"command_id,omitempty"`
	ExternalRef   string          `json:"external_ref,omitempty"`
}

type ListQuery struct {
	ToolName    string
	Status      string
	ClientName  string
	WorkflowID  string
	SessionID   string
	ExternalRef string
	PageToken   string
	PageSize    int
}

type Page struct {
	Invocations   []orm.AgentInvocation `json:"invocations"`
	NextPageToken string                `json:"next_page_token,omitempty"`
}

type pageCursor struct {
	StartedAt time.Time `json:"started_at"`
	ID        string    `json:"id"`
}

func New(db *gorm.DB) *Service { return &Service{db: db} }

func (s *Service) SyncTurn(ctx context.Context, owner string, input TurnSyncInput) error {
	if s == nil || s.db == nil {
		return ErrInvalidInput
	}
	if err := externalcontext.New(s.db).SyncTurnAnswer(ctx, owner, input.Source, input.Answer); errors.Is(err, externalcontext.ErrInvalidSource) {
		return ErrInvalidInput
	} else {
		return err
	}
}

func (s *Service) Start(ctx context.Context, owner string, input StartInput) (orm.AgentInvocation, bool, error) {
	result, err := s.StartLinked(ctx, owner, input)
	return result.Invocation, result.Created, err
}

func (s *Service) StartLinked(ctx context.Context, owner string, input StartInput) (StartResult, error) {
	if s == nil || s.db == nil {
		return StartResult{}, ErrInvalidInput
	}
	var output StartResult
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if input.Source != nil && strings.TrimSpace(input.ExternalRef) == "" && strings.TrimSpace(input.ConversationID) == "" {
			link, err := externalcontext.New(tx).ResolveInvocation(ctx, owner, input.ID, input.ToolName, *input.Source)
			if errors.Is(err, externalcontext.ErrInvalidSource) {
				return ErrInvalidInput
			}
			if errors.Is(err, externalcontext.ErrThreadOwned) {
				return ErrConflict
			}
			if err != nil {
				return err
			}
			input.ExternalRef, input.ConversationID = link.ExternalRef, link.ConversationID
			output.Source = &link
		} else if strings.TrimSpace(input.ExternalRef) != "" || strings.TrimSpace(input.ConversationID) != "" {
			output.Source = &externalcontext.Link{
				ConversationID: strings.TrimSpace(input.ConversationID),
				ExternalRef:    strings.TrimSpace(input.ExternalRef),
			}
		}
		record, created, err := (&Service{db: tx}).startRecord(ctx, owner, input)
		if err != nil {
			return err
		}
		output.Invocation, output.Created = record, created
		return nil
	})
	return output, err
}

func (s *Service) startRecord(ctx context.Context, owner string, input StartInput) (orm.AgentInvocation, bool, error) {
	owner = strings.TrimSpace(owner)
	input.ID = strings.TrimSpace(input.ID)
	input.ClientName = strings.TrimSpace(input.ClientName)
	input.ClientVersion = strings.TrimSpace(input.ClientVersion)
	input.ConnectorName = strings.TrimSpace(input.ConnectorName)
	input.ConnectorVersion = strings.TrimSpace(input.ConnectorVersion)
	input.ToolName = strings.TrimSpace(input.ToolName)
	input.ConnectorInstanceID = strings.TrimSpace(input.ConnectorInstanceID)
	input.ProtocolVersion = strings.TrimSpace(input.ProtocolVersion)
	input.Transport = strings.TrimSpace(input.Transport)
	input.RequestHash = strings.ToLower(strings.TrimSpace(input.RequestHash))
	input.ExternalRef = strings.TrimSpace(input.ExternalRef)
	if s == nil || s.db == nil || !bounded(owner, 255) || !bounded(input.ID, 80) ||
		!bounded(input.ClientName, 128) || !bounded(input.ConnectorName, 128) ||
		!bounded(input.ToolName, 128) || !validSummaryIdentifier(input.ToolName) ||
		!bounded(input.ConnectorInstanceID, 80) || !validHash(input.RequestHash) {
		return orm.AgentInvocation{}, false, ErrInvalidInput
	}
	requestSummary, err := normalizedSummary(input.RequestSummary)
	if err != nil {
		return orm.AgentInvocation{}, false, err
	}
	now := time.Now().UTC()
	record := orm.AgentInvocation{
		ID: input.ID, OwnerUserID: owner,
		ClientName: boundedValue(input.ClientName, 128), ClientVersion: boundedValue(input.ClientVersion, 128),
		ConnectorName: boundedValue(input.ConnectorName, 128), ConnectorVersion: boundedValue(input.ConnectorVersion, 64),
		ConnectorInstanceID: input.ConnectorInstanceID, ProtocolVersion: boundedValue(input.ProtocolVersion, 64),
		Transport: boundedDefault(input.Transport, "stdio", 32), ToolName: input.ToolName, ReadOnly: input.ReadOnly,
		Status: StatusRunning, RequestHash: input.RequestHash, RequestSummary: requestSummary, ExternalRef: input.ExternalRef,
		ResultSummary: json.RawMessage(`{}`), StartedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	result := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&record)
	if result.Error != nil {
		return orm.AgentInvocation{}, false, result.Error
	}
	if result.RowsAffected == 1 {
		return record, true, nil
	}
	var existing orm.AgentInvocation
	if err := s.db.WithContext(ctx).Where("id = ?", input.ID).Take(&existing).Error; err != nil {
		return orm.AgentInvocation{}, false, err
	}
	if !sameStart(existing, record) {
		return orm.AgentInvocation{}, false, ErrConflict
	}
	return existing, false, nil
}

func (s *Service) Finish(ctx context.Context, owner, id string, input FinishInput) (orm.AgentInvocation, error) {
	if s == nil || s.db == nil {
		return orm.AgentInvocation{}, ErrInvalidInput
	}
	var output orm.AgentInvocation
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		record, err := (&Service{db: tx}).finishRecord(ctx, owner, id, input)
		if err != nil {
			return err
		}
		source := externalcontext.New(tx)
		if err := source.LinkWorkflowSession(ctx, owner, record.ExternalRef, record.SessionID); err != nil {
			return err
		}
		if err := source.CompleteObservedTurn(ctx, owner, record.ExternalRef); err != nil {
			return err
		}
		output = record
		return nil
	})
	return output, err
}

func (s *Service) finishRecord(ctx context.Context, owner, id string, input FinishInput) (orm.AgentInvocation, error) {
	owner, id = strings.TrimSpace(owner), strings.TrimSpace(id)
	input.Status = strings.ToLower(strings.TrimSpace(input.Status))
	if s == nil || s.db == nil || !bounded(owner, 255) || !bounded(id, 80) || !terminalStatus(input.Status) {
		return orm.AgentInvocation{}, ErrInvalidInput
	}
	for _, link := range []string{input.WorkflowID, input.SessionID, input.StepID, input.AttemptID,
		input.ResourceID, input.ArtifactID, input.CommandID} {
		if !validOptionalSummaryIdentifier(link) {
			return orm.AgentInvocation{}, ErrInvalidInput
		}
	}
	resultSummary, err := normalizedSummary(input.ResultSummary)
	if err != nil {
		return orm.AgentInvocation{}, err
	}
	var record orm.AgentInvocation
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", id, owner).Take(&record).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return orm.AgentInvocation{}, ErrNotFound
		}
		return orm.AgentInvocation{}, err
	}
	if strings.TrimSpace(input.ExternalRef) == "" {
		input.ExternalRef = record.ExternalRef
	}
	if terminalStatus(record.Status) {
		if !sameFinish(record, input, resultSummary) {
			return orm.AgentInvocation{}, ErrConflict
		}
		return record, nil
	}
	if record.Status != StatusRunning {
		return orm.AgentInvocation{}, ErrConflict
	}
	now := time.Now().UTC()
	updates := map[string]any{
		"status": input.Status, "result_summary": resultSummary,
		"error_code": boundedValue(input.ErrorCode, 128), "retryable": input.Retryable,
		"workflow_id": boundedValue(input.WorkflowID, 255), "session_id": boundedValue(input.SessionID, 80),
		"step_id": boundedValue(input.StepID, 128), "attempt_id": boundedValue(input.AttemptID, 80),
		"resource_id": boundedValue(input.ResourceID, 128), "artifact_id": boundedValue(input.ArtifactID, 80),
		"command_id": boundedValue(input.CommandID, 128), "external_ref": boundedValue(input.ExternalRef, 255),
		"finished_at": now, "updated_at": now,
	}
	updated := s.db.WithContext(ctx).Model(&orm.AgentInvocation{}).
		Where("id = ? AND owner_user_id = ? AND status = ?", id, owner, StatusRunning).Updates(updates)
	if updated.Error != nil {
		return orm.AgentInvocation{}, updated.Error
	}
	if updated.RowsAffected != 1 {
		if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", id, owner).Take(&record).Error; err == nil &&
			terminalStatus(record.Status) && sameFinish(record, input, resultSummary) {
			return record, nil
		}
		return orm.AgentInvocation{}, ErrConflict
	}
	if err := s.db.WithContext(ctx).Where("id = ? AND owner_user_id = ?", id, owner).Take(&record).Error; err != nil {
		return orm.AgentInvocation{}, err
	}
	return record, nil
}

// InterruptRunningForExternalRef closes MCP calls abandoned by an expired
// External Chat lease. It never changes terminal invocations and is executed
// in the same transaction that transfers the Run to the replacement Host.
func (s *Service) InterruptRunningForExternalRef(
	ctx context.Context,
	owner, externalRef string,
	finishedAt time.Time,
) (int64, error) {
	owner = strings.TrimSpace(owner)
	externalRef = strings.TrimSpace(externalRef)
	if s == nil || s.db == nil || !bounded(owner, 255) || !bounded(externalRef, 255) {
		return 0, ErrInvalidInput
	}
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	} else {
		finishedAt = finishedAt.UTC()
	}
	updated := s.db.WithContext(ctx).Model(&orm.AgentInvocation{}).
		Where("owner_user_id = ? AND external_ref = ? AND status = ?", owner, externalRef, StatusRunning).
		Updates(map[string]any{
			"status": StatusInterrupted, "error_code": "EXTERNAL_RUN_RECLAIMED",
			"retryable": true, "finished_at": finishedAt, "updated_at": finishedAt,
		})
	return updated.RowsAffected, updated.Error
}

func (s *Service) List(ctx context.Context, owner string, query ListQuery) (Page, error) {
	owner = strings.TrimSpace(owner)
	if s == nil || s.db == nil || !bounded(owner, 255) {
		return Page{}, ErrInvalidInput
	}
	limit := query.PageSize
	if limit == 0 {
		limit = defaultPageSize
	}
	if limit < 1 || limit > maxPageSize {
		return Page{}, ErrInvalidInput
	}
	db := s.db.WithContext(ctx).Where("owner_user_id = ?", owner)
	filters := []struct {
		column string
		value  string
		limit  int
	}{{"tool_name", query.ToolName, 128}, {"status", query.Status, 32}, {"client_name", query.ClientName, 128},
		{"workflow_id", query.WorkflowID, 255}, {"session_id", query.SessionID, 80}, {"external_ref", query.ExternalRef, 255}}
	for _, filter := range filters {
		value := strings.TrimSpace(filter.value)
		if value == "" {
			continue
		}
		if !bounded(value, filter.limit) {
			return Page{}, ErrInvalidInput
		}
		db = db.Where(filter.column+" = ?", value)
	}
	if strings.TrimSpace(query.PageToken) != "" {
		cursor, err := decodeCursor(query.PageToken)
		if err != nil {
			return Page{}, err
		}
		db = db.Where("started_at < ? OR (started_at = ? AND id < ?)", cursor.StartedAt, cursor.StartedAt, cursor.ID)
	}
	var records []orm.AgentInvocation
	if err := db.Order("started_at DESC, id DESC").Limit(limit + 1).Find(&records).Error; err != nil {
		return Page{}, err
	}
	page := Page{Invocations: records}
	if len(records) > limit {
		page.Invocations = records[:limit]
		last := page.Invocations[len(page.Invocations)-1]
		page.NextPageToken, _ = encodeCursor(pageCursor{StartedAt: last.StartedAt, ID: last.ID})
	}
	if page.Invocations == nil {
		page.Invocations = []orm.AgentInvocation{}
	}
	return page, nil
}

func terminalStatus(value string) bool {
	return value == StatusSucceeded || value == StatusFailed || value == StatusInterrupted
}

func normalizedSummary(value json.RawMessage) (json.RawMessage, error) {
	if len(value) == 0 || string(value) == "null" {
		return json.RawMessage(`{}`), nil
	}
	if len(value) > maxSummaryBytes || !json.Valid(value) {
		return nil, ErrInvalidInput
	}
	var object map[string]any
	if err := json.Unmarshal(value, &object); err != nil || object == nil {
		return nil, ErrInvalidInput
	}
	for key, entry := range object {
		if !validSummaryEntry(key, entry) {
			return nil, ErrInvalidInput
		}
	}
	normalized, err := json.Marshal(object)
	if err != nil || len(normalized) > maxSummaryBytes {
		return nil, ErrInvalidInput
	}
	return normalized, nil
}

func validSummaryEntry(key string, value any) bool {
	key = strings.TrimSpace(key)
	if summaryIdentifierKey(key) {
		identifier, ok := value.(string)
		return ok && validSummaryIdentifier(identifier)
	}
	if summaryScalarKey(key) {
		switch typed := value.(type) {
		case string:
			return len([]rune(typed)) <= 255
		case bool, float64:
			return true
		default:
			return false
		}
	}
	if key == "local_file_name" {
		name, ok := value.(string)
		return ok && name != "" && len([]rune(name)) <= 255 && filepath.Base(name) == name && !strings.ContainsAny(name, `/\\`)
	}
	if summaryIDListKey(key) {
		values, ok := value.([]any)
		if !ok || len(values) == 0 || len(values) > 50 {
			return false
		}
		for _, entry := range values {
			identifier, ok := entry.(string)
			if !ok || !validSummaryIdentifier(identifier) {
				return false
			}
		}
		return true
	}
	if summaryLengthKey(key) || summaryCountKey(key) {
		number, ok := value.(float64)
		return ok && number >= 0 && number == math.Trunc(number)
	}
	return false
}

func summaryScalarKey(key string) bool {
	switch key {
	case "external_ref", "executor_ref", "tool_name", "status", "outcome", "attempt_status", "state_version", "revision",
		"completed", "already_terminal", "read_only", "slot", "slot_id", "content_type":
		return true
	default:
		return false
	}
}

func summaryIdentifierKey(key string) bool {
	switch key {
	case "workflow_id", "revision_id", "preparation_id", "session_id", "step_id", "attempt_id",
		"execution_id", "producer_attempt_id", "resource_id", "artifact_id", "command_id",
		"knowledge_id", "document_id", "skill_id":
		return true
	default:
		return false
	}
}

func summaryIDListKey(key string) bool { return key == "knowledge_ids" }

func validSummaryIdentifier(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > 255 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("_.:-", character) {
			continue
		}
		return false
	}
	return true
}

func validOptionalSummaryIdentifier(value string) bool {
	return strings.TrimSpace(value) == "" || validSummaryIdentifier(value)
}

func summaryLengthKey(key string) bool {
	switch key {
	case "query_length", "request_context_length", "objective_length", "summary_length":
		return true
	default:
		return false
	}
}

func summaryCountKey(key string) bool {
	if !strings.HasSuffix(key, "_count") {
		return false
	}
	switch strings.TrimSuffix(key, "_count") {
	case "acceptance_criteria", "artifacts", "control_edges", "current", "declared_outputs", "edges",
		"hits", "items", "knowledge_ids", "legacy_tools", "optional_inputs", "outputs", "past", "reachable",
		"ready", "required_outputs", "rewindable", "sessions", "static_order", "tags", "typed_artifacts",
		"witnesses", "workflows":
		return true
	default:
		return false
	}
}

func sameStart(left, right orm.AgentInvocation) bool {
	return left.ID == right.ID && left.OwnerUserID == right.OwnerUserID &&
		left.ClientName == right.ClientName && left.ClientVersion == right.ClientVersion &&
		left.ConnectorName == right.ConnectorName && left.ConnectorVersion == right.ConnectorVersion &&
		left.ConnectorInstanceID == right.ConnectorInstanceID && left.ProtocolVersion == right.ProtocolVersion &&
		left.Transport == right.Transport && left.ToolName == right.ToolName && left.ReadOnly == right.ReadOnly &&
		left.ExternalRef == right.ExternalRef &&
		sameHash(left.RequestHash, right.RequestHash) && string(left.RequestSummary) == string(right.RequestSummary)
}

func sameFinish(record orm.AgentInvocation, input FinishInput, resultSummary json.RawMessage) bool {
	return record.Status == input.Status && string(record.ResultSummary) == string(resultSummary) &&
		record.ErrorCode == boundedValue(input.ErrorCode, 128) && record.Retryable == input.Retryable &&
		record.WorkflowID == boundedValue(input.WorkflowID, 255) && record.SessionID == boundedValue(input.SessionID, 80) &&
		record.StepID == boundedValue(input.StepID, 128) && record.AttemptID == boundedValue(input.AttemptID, 80) &&
		record.ResourceID == boundedValue(input.ResourceID, 128) && record.ArtifactID == boundedValue(input.ArtifactID, 80) &&
		record.CommandID == boundedValue(input.CommandID, 128) && record.ExternalRef == boundedValue(input.ExternalRef, 255)
}

func bounded(value string, limit int) bool {
	value = strings.TrimSpace(value)
	return value != "" && len([]rune(value)) <= limit
}

func boundedValue(value string, limit int) string {
	value = strings.TrimSpace(value)
	if runes := []rune(value); len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

func boundedDefault(value, fallback string, limit int) string {
	if strings.TrimSpace(value) == "" {
		value = fallback
	}
	return boundedValue(value, limit)
}

func validHash(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func sameHash(left, right string) bool {
	left, right = strings.ToLower(strings.TrimSpace(left)), strings.ToLower(strings.TrimSpace(right))
	return len(left) == len(right) && subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func encodeCursor(cursor pageCursor) (string, error) {
	value, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func decodeCursor(value string) (pageCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return pageCursor{}, ErrInvalidInput
	}
	var cursor pageCursor
	if json.Unmarshal(decoded, &cursor) != nil || cursor.StartedAt.IsZero() || !bounded(cursor.ID, 80) {
		return pageCursor{}, ErrInvalidInput
	}
	return cursor, nil
}
