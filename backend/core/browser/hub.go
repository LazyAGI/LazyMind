package browser

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	corelog "lazymind/core/log"
)

const (
	defaultPairingTTL = 5 * time.Minute
	defaultCommandTTL = 30 * time.Second
	maxMessageBytes   = 16 << 20
)

type deviceRecord struct {
	ID             string
	UserID         string
	Name           string
	Browser        string
	BrowserVersion string
	Version        string
	TokenHash      [32]byte
	CreatedAt      time.Time
	LastSeenAt     time.Time
	Connection     *deviceConnection
}

type commandResponse struct {
	result json.RawMessage
	err    error
}

type browserCommandResultSummary struct {
	ResponseBytes      int
	Revision           int64
	ElementCount       int
	RawAXNodeCount     int
	InvisibleTextNodes int
	SnapshotMS         int64
	RoleCounts         map[string]int
	Limitations        []string
}

type deviceConnection struct {
	ws      *websocket.Conn
	send    chan commandEnvelope
	done    chan struct{}
	once    sync.Once
	mu      sync.Mutex
	pending map[string]chan commandResponse
}

func newDeviceConnection(ws *websocket.Conn) *deviceConnection {
	return &deviceConnection{
		ws: ws, send: make(chan commandEnvelope, 32), done: make(chan struct{}),
		pending: make(map[string]chan commandResponse),
	}
}

func (c *deviceConnection) close(err error) {
	c.once.Do(func() {
		close(c.done)
		if c.ws != nil {
			_ = c.ws.Close()
		}
		c.mu.Lock()
		pending := c.pending
		c.pending = make(map[string]chan commandResponse)
		c.mu.Unlock()
		if err == nil {
			err = ErrDeviceOffline
		}
		for _, ch := range pending {
			select {
			case ch <- commandResponse{err: err}:
			default:
			}
		}
	})
}

func (c *deviceConnection) register(id string) (chan commandResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.done:
		return nil, ErrDeviceOffline
	default:
	}
	ch := make(chan commandResponse, 1)
	c.pending[id] = ch
	return ch, nil
}

func (c *deviceConnection) resolve(message resultEnvelope) {
	c.mu.Lock()
	ch := c.pending[message.ID]
	delete(c.pending, message.ID)
	c.mu.Unlock()
	if ch == nil {
		return
	}
	if !message.OK {
		ch <- commandResponse{err: message.Error}
		return
	}
	ch <- commandResponse{result: message.Result}
}

func (c *deviceConnection) forget(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

type Hub struct {
	mu         sync.RWMutex
	pairings   map[string]pairingRecord
	devices    map[string]*deviceRecord
	pairingTTL time.Duration
	commandTTL time.Duration
	now        func() time.Time
	signer     *tokenSigner
}

func NewHub() (*Hub, error) {
	signer, err := newTokenSigner(nil)
	if err != nil {
		return nil, err
	}
	return &Hub{
		pairings: make(map[string]pairingRecord), devices: make(map[string]*deviceRecord),
		pairingTTL: defaultPairingTTL, commandTTL: defaultCommandTTL, now: time.Now, signer: signer,
	}, nil
}

var DefaultHub = mustNewHub()

func mustNewHub() *Hub {
	hub, err := NewHub()
	if err != nil {
		panic(err)
	}
	return hub
}

func (h *Hub) CreatePairing(userID string) (PairingResult, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return PairingResult{}, errors.New("user is required")
	}
	now := h.now().UTC()
	h.mu.Lock()
	defer h.mu.Unlock()
	h.prunePairingsLocked(now)
	for attempts := 0; attempts < 10; attempts++ {
		code, err := randomPairingCode()
		if err != nil {
			return PairingResult{}, err
		}
		if _, exists := h.pairings[code]; exists {
			continue
		}
		expires := now.Add(h.pairingTTL)
		h.pairings[code] = pairingRecord{Code: code, UserID: userID, ExpiresAt: expires}
		return PairingResult{Code: code, ExpiresAt: expires}, nil
	}
	return PairingResult{}, errors.New("could not allocate browser pairing code")
}

func (h *Hub) PairExtension(input PairExtensionInput) (PairExtensionResult, error) {
	code := normalizePairingCode(input.Code)
	now := h.now().UTC()
	h.mu.Lock()
	defer h.mu.Unlock()
	pairing, ok := h.pairings[code]
	if !ok || !pairing.ExpiresAt.After(now) {
		delete(h.pairings, code)
		return PairExtensionResult{}, ErrPairingInvalid
	}
	delete(h.pairings, code)
	token, err := randomToken()
	if err != nil {
		return PairExtensionResult{}, err
	}
	deviceID := "bd_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	name := strings.TrimSpace(input.DeviceName)
	if name == "" {
		name = "Chromium"
	}
	extensionVersion := strings.TrimSpace(input.ExtensionVersion)
	if extensionVersion == "" {
		extensionVersion = strings.TrimSpace(input.Version)
	}
	h.devices[deviceID] = &deviceRecord{
		ID: deviceID, UserID: pairing.UserID, Name: name,
		Browser: strings.TrimSpace(input.Browser), BrowserVersion: strings.TrimSpace(input.BrowserVersion),
		Version:   extensionVersion,
		TokenHash: sha256.Sum256([]byte(token)), CreatedAt: now, LastSeenAt: now,
	}
	return PairExtensionResult{ProtocolVersion: ProtocolVersion, DeviceID: deviceID, DeviceToken: token}, nil
}

func (h *Hub) AuthenticateDevice(deviceID, token string) (*deviceRecord, error) {
	deviceID = strings.TrimSpace(deviceID)
	provided := sha256.Sum256([]byte(strings.TrimSpace(token)))
	h.mu.RLock()
	device := h.devices[deviceID]
	if device == nil || subtle.ConstantTimeCompare(device.TokenHash[:], provided[:]) != 1 {
		h.mu.RUnlock()
		return nil, errors.New("invalid browser device credentials")
	}
	copy := *device
	h.mu.RUnlock()
	return &copy, nil
}

func (h *Hub) Attach(deviceID string, conn *deviceConnection) error {
	h.mu.Lock()
	device := h.devices[strings.TrimSpace(deviceID)]
	if device == nil {
		h.mu.Unlock()
		return ErrDeviceNotFound
	}
	previous := device.Connection
	device.Connection = conn
	device.LastSeenAt = h.now().UTC()
	h.mu.Unlock()
	if previous != nil && previous != conn {
		previous.close(errors.New("browser device connected from a newer session"))
	}
	return nil
}

func (h *Hub) Detach(deviceID string, conn *deviceConnection) {
	h.mu.Lock()
	device := h.devices[strings.TrimSpace(deviceID)]
	if device != nil && device.Connection == conn {
		device.Connection = nil
		device.LastSeenAt = h.now().UTC()
	}
	h.mu.Unlock()
}

func (h *Hub) ListDevices(userID string) []DeviceInfo {
	userID = strings.TrimSpace(userID)
	h.mu.RLock()
	out := make([]DeviceInfo, 0)
	for _, device := range h.devices {
		if device.UserID != userID {
			continue
		}
		out = append(out, DeviceInfo{
			ID: device.ID, Name: device.Name, Browser: device.Browser,
			BrowserVersion: device.BrowserVersion, ExtensionVersion: device.Version, Version: device.Version,
			Online: device.Connection != nil, CreatedAt: device.CreatedAt, LastSeenAt: device.LastSeenAt,
		})
	}
	h.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeenAt.After(out[j].LastSeenAt) })
	return out
}

func (h *Hub) RevokeDevice(userID, deviceID string) error {
	userID = strings.TrimSpace(userID)
	deviceID = strings.TrimSpace(deviceID)
	h.mu.Lock()
	device := h.devices[deviceID]
	if device == nil || device.UserID != userID {
		h.mu.Unlock()
		return ErrDeviceNotFound
	}
	delete(h.devices, deviceID)
	conn := device.Connection
	h.mu.Unlock()
	if conn != nil {
		conn.close(errors.New("browser device was revoked"))
	}
	return nil
}

func (h *Hub) RevokeAllDevices(userID string) int {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return 0
	}
	h.mu.Lock()
	connections := make([]*deviceConnection, 0)
	revoked := 0
	for deviceID, device := range h.devices {
		if device.UserID != userID {
			continue
		}
		delete(h.devices, deviceID)
		revoked++
		if device.Connection != nil {
			connections = append(connections, device.Connection)
		}
	}
	h.mu.Unlock()
	for _, connection := range connections {
		connection.close(errors.New("browser device was revoked"))
	}
	return revoked
}

func (h *Hub) Call(ctx context.Context, userID, deviceID, action string, value any) (json.RawMessage, error) {
	startedAt := time.Now()
	userID = strings.TrimSpace(userID)
	action = strings.TrimSpace(action)
	if userID == "" {
		return nil, errors.New("browser tool user is required")
	}
	if !allowedAction(action) {
		return nil, fmt.Errorf("unsupported browser action %q", action)
	}
	connection, err := h.onlineDevice(userID, deviceID)
	if err != nil {
		return nil, err
	}
	raw, err := payload(value)
	if err != nil {
		return nil, err
	}
	id := "bc_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	response, err := connection.register(id)
	if err != nil {
		return nil, err
	}
	ttl := h.commandTTL
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < ttl {
			ttl = remaining
		}
	}
	if ttl <= 0 {
		connection.forget(id)
		return nil, context.DeadlineExceeded
	}
	command := commandEnvelope{
		Type: "command", ProtocolVersion: ProtocolVersion, ID: id, Action: action,
		DeadlineMS: h.now().Add(ttl).UnixMilli(), Payload: raw,
	}
	select {
	case connection.send <- command:
	case <-connection.done:
		connection.forget(id)
		logBrowserCommand(action, deviceID, len(raw), nil, ErrDeviceOffline, time.Since(startedAt))
		return nil, ErrDeviceOffline
	case <-ctx.Done():
		connection.forget(id)
		logBrowserCommand(action, deviceID, len(raw), nil, ctx.Err(), time.Since(startedAt))
		return nil, ctx.Err()
	}
	timer := time.NewTimer(ttl)
	defer timer.Stop()
	select {
	case result := <-response:
		logBrowserCommand(action, deviceID, len(raw), result.result, result.err, time.Since(startedAt))
		return result.result, result.err
	case <-timer.C:
		connection.forget(id)
		err := fmt.Errorf("browser command %s timed out", action)
		logBrowserCommand(action, deviceID, len(raw), nil, err, time.Since(startedAt))
		return nil, err
	case <-ctx.Done():
		connection.forget(id)
		logBrowserCommand(action, deviceID, len(raw), nil, ctx.Err(), time.Since(startedAt))
		return nil, ctx.Err()
	case <-connection.done:
		connection.forget(id)
		logBrowserCommand(action, deviceID, len(raw), nil, ErrDeviceOffline, time.Since(startedAt))
		return nil, ErrDeviceOffline
	}
}

func logBrowserCommand(action, deviceID string, requestBytes int, raw json.RawMessage, commandErr error, elapsed time.Duration) {
	summary := summarizeBrowserCommandResult(raw)
	status := "ok"
	errorMessage := ""
	if commandErr != nil {
		status = "error"
		errorMessage = commandErr.Error()
	}
	event := corelog.Logger.Info()
	if commandErr != nil {
		event = corelog.Logger.Warn()
	}
	event.
		Str("component", "browser").
		Str("event", "browser.command.completed").
		Str("action", action).
		Str("status", status).
		Str("device_id", strings.TrimSpace(deviceID)).
		Int64("elapsed_ms", elapsed.Milliseconds()).
		Int("request_bytes", requestBytes).
		Int("response_bytes", summary.ResponseBytes).
		Int64("revision", summary.Revision).
		Int("elements", summary.ElementCount).
		Int("raw_ax_nodes", summary.RawAXNodeCount).
		Int("invisible_text_nodes", summary.InvisibleTextNodes).
		Int64("snapshot_ms", summary.SnapshotMS).
		Str("role_counts", formatBrowserRoleCounts(summary.RoleCounts)).
		Str("limitations", strings.Join(summary.Limitations, ",")).
		Str("error", errorMessage).
		Msg("browser command completed")
}

func summarizeBrowserCommandResult(raw json.RawMessage) browserCommandResultSummary {
	summary := browserCommandResultSummary{ResponseBytes: len(raw), RoleCounts: make(map[string]int)}
	if len(raw) == 0 {
		return summary
	}
	var response struct {
		Revision int64 `json:"revision"`
		Elements []struct {
			Role string `json:"role"`
		} `json:"elements"`
		Limitations     []string `json:"limitations"`
		SnapshotMetrics struct {
			RawAXNodes         int            `json:"raw_ax_nodes"`
			IncludedElements   int            `json:"included_elements"`
			InvisibleTextNodes int            `json:"invisible_text_nodes"`
			RoleCounts         map[string]int `json:"role_counts"`
			SnapshotMS         int64          `json:"snapshot_ms"`
		} `json:"snapshot_metrics"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return summary
	}
	summary.Revision = response.Revision
	summary.ElementCount = response.SnapshotMetrics.IncludedElements
	if summary.ElementCount == 0 {
		summary.ElementCount = len(response.Elements)
	}
	summary.RawAXNodeCount = response.SnapshotMetrics.RawAXNodes
	summary.InvisibleTextNodes = response.SnapshotMetrics.InvisibleTextNodes
	summary.SnapshotMS = response.SnapshotMetrics.SnapshotMS
	if len(response.SnapshotMetrics.RoleCounts) > 0 {
		summary.RoleCounts = response.SnapshotMetrics.RoleCounts
	} else {
		for _, element := range response.Elements {
			summary.RoleCounts[element.Role]++
		}
	}
	summary.Limitations = response.Limitations
	return summary
}

func formatBrowserRoleCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return "-"
	}
	roles := make([]string, 0, len(counts))
	for role := range counts {
		roles = append(roles, role)
	}
	sort.Strings(roles)
	parts := make([]string, 0, len(roles))
	for _, role := range roles {
		parts = append(parts, fmt.Sprintf("%s:%d", role, counts[role]))
	}
	return strings.Join(parts, ",")
}

func (h *Hub) onlineDevice(userID, deviceID string) (*deviceConnection, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if id := strings.TrimSpace(deviceID); id != "" {
		device := h.devices[id]
		if device == nil || device.UserID != userID {
			return nil, ErrDeviceNotFound
		}
		if device.Connection == nil {
			return nil, ErrDeviceOffline
		}
		return device.Connection, nil
	}
	preferredBrowser := strings.TrimSpace(os.Getenv("LAZYMIND_BROWSER_PREFERRED_DEVICE_BROWSER"))
	var selected *deviceRecord
	for _, device := range h.devices {
		if device.UserID != userID || device.Connection == nil {
			continue
		}
		devicePreferred := preferredBrowser != "" && strings.EqualFold(device.Browser, preferredBrowser)
		selectedPreferred := selected != nil && preferredBrowser != "" && strings.EqualFold(selected.Browser, preferredBrowser)
		if selected == nil || (devicePreferred && !selectedPreferred) ||
			(devicePreferred == selectedPreferred && device.LastSeenAt.After(selected.LastSeenAt)) {
			selected = device
		}
	}
	if selected == nil {
		return nil, ErrDeviceOffline
	}
	return selected.Connection, nil
}

func (h *Hub) ToolToken(userID string) (string, error) {
	return h.signer.issue(userID, time.Hour)
}

func (h *Hub) VerifyToolToken(token string) (toolClaims, error) {
	return h.signer.verify(token)
}

func (h *Hub) prunePairingsLocked(now time.Time) {
	for code, pairing := range h.pairings {
		if !pairing.ExpiresAt.After(now) {
			delete(h.pairings, code)
		}
	}
}

func randomPairingCode() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	for i := range raw {
		raw[i] = alphabet[int(raw[i])%len(alphabet)]
	}
	return string(raw[:4]) + "-" + string(raw[4:]), nil
}

func normalizePairingCode(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "")
	if len(value) == 8 {
		value = value[:4] + "-" + value[4:]
	}
	return value
}

func randomToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func allowedAction(action string) bool {
	switch action {
	case "capture_current_page", "open", "navigate", "snapshot", "click", "click_intersection", "type", "type_focused", "select", "press", "scroll", "wait", "screenshot", "tabs", "close":
		return true
	default:
		return false
	}
}
