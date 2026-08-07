package externalagent

import (
	"encoding/json"
	"errors"
)

const ProviderCodex = "codex"

var (
	ErrUnsupportedProvider = errors.New("unsupported external agent provider")
	ErrBindingNotFound     = errors.New("external agent binding not found")
	ErrThreadBusy          = errors.New("external agent thread is controlled by another user")
	ErrUnmanagedActive     = errors.New("unmanaged provider thread may still be active locally")
	ErrRequestNotFound     = errors.New("external agent request not found")
)

type ThreadStatus struct {
	Type        string   `json:"type"`
	ActiveFlags []string `json:"activeFlags,omitempty"`
}

type Thread struct {
	ID             string          `json:"id"`
	Name           *string         `json:"name,omitempty"`
	Preview        string          `json:"preview,omitempty"`
	Cwd            string          `json:"cwd,omitempty"`
	Source         string          `json:"source,omitempty"`
	Status         ThreadStatus    `json:"status"`
	Turns          json.RawMessage `json:"turns,omitempty"`
	CreatedAt      int64           `json:"createdAt,omitempty"`
	UpdatedAt      int64           `json:"updatedAt,omitempty"`
	Available      bool            `json:"available"`
	Managed        bool            `json:"managed_by_lazymind"`
	ConversationID string          `json:"conversation_id,omitempty"`
}

type ThreadPage struct {
	Data       []Thread `json:"data"`
	NextCursor *string  `json:"nextCursor"`
	Total      int      `json:"total"`
	HasMore    bool     `json:"has_more"`
}

type TurnPage struct {
	Thread     Thread          `json:"thread"`
	Turns      json.RawMessage `json:"turns"`
	Offset     int             `json:"offset"`
	Limit      int             `json:"limit"`
	TotalTurns int             `json:"total_turns"`
	HasMore    bool            `json:"has_more"`
}

type RunSnapshot struct {
	ConversationID   string  `json:"conversation_id"`
	Provider         string  `json:"provider"`
	ProviderThreadID string  `json:"provider_thread_id"`
	RunID            string  `json:"run_id,omitempty"`
	RequestID        string  `json:"request_id,omitempty"`
	Status           string  `json:"status"`
	Answer           string  `json:"answer,omitempty"`
	Events           []Event `json:"events,omitempty"`
	PendingRequestID string  `json:"pending_request_id,omitempty"`
}

type StartThreadInput struct {
	Cwd string `json:"cwd"`
}

type BindInput struct {
	Provider         string
	ProviderThreadID string
	ConversationID   string
	CreatedByUserID  string
	Managed          bool
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
	Type              string          `json:"type"`
	Provider          string          `json:"provider"`
	ThreadID          string          `json:"thread_id"`
	TurnID            string          `json:"turn_id,omitempty"`
	RunID             string          `json:"run_id,omitempty"`
	ProviderEventType string          `json:"provider_event_type,omitempty"`
	Delta             string          `json:"delta,omitempty"`
	Summary           string          `json:"summary,omitempty"`
	Message           string          `json:"message,omitempty"`
	RequestID         string          `json:"request_id,omitempty"`
	RequestKind       string          `json:"request_kind,omitempty"`
	RequestPayload    json.RawMessage `json:"request_payload,omitempty"`
	Status            string          `json:"status,omitempty"`
	Terminal          bool            `json:"-"`
}

type Execution struct {
	RunID     string
	HistoryID string
	Seq       int
	Events    <-chan Event
}

type RequestResponse struct {
	RequestID   string
	Payload     json.RawMessage
	ActorUserID string
}
