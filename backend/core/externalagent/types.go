package externalagent

import (
	"encoding/json"
	"errors"
)

const ProviderCodex = "codex"

var (
	ErrUnsupportedProvider = errors.New("unsupported external agent provider")
	ErrInvalidCursor       = errors.New("invalid external agent thread cursor")
	ErrThreadNotFound      = errors.New("external agent thread not found")
	ErrBindingNotFound     = errors.New("external agent binding not found")
	ErrThreadBusy          = errors.New("external agent thread is controlled by another user")
	ErrUnmanagedActive     = errors.New("unmanaged provider thread may still be active locally")
	ErrRequestNotFound     = errors.New("external agent request not found")
	ErrReleasePending      = errors.New("external agent control release is already pending")
)

type ThreadStatus struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"activeFlags,omitempty"`
}

type Thread struct {
	ID                   string          `json:"id"`
	Name                 *string         `json:"name,omitempty"`
	Preview              string          `json:"preview,omitempty"`
	Cwd                  string          `json:"cwd,omitempty"`
	Source               string          `json:"source,omitempty"`
	Status               ThreadStatus    `json:"status"`
	CanAcceptInput       *bool           `json:"canAcceptDirectInput,omitempty"`
	Turns                json.RawMessage `json:"turns,omitempty"`
	CreatedAt            int64           `json:"createdAt,omitempty"`
	UpdatedAt            int64           `json:"updatedAt,omitempty"`
	Available            bool            `json:"available"`
	CreatedByLazyMind    bool            `json:"created_by_lazymind,omitempty"`
	ControlledByLazyMind bool            `json:"controlled_by_lazymind,omitempty"`
	BoundByOther         bool            `json:"bound_by_other,omitempty"`
	ConversationID       string          `json:"conversation_id,omitempty"`
}

type ThreadPage struct {
	Data       []Thread `json:"data"`
	NextCursor *string  `json:"nextCursor"`
	Total      int      `json:"total"`
	HasMore    bool     `json:"has_more"`
}

type Project struct {
	Cwd         string `json:"cwd"`
	Name        string `json:"name"`
	ThreadCount int    `json:"thread_count"`
	UpdatedAt   int64  `json:"updated_at,omitempty"`
}

type ProjectList struct {
	Data       []Project `json:"data"`
	NextCursor *string   `json:"nextCursor"`
	Total      int       `json:"total"`
	HasMore    bool      `json:"has_more"`
}

type TurnSummary struct {
	Role string `json:"role"`
	Text string `json:"text"`
}

type TurnPage struct {
	Thread     Thread        `json:"thread"`
	Turns      []TurnSummary `json:"turns"`
	Offset     int           `json:"offset"`
	Limit      int           `json:"limit"`
	TotalTurns int           `json:"total_turns"`
	HasMore    bool          `json:"has_more"`
	Snapshot   *RunSnapshot  `json:"snapshot,omitempty"`
}

type RunSnapshot struct {
	ConversationID string           `json:"conversation_id"`
	RunID          string           `json:"run_id,omitempty"`
	Status         string           `json:"status"`
	Answer         string           `json:"answer,omitempty"`
	PendingRequest *ExternalRequest `json:"pending_request,omitempty"`
	ControlRelease string           `json:"control_release,omitempty"`
}

type ExternalRequest struct {
	RequestID string                    `json:"request_id"`
	Kind      string                    `json:"kind"`
	Summary   string                    `json:"summary,omitempty"`
	Error     string                    `json:"error,omitempty"`
	Fields    []ExternalRequestField    `json:"fields,omitempty"`
	Actions   []ExternalRequestAction   `json:"actions,omitempty"`
	Questions []ExternalRequestQuestion `json:"questions,omitempty"`
}

type ExternalRequestField struct {
	Kind     string `json:"kind"`
	Value    string `json:"value"`
	Language string `json:"language,omitempty"`
}

type ExternalRequestAction struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
}

type ExternalRequestOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

type ExternalRequestQuestion struct {
	ID         string                  `json:"id"`
	Header     string                  `json:"header,omitempty"`
	Question   string                  `json:"question"`
	Options    []ExternalRequestOption `json:"options,omitempty"`
	AllowOther bool                    `json:"allow_other,omitempty"`
}

type ExternalRequestAnswer struct {
	Answers []string `json:"answers"`
}

type StartThreadInput struct {
	Cwd string `json:"cwd"`
}

type BindInput struct {
	Provider          string
	ProviderThreadID  string
	ConversationID    string
	CreatedByUserID   string
	CreatedByLazyMind bool
}

type ChatInput struct {
	Provider         string
	ProviderThreadID string
	ConversationID   string
	HistoryID        string
	RequestID        string
	Query            string
	ActorUserID      string
	Seq              int
}

type Event struct {
	Type           string           `json:"type"`
	Provider       string           `json:"provider"`
	ThreadID       string           `json:"thread_id"`
	TurnID         string           `json:"turn_id,omitempty"`
	RunID          string           `json:"run_id,omitempty"`
	Delta          string           `json:"delta,omitempty"`
	Summary        string           `json:"summary,omitempty"`
	Message        string           `json:"message,omitempty"`
	Request        *ExternalRequest `json:"request,omitempty"`
	Status         string           `json:"status,omitempty"`
	ControlRelease string           `json:"control_release,omitempty"`
	ControlError   string           `json:"control_error,omitempty"`
	Terminal       bool             `json:"terminal,omitempty"`
}

type RunUpdate struct {
	ConversationID string      `json:"conversation_id"`
	HistoryID      string      `json:"history_id"`
	Seq            int         `json:"seq"`
	Event          Event       `json:"event"`
	Snapshot       RunSnapshot `json:"snapshot"`
}

type Execution struct {
	RunID     string
	HistoryID string
	Seq       int
	Events    <-chan Event
	Cancel    func()
}

type RequestResponse struct {
	RequestID   string
	ActionID    string
	Answers     map[string]ExternalRequestAnswer
	ActorUserID string
}
