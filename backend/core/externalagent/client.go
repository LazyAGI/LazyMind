package externalagent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *rpcError) Error() string { return e.Message }

type rpcCallError struct {
	method   string
	response *rpcError
}

func (e *rpcCallError) Error() string {
	return fmt.Sprintf(
		"request failed: codex %s: code=%d message=%s",
		e.method, e.response.Code, e.response.Message,
	)
}

type transportUnavailableError struct{ cause error }

func (e *transportUnavailableError) Error() string {
	return "request failed: codex app-server unavailable: " + e.cause.Error()
}

func (e *transportUnavailableError) Unwrap() error { return e.cause }

func isTransportUnavailable(err error) bool {
	var unavailable *transportUnavailableError
	return errors.As(err, &unavailable)
}

type codexConfigurationError struct{ cause error }

func (e *codexConfigurationError) Error() string {
	return "codex assistant is not configured: install and sign in to Codex on this device"
}

func (e *codexConfigurationError) Unwrap() error { return e.cause }

func isCodexConfigurationError(err error) bool {
	var configurationError *codexConfigurationError
	return errors.As(err, &configurationError)
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

func (m rpcMessage) isServerRequest() bool {
	return len(m.ID) > 0 && m.Method != ""
}

type messageTransport interface {
	ReadMessage() ([]byte, error)
	WriteMessage([]byte) error
	Close() error
}

type websocketTransport struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func dialWebsocket(endpoint, token string) (messageTransport, error) {
	header := make(http.Header)
	if token != "" {
		header.Set("Authorization", "Bearer "+token)
	}
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 10 * time.Second
	conn, _, err := dialer.Dial(endpoint, header)
	if err != nil {
		return nil, err
	}
	return &websocketTransport{conn: conn}, nil
}

func (t *websocketTransport) ReadMessage() ([]byte, error) {
	_, payload, err := t.conn.ReadMessage()
	return payload, err
}

func (t *websocketTransport) WriteMessage(payload []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.conn.WriteMessage(websocket.TextMessage, payload)
}

func (t *websocketTransport) Close() error { return t.conn.Close() }

type stdioTransport struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	scanner *bufio.Scanner
	mu      sync.Mutex
}

func startStdio(binary string) (messageTransport, error) {
	cmd := exec.Command(binary, "app-server")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return nil, err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	return &stdioTransport{cmd: cmd, stdin: stdin, scanner: scanner}, nil
}

func (t *stdioTransport) ReadMessage() ([]byte, error) {
	if !t.scanner.Scan() {
		if err := t.scanner.Err(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	return append([]byte(nil), t.scanner.Bytes()...), nil
}

func (t *stdioTransport) WriteMessage(payload []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, err := t.stdin.Write(append(payload, '\n'))
	return err
}

func (t *stdioTransport) Close() error {
	_ = t.stdin.Close()
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	return t.cmd.Wait()
}

type CodexClient struct {
	factory    func() (messageTransport, error)
	events     chan rpcMessage
	nextID     atomic.Int64
	generation atomic.Int64
	connectMu  sync.Mutex
	mu         sync.Mutex
	transport  messageTransport
	pending    map[string]chan rpcMessage
}

func NewCodexClient() *CodexClient {
	endpoint := strings.TrimSpace(os.Getenv("LAZYMIND_CODEX_APP_SERVER_URL"))
	token := strings.TrimSpace(os.Getenv("LAZYMIND_CODEX_APP_SERVER_TOKEN"))
	binaryName := strings.TrimSpace(os.Getenv("LAZYMIND_CODEX_BIN"))
	if binaryName == "" {
		binaryName = "codex"
	}
	factory := func() (messageTransport, error) {
		if endpoint != "" {
			parsed, err := url.Parse(endpoint)
			if err != nil || parsed.Host == "" || (parsed.Scheme != "ws" && parsed.Scheme != "wss") {
				return nil, &codexConfigurationError{
					cause: fmt.Errorf("invalid LAZYMIND_CODEX_APP_SERVER_URL %q", endpoint),
				}
			}
			return dialWebsocket(endpoint, token)
		}
		binary, err := exec.LookPath(binaryName)
		if err != nil {
			return nil, &codexConfigurationError{cause: err}
		}
		return startStdio(binary)
	}
	return &CodexClient{
		factory: factory,
		events:  make(chan rpcMessage, 512),
		pending: make(map[string]chan rpcMessage),
	}
}

func (c *CodexClient) Events() <-chan rpcMessage { return c.events }

func (c *CodexClient) ensureConnected(ctx context.Context) error {
	c.mu.Lock()
	connected := c.transport != nil
	c.mu.Unlock()
	if connected {
		return nil
	}

	c.connectMu.Lock()
	defer c.connectMu.Unlock()
	c.mu.Lock()
	connected = c.transport != nil
	c.mu.Unlock()
	if connected {
		return nil
	}

	transport, err := c.factory()
	if err != nil {
		if isCodexConfigurationError(err) {
			return err
		}
		return &transportUnavailableError{cause: err}
	}
	c.mu.Lock()
	c.transport = transport
	c.mu.Unlock()
	go c.readLoop(transport)

	initCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var initialized map[string]any
	if err := c.callConnected(initCtx, "initialize", map[string]any{
		"clientInfo": map[string]string{
			"name":    "lazymind",
			"title":   "LazyMind Codex Assistant",
			"version": "0.1.0",
		},
		"capabilities": map[string]bool{
			"experimentalApi": true,
		},
	}, &initialized); err != nil {
		c.disconnect(transport, err)
		return err
	}
	if err := c.notifyConnected("initialized", map[string]any{}); err != nil {
		c.disconnect(transport, err)
		return &transportUnavailableError{cause: err}
	}
	c.generation.Add(1)
	return nil
}

func (c *CodexClient) Generation() int64 { return c.generation.Load() }

func (c *CodexClient) Call(ctx context.Context, method string, params, result any) error {
	for {
		if err := c.ensureConnected(ctx); err != nil {
			if !isTransportUnavailable(err) {
				return err
			}
		} else if err := c.callConnected(ctx, method, params, result); err == nil {
			return nil
		} else if !isTransportUnavailable(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func (c *CodexClient) callConnected(ctx context.Context, method string, params, result any) error {
	id := strconv.FormatInt(c.nextID.Add(1), 10)
	waiter := make(chan rpcMessage, 1)
	c.mu.Lock()
	transport := c.transport
	if transport == nil {
		c.mu.Unlock()
		return &transportUnavailableError{cause: io.EOF}
	}
	c.pending[id] = waiter
	c.mu.Unlock()

	payload, err := json.Marshal(map[string]any{"method": method, "id": json.Number(id), "params": params})
	if err != nil {
		c.removePending(id)
		return err
	}
	if err := transport.WriteMessage(payload); err != nil {
		c.removePending(id)
		c.disconnect(transport, err)
		return &transportUnavailableError{cause: err}
	}

	select {
	case response := <-waiter:
		if response.Error != nil {
			if response.Error.Code == -1 {
				return &transportUnavailableError{cause: response.Error}
			}
			return &rpcCallError{method: method, response: response.Error}
		}
		if result == nil || len(response.Result) == 0 {
			return nil
		}
		return json.Unmarshal(response.Result, result)
	case <-ctx.Done():
		c.removePending(id)
		return ctx.Err()
	}
}

func (c *CodexClient) notifyConnected(method string, params any) error {
	payload, err := json.Marshal(map[string]any{"method": method, "params": params})
	if err != nil {
		return err
	}
	c.mu.Lock()
	transport := c.transport
	c.mu.Unlock()
	if transport == nil {
		return io.EOF
	}
	return transport.WriteMessage(payload)
}

func (c *CodexClient) Respond(id json.RawMessage, result any) error {
	payload, err := json.Marshal(struct {
		ID     json.RawMessage `json:"id"`
		Result any             `json:"result"`
	}{ID: id, Result: result})
	if err != nil {
		return err
	}
	c.mu.Lock()
	transport := c.transport
	c.mu.Unlock()
	if transport == nil {
		return io.EOF
	}
	return transport.WriteMessage(payload)
}

func (c *CodexClient) RespondError(id json.RawMessage, code int, message string) error {
	payload, err := json.Marshal(struct {
		ID    json.RawMessage `json:"id"`
		Error rpcError        `json:"error"`
	}{ID: id, Error: rpcError{Code: code, Message: message}})
	if err != nil {
		return err
	}
	c.mu.Lock()
	transport := c.transport
	c.mu.Unlock()
	if transport == nil {
		return io.EOF
	}
	return transport.WriteMessage(payload)
}

func (c *CodexClient) readLoop(transport messageTransport) {
	for {
		payload, err := transport.ReadMessage()
		if err != nil {
			c.disconnect(transport, err)
			return
		}
		var message rpcMessage
		if err := json.Unmarshal(payload, &message); err != nil {
			continue
		}
		if len(message.ID) > 0 && message.Method == "" {
			key := string(message.ID)
			c.mu.Lock()
			waiter := c.pending[key]
			delete(c.pending, key)
			c.mu.Unlock()
			if waiter != nil {
				waiter <- message
			}
			continue
		}
		c.events <- message
	}
}

func (c *CodexClient) removePending(id string) {
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
}

func (c *CodexClient) disconnect(transport messageTransport, cause error) {
	c.mu.Lock()
	if c.transport != transport {
		c.mu.Unlock()
		return
	}
	c.transport = nil
	pending := c.pending
	c.pending = make(map[string]chan rpcMessage)
	c.mu.Unlock()
	_ = transport.Close()
	for id, waiter := range pending {
		waiter <- rpcMessage{ID: json.RawMessage(id), Error: &rpcError{Code: -1, Message: cause.Error()}}
	}
	params, _ := json.Marshal(map[string]string{"message": cause.Error()})
	select {
	case c.events <- rpcMessage{Method: "lazymind/transport/disconnected", Params: params}:
	default:
	}
}
