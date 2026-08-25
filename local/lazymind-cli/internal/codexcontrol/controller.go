// Package codexcontrol implements the local side of Codex's remote-control
// transport. Codex app-server remains the sole owner of threads and turns;
// this package only carries JSON-RPC requests and notifications to that same
// process.
package codexcontrol

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	DefaultAddress      = "127.0.0.1:19091"
	protocolVersion     = "3"
	segmentTargetBytes  = 100 << 10
	maxMessageBytes     = 100 << 20
	requestTimeout      = 45 * time.Second
	connectionPingEvery = 10 * time.Second
)

var ErrUnavailable = errors.New("Codex native control is not connected; restart Codex after connecting it from LazyMind")

type Status struct {
	Connected      bool   `json:"connected"`
	Ready          bool   `json:"ready"`
	ServerID       string `json:"server_id,omitempty"`
	InstallationID string `json:"installation_id,omitempty"`
	ServerName     string `json:"server_name,omitempty"`
	LastError      string `json:"last_error,omitempty"`
}

type Notification struct {
	Method    string
	Params    json.RawMessage
	RequestID json.RawMessage
}

type Controller struct {
	mu          sync.Mutex
	connection  *connection
	pending     map[string]chan rpcResponse
	subscribers map[uint64]chan Notification
	nextSubID   uint64
	nextID      atomic.Int64
	status      Status
	upstream    http.Handler
}

type connection struct {
	ws       *websocket.Conn
	writeMu  sync.Mutex
	clientID string
	streamID string
	seq      uint64
	chunks   map[string]*chunkAssembly
}

type chunkAssembly struct {
	count int
	size  int
	next  int
	data  []byte
}

type rpcResponse struct {
	result json.RawMessage
	err    error
}

type incomingEnvelope struct {
	Type               string          `json:"type"`
	ClientID           string          `json:"client_id"`
	StreamID           string          `json:"stream_id"`
	SeqID              uint64          `json:"seq_id"`
	Message            json.RawMessage `json:"message"`
	SegmentID          int             `json:"segment_id"`
	SegmentCount       int             `json:"segment_count"`
	MessageSizeBytes   int             `json:"message_size_bytes"`
	MessageChunkBase64 string          `json:"message_chunk_base64"`
	Status             string          `json:"status"`
}

type rpcFrame struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type enrollmentRequest struct {
	Name           string `json:"name"`
	InstallationID string `json:"installation_id"`
}

func New() (*Controller, error) {
	upstreamURL, err := configuredUpstreamURL()
	if err != nil {
		return nil, err
	}
	upstream, err := newUpstreamProxy(upstreamURL)
	if err != nil {
		return nil, err
	}
	controller := &Controller{
		pending: make(map[string]chan rpcResponse), subscribers: make(map[uint64]chan Notification),
		upstream: upstream,
	}
	controller.nextID.Store(1)
	return controller, nil
}

func (c *Controller) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

func (c *Controller) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /backend-api/wham/remote/control/server/enroll", c.enroll)
	mux.HandleFunc("POST /backend-api/wham/remote/control/server/refresh", c.enroll)
	mux.HandleFunc("GET /backend-api/wham/remote/control/server", c.upgrade)
	mux.Handle("/backend-api/", c.upstream)
}

func newUpstreamProxy(rawURL string) (http.Handler, error) {
	return newUpstreamProxyWithTransport(rawURL, nil)
}

func newUpstreamProxyWithTransport(rawURL string, transport http.RoundTripper) (http.Handler, error) {
	target, err := url.Parse(rawURL)
	if err != nil || target.Scheme == "" || target.Host == "" {
		return nil, fmt.Errorf("invalid Codex ChatGPT upstream %q", rawURL)
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			path := strings.TrimPrefix(request.In.URL.Path, "/backend-api")
			request.Out.URL.Scheme = target.Scheme
			request.Out.URL.Host = target.Host
			request.Out.URL.Path = strings.TrimRight(target.Path, "/") + "/" + strings.TrimLeft(path, "/")
			request.Out.Host = target.Host
			request.SetXForwarded()
		},
		FlushInterval: -1,
		Transport:     transport,
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if !loopbackRequest(request) {
			http.Error(writer, "Codex upstream proxy is loopback-only", http.StatusForbidden)
			return
		}
		proxy.ServeHTTP(writer, request)
	}), nil
}

func (c *Controller) enroll(writer http.ResponseWriter, request *http.Request) {
	if !loopbackRequest(request) {
		http.Error(writer, "remote control is loopback-only", http.StatusForbidden)
		return
	}
	var input enrollmentRequest
	if err := json.NewDecoder(http.MaxBytesReader(writer, request.Body, 64<<10)).Decode(&input); err != nil {
		http.Error(writer, "invalid enrollment", http.StatusBadRequest)
		return
	}
	installationID := strings.TrimSpace(input.InstallationID)
	if installationID == "" {
		installationID = strings.TrimSpace(request.Header.Get("X-Codex-Installation-Id"))
	}
	if installationID == "" || len(installationID) > 128 {
		http.Error(writer, "installation_id is required", http.StatusBadRequest)
		return
	}
	serverID := stableID("srv", installationID)
	environmentID := stableID("env", installationID)
	token := serverToken(installationID, serverID)
	c.mu.Lock()
	c.status.ServerID = serverID
	c.status.InstallationID = installationID
	c.status.ServerName = strings.TrimSpace(input.Name)
	c.mu.Unlock()
	writeJSON(writer, http.StatusOK, map[string]any{
		"server_id": serverID, "environment_id": environmentID,
		"remote_control_token": token, "expires_at": time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
	})
}

func (c *Controller) upgrade(writer http.ResponseWriter, request *http.Request) {
	if !loopbackRequest(request) || request.Header.Get("X-Codex-Protocol-Version") != protocolVersion {
		http.Error(writer, "invalid remote-control connection", http.StatusForbidden)
		return
	}
	installationID := strings.TrimSpace(request.Header.Get("X-Codex-Installation-Id"))
	serverID := strings.TrimSpace(request.Header.Get("X-Codex-Server-Id"))
	if installationID == "" || serverID != stableID("srv", installationID) ||
		!validBearer(request.Header.Get("Authorization"), serverToken(installationID, serverID)) {
		http.Error(writer, "invalid remote-control token", http.StatusUnauthorized)
		return
	}
	upgrader := websocket.Upgrader{
		ReadBufferSize: 64 << 10, WriteBufferSize: 64 << 10,
		CheckOrigin: func(r *http.Request) bool { return loopbackRequest(r) },
	}
	ws, err := upgrader.Upgrade(writer, request, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(maxMessageBytes)
	conn := &connection{
		ws: ws, clientID: randomID("client"), streamID: randomID("stream"), seq: 1,
		chunks: make(map[string]*chunkAssembly),
	}
	c.installConnection(conn, serverID, installationID, request.Header.Get("X-Codex-Name"))
	defer c.removeConnection(conn)
	go c.initialize(conn)
	c.readLoop(conn)
}

func (c *Controller) installConnection(conn *connection, serverID, installationID, encodedName string) {
	c.mu.Lock()
	previous := c.connection
	c.connection = conn
	c.status = Status{Connected: true, ServerID: serverID, InstallationID: installationID}
	if decoded, err := base64.StdEncoding.DecodeString(encodedName); err == nil {
		c.status.ServerName = string(decoded)
	}
	c.failPendingLocked(ErrUnavailable)
	c.mu.Unlock()
	if previous != nil {
		_ = previous.ws.Close()
	}
}

func (c *Controller) removeConnection(conn *connection) {
	_ = conn.ws.Close()
	c.mu.Lock()
	if c.connection == conn {
		c.connection = nil
		c.status.Connected = false
		c.status.Ready = false
		if c.status.LastError == "" {
			c.status.LastError = "Codex remote-control connection closed"
		}
		c.failPendingLocked(ErrUnavailable)
	}
	c.mu.Unlock()
}

func (c *Controller) initialize(conn *connection) {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()
	_, err := c.requestOn(ctx, conn, "initialize", map[string]any{
		"clientInfo":   map[string]string{"name": "lazymind", "title": "LazyMind", "version": "1"},
		"capabilities": map[string]any{"experimentalApi": true},
	})
	if err == nil {
		err = c.notifyOn(conn, "initialized", map[string]any{})
	}
	c.mu.Lock()
	if c.connection == conn {
		c.status.Ready = err == nil
		if err != nil {
			c.status.LastError = err.Error()
		} else {
			c.status.LastError = ""
		}
	}
	c.mu.Unlock()
}

func (c *Controller) readLoop(conn *connection) {
	ping := time.NewTicker(connectionPingEvery)
	defer ping.Stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			_, body, err := conn.ws.ReadMessage()
			if err != nil {
				return
			}
			c.handleEnvelope(conn, body)
		}
	}()
	for {
		select {
		case <-done:
			return
		case <-ping.C:
			_ = c.writeEnvelope(conn, map[string]any{
				"type": "ping", "client_id": conn.clientID, "stream_id": conn.streamID,
			})
		}
	}
}

func (c *Controller) handleEnvelope(conn *connection, body []byte) {
	var envelope incomingEnvelope
	if json.Unmarshal(body, &envelope) != nil || envelope.ClientID != conn.clientID || envelope.StreamID != conn.streamID {
		return
	}
	switch envelope.Type {
	case "server_message":
		c.ack(conn, envelope.SeqID, nil)
		c.handleFrame(conn, envelope.Message)
	case "server_message_chunk":
		segment := envelope.SegmentID
		c.ack(conn, envelope.SeqID, &segment)
		if message := conn.observeChunk(envelope); message != nil {
			c.handleFrame(conn, message)
		}
	case "pong":
		c.ack(conn, envelope.SeqID, nil)
		if envelope.Status == "unknown" {
			go c.initialize(conn)
		}
	case "ack":
		c.ack(conn, envelope.SeqID, nil)
	}
}

func (c *Controller) handleFrame(conn *connection, body []byte) {
	var frame rpcFrame
	if json.Unmarshal(body, &frame) != nil {
		return
	}
	if len(frame.ID) > 0 && frame.Method == "" {
		key := string(frame.ID)
		c.mu.Lock()
		response := c.pending[key]
		delete(c.pending, key)
		c.mu.Unlock()
		if response != nil {
			if frame.Error != nil {
				response <- rpcResponse{err: fmt.Errorf("Codex %s (code %d)", frame.Error.Message, frame.Error.Code)}
			} else {
				response <- rpcResponse{result: append(json.RawMessage(nil), frame.Result...)}
			}
		}
		return
	}
	if frame.Method != "" {
		notification := Notification{Method: frame.Method, Params: append(json.RawMessage(nil), frame.Params...), RequestID: append(json.RawMessage(nil), frame.ID...)}
		c.broadcast(notification)
		if len(frame.ID) > 0 {
			_ = c.respond(conn, frame.ID, nil, map[string]any{"code": -32601, "message": "unsupported server request"})
		}
	}
}

func (c *Controller) Request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	conn := c.connection
	ready := c.status.Ready
	c.mu.Unlock()
	if conn == nil || !ready {
		return nil, ErrUnavailable
	}
	return c.requestOn(ctx, conn, method, params)
}

func (c *Controller) requestOn(ctx context.Context, conn *connection, method string, params any) (json.RawMessage, error) {
	id := c.nextID.Add(1)
	key := fmt.Sprintf("%d", id)
	response := make(chan rpcResponse, 1)
	c.mu.Lock()
	if c.connection != conn {
		c.mu.Unlock()
		return nil, ErrUnavailable
	}
	c.pending[key] = response
	c.mu.Unlock()
	if err := c.sendRPC(conn, map[string]any{"id": id, "method": method, "params": params}); err != nil {
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		return nil, err
	}
	select {
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, key)
		c.mu.Unlock()
		return nil, ctx.Err()
	case result := <-response:
		return result.result, result.err
	}
}

func (c *Controller) notifyOn(conn *connection, method string, params any) error {
	return c.sendRPC(conn, map[string]any{"method": method, "params": params})
}

func (c *Controller) sendRPC(conn *connection, message any) error {
	raw, err := json.Marshal(message)
	if err != nil {
		return err
	}
	conn.writeMu.Lock()
	defer conn.writeMu.Unlock()
	seq := conn.seq
	conn.seq++
	if len(raw) <= segmentTargetBytes {
		return conn.ws.WriteJSON(map[string]any{
			"type": "client_message", "client_id": conn.clientID, "stream_id": conn.streamID,
			"seq_id": seq, "message": json.RawMessage(raw),
		})
	}
	if len(raw) > maxMessageBytes {
		return errors.New("Codex remote-control request is too large")
	}
	count := (len(raw) + segmentTargetBytes - 1) / segmentTargetBytes
	for segment := 0; segment < count; segment++ {
		start := segment * segmentTargetBytes
		end := min(start+segmentTargetBytes, len(raw))
		if err := conn.ws.WriteJSON(map[string]any{
			"type": "client_message_chunk", "client_id": conn.clientID, "stream_id": conn.streamID,
			"seq_id": seq, "segment_id": segment, "segment_count": count,
			"message_size_bytes": len(raw), "message_chunk_base64": base64.StdEncoding.EncodeToString(raw[start:end]),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (c *Controller) ack(conn *connection, seq uint64, segment *int) {
	value := map[string]any{
		"type": "ack", "client_id": conn.clientID, "stream_id": conn.streamID, "seq_id": seq,
	}
	if segment != nil {
		value["segment_id"] = *segment
	}
	_ = c.writeEnvelope(conn, value)
}

func (c *Controller) respond(conn *connection, id json.RawMessage, result any, rpcError any) error {
	frame := map[string]any{"id": json.RawMessage(id)}
	if rpcError != nil {
		frame["error"] = rpcError
	} else {
		frame["result"] = result
	}
	return c.sendRPC(conn, frame)
}

func (c *Controller) writeEnvelope(conn *connection, value any) error {
	conn.writeMu.Lock()
	defer conn.writeMu.Unlock()
	return conn.ws.WriteJSON(value)
}

func (c *Controller) Subscribe() (<-chan Notification, func()) {
	c.mu.Lock()
	id := c.nextSubID
	c.nextSubID++
	channel := make(chan Notification, 512)
	c.subscribers[id] = channel
	c.mu.Unlock()
	return channel, func() {
		c.mu.Lock()
		if existing := c.subscribers[id]; existing != nil {
			delete(c.subscribers, id)
			close(existing)
		}
		c.mu.Unlock()
	}
}

func (c *Controller) broadcast(notification Notification) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, subscriber := range c.subscribers {
		select {
		case subscriber <- notification:
		default:
		}
	}
}

func (c *Controller) failPendingLocked(err error) {
	for key, response := range c.pending {
		delete(c.pending, key)
		response <- rpcResponse{err: err}
	}
}

func (conn *connection) observeChunk(envelope incomingEnvelope) []byte {
	if envelope.SegmentCount < 1 || envelope.SegmentCount > 1024 || envelope.MessageSizeBytes < 1 ||
		envelope.MessageSizeBytes > maxMessageBytes || envelope.SegmentID < 0 || envelope.SegmentID >= envelope.SegmentCount {
		return nil
	}
	key := fmt.Sprintf("%d", envelope.SeqID)
	assembly := conn.chunks[key]
	if assembly == nil {
		assembly = &chunkAssembly{count: envelope.SegmentCount, size: envelope.MessageSizeBytes, data: make([]byte, 0, envelope.MessageSizeBytes)}
		conn.chunks[key] = assembly
	}
	if assembly.count != envelope.SegmentCount || assembly.size != envelope.MessageSizeBytes || assembly.next != envelope.SegmentID {
		delete(conn.chunks, key)
		return nil
	}
	chunk, err := base64.StdEncoding.DecodeString(envelope.MessageChunkBase64)
	if err != nil || len(assembly.data)+len(chunk) > assembly.size {
		delete(conn.chunks, key)
		return nil
	}
	assembly.data = append(assembly.data, chunk...)
	assembly.next++
	if assembly.next != assembly.count {
		return nil
	}
	delete(conn.chunks, key)
	if len(assembly.data) != assembly.size {
		return nil
	}
	return assembly.data
}

func loopbackRequest(request *http.Request) bool {
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

func validBearer(value, expected string) bool {
	parts := strings.Fields(value)
	return len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && parts[1] == expected
}

func serverToken(installationID, serverID string) string {
	return stableID("token", installationID+"\x00"+serverID)
}

func stableID(prefix, seed string) string {
	var hash uint64 = 0xcbf29ce484222325
	for _, value := range []byte(seed) {
		hash ^= uint64(value)
		hash *= 0x100000001b3
	}
	return fmt.Sprintf("%s_%016x", prefix, hash)
}

func randomID(prefix string) string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(value)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
