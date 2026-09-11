package browser

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const ProtocolVersion = "1"

var (
	ErrDeviceOffline     = errors.New("browser device is offline")
	ErrDeviceNotFound    = errors.New("browser device not found")
	ErrPairingInvalid    = errors.New("browser pairing code is invalid or expired")
	ErrPermissionMissing = errors.New("browser permission is required")
)

type commandEnvelope struct {
	Type            string          `json:"type"`
	ProtocolVersion string          `json:"protocol_version"`
	ID              string          `json:"id"`
	Action          string          `json:"action"`
	DeadlineMS      int64           `json:"deadline_ms"`
	Payload         json.RawMessage `json:"payload,omitempty"`
}

type resultEnvelope struct {
	Type   string          `json:"type"`
	ID     string          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *protocolError  `json:"error,omitempty"`
}

type protocolError struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

func (e *protocolError) Error() string {
	if e == nil {
		return "browser command failed"
	}
	code := strings.TrimSpace(e.Code)
	message := strings.TrimSpace(e.Message)
	if code == "" {
		return message
	}
	if message == "" {
		return code
	}
	return fmt.Sprintf("%s: %s", code, message)
}

type helloEnvelope struct {
	Type            string `json:"type"`
	ProtocolVersion string `json:"protocol_version"`
	DeviceID        string `json:"device_id"`
	DeviceToken     string `json:"device_token"`
}

type pairingRecord struct {
	Code      string
	UserID    string
	ExpiresAt time.Time
}

type PairingResult struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

type PairExtensionInput struct {
	Code             string `json:"code"`
	DeviceName       string `json:"device_name"`
	Browser          string `json:"browser"`
	BrowserVersion   string `json:"browser_version,omitempty"`
	ExtensionVersion string `json:"extension_version,omitempty"`
	// Version is the legacy extension version field retained for older clients.
	Version string `json:"version,omitempty"`
}

type PairExtensionResult struct {
	ProtocolVersion string `json:"protocol_version"`
	DeviceID        string `json:"device_id"`
	DeviceToken     string `json:"device_token"`
}

type DeviceInfo struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Browser          string `json:"browser"`
	BrowserVersion   string `json:"browser_version,omitempty"`
	ExtensionVersion string `json:"extension_version,omitempty"`
	// Version mirrors ExtensionVersion for compatibility with existing clients.
	Version    string    `json:"version"`
	Online     bool      `json:"online"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type BrowserToolResult struct {
	Result map[string]any `json:"result"`
}

type DeviceInput struct {
	DeviceID string `json:"device_id,omitempty" jsonschema:"optional device ID; omit to use the most recently seen online browser"`
}

type CaptureInput struct {
	DeviceID string `json:"device_id,omitempty" jsonschema:"optional device ID; omit to use the most recently seen online browser"`
	Offset   int    `json:"offset,omitempty" jsonschema:"UTF-16 character offset for the next page; use content.next_offset from the previous result"`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"maximum characters to return in this page; defaults to 200000 and is capped at 400000"`
}

type OpenInput struct {
	DeviceID            string `json:"device_id,omitempty" jsonschema:"optional device ID"`
	URL                 string `json:"url" jsonschema:"http or https URL to open in a visible LazyMind-managed Chrome or Edge window"`
	AllowPrivateNetwork bool   `json:"allow_private_network,omitempty" jsonschema:"allow an explicitly requested localhost or private-network URL"`
}

type SessionInput struct {
	DeviceID  string `json:"device_id,omitempty" jsonschema:"optional device ID"`
	SessionID string `json:"session_id" jsonschema:"managed browser session ID returned by browser.open"`
}

type NavigateInput struct {
	DeviceID            string `json:"device_id,omitempty"`
	SessionID           string `json:"session_id"`
	URL                 string `json:"url"`
	AllowPrivateNetwork bool   `json:"allow_private_network,omitempty"`
}

type ClickInput struct {
	DeviceID         string `json:"device_id,omitempty"`
	SessionID        string `json:"session_id"`
	Ref              string `json:"ref" jsonschema:"element reference from the latest browser.snapshot"`
	ExpectedRevision int64  `json:"expected_revision,omitempty" jsonschema:"snapshot revision; stale references are rejected"`
}

type ClickIntersectionInput struct {
	DeviceID         string `json:"device_id,omitempty"`
	SessionID        string `json:"session_id"`
	RowRef           string `json:"row_ref" jsonschema:"element reference identifying the target row, such as a person name"`
	ColumnRef        string `json:"column_ref" jsonschema:"element reference identifying the target column, such as a date header"`
	ExpectedRevision int64  `json:"expected_revision,omitempty" jsonschema:"snapshot revision; stale references are rejected"`
}

type TypeInput struct {
	DeviceID         string `json:"device_id,omitempty"`
	SessionID        string `json:"session_id"`
	Ref              string `json:"ref"`
	Text             string `json:"text"`
	ExpectedRevision int64  `json:"expected_revision,omitempty"`
	Replace          bool   `json:"replace,omitempty" jsonschema:"replace existing text instead of appending"`
	VerifyText       string `json:"verify_text,omitempty" jsonschema:"optional visible text that must appear after typing; use this for document edits"`
}

type TypeFocusedInput struct {
	DeviceID   string `json:"device_id,omitempty"`
	SessionID  string `json:"session_id"`
	Text       string `json:"text"`
	Replace    bool   `json:"replace,omitempty" jsonschema:"replace text in the currently focused editable element before typing"`
	VerifyText string `json:"verify_text,omitempty" jsonschema:"optional visible text that must appear after typing; use this for document edits"`
}

type SelectInput struct {
	DeviceID         string `json:"device_id,omitempty"`
	SessionID        string `json:"session_id"`
	Ref              string `json:"ref"`
	Value            string `json:"value"`
	ExpectedRevision int64  `json:"expected_revision,omitempty"`
}

type PressInput struct {
	DeviceID  string `json:"device_id,omitempty"`
	SessionID string `json:"session_id"`
	Key       string `json:"key" jsonschema:"browser key value such as Enter, Escape, Tab, an arrow key, Backspace or Delete"`
}

type ScrollInput struct {
	DeviceID  string  `json:"device_id,omitempty"`
	SessionID string  `json:"session_id"`
	X         float64 `json:"x,omitempty"`
	Y         float64 `json:"y"`
}

type WaitInput struct {
	DeviceID  string `json:"device_id,omitempty"`
	SessionID string `json:"session_id"`
	Text      string `json:"text,omitempty" jsonschema:"optional visible text to wait for"`
	URL       string `json:"url,omitempty" jsonschema:"optional URL substring to wait for"`
	TimeoutMS int64  `json:"timeout_ms,omitempty" jsonschema:"maximum wait in milliseconds, capped at 30000"`
}

func payload(value any) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode browser command: %w", err)
	}
	return raw, nil
}
