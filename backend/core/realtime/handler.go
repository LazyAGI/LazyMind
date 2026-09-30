// Package realtime multiplexes the existing chat SSE handlers over a WebSocket.
// It never issues network requests to caller-provided URLs or replays operations.
package realtime

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const maxStreams = 64

var subscriptionPath = regexp.MustCompile(`^/(conversations/[A-Za-z0-9_-]+/events|tasks/[A-Za-z0-9_-]+:stream|workflow-sessions/[A-Za-z0-9_-]+/events)$`)

func allowed(method, path string) bool {
	return method == http.MethodGet && subscriptionPath.MatchString(path) ||
		method == http.MethodPost && (path == "/conversations:chat" || path == "/conversations:resumeChat")
}

type Handler struct {
	Routes    http.Handler
	Authorize func(*http.Request) int
}

type command struct {
	Type          string `json:"type"`
	ID            string `json:"id"`
	Method        string `json:"method"`
	Path          string `json:"path"`
	Authorization string `json:"authorization"`
	Payload       string `json:"payload"`
	Language      string `json:"language"`
	LastEventID   string `json:"last_event_id"`
}

type frame struct {
	Type   string `json:"type"`
	ID     string `json:"id,omitempty"`
	Status int    `json:"status,omitempty"`
	// Bytes are base64 encoded by JSON; chunks may split a UTF-8 character.
	Data []byte `json:"data,omitempty"`
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		HandshakeTimeout: 10 * time.Second,
		Subprotocols:     []string{"lazymind.realtime.v1"},
		// Credentials are explicit in each open frame, never ambient cookies.
		// Cross-origin frontends are supported, just like VITE_API_BASE_URL.
		CheckOrigin: func(r *http.Request) bool {
			origin, err := url.Parse(r.Header.Get("Origin"))
			return err == nil && origin.Host != "" && (origin.Scheme == "http" || origin.Scheme == "https")
		},
	}
	if !containsProtocol(websocket.Subprotocols(r), "lazymind.realtime.v1") {
		http.Error(w, "unsupported realtime protocol", http.StatusBadRequest)
		return
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	ws.SetReadLimit(16 << 20) // Chat payloads can contain inline images.
	var authenticated atomic.Bool
	_ = ws.SetReadDeadline(time.Now().Add(30 * time.Second))
	ws.SetPongHandler(func(string) error {
		if authenticated.Load() {
			return ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		}
		return nil
	})
	var writeMu sync.Mutex
	send := func(message frame) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := ws.WriteJSON(message); err != nil {
			cancel()
			_ = ws.Close()
			return err
		}
		return nil
	}
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)) != nil {
					cancel()
					_ = ws.Close()
					return
				}
				if send(frame{Type: "heartbeat"}) != nil {
					return
				}
			}
		}
	}()
	var mu sync.Mutex
	streams := map[string]context.CancelFunc{}
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		for _, stop := range streams {
			stop()
		}
	}()
	for {
		var input command
		if ws.ReadJSON(&input) != nil {
			return
		}
		if authenticated.Load() {
			_ = ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		}
		if input.Type == "cancel" {
			mu.Lock()
			if stop := streams[input.ID]; stop != nil {
				stop()
			}
			mu.Unlock()
			continue
		}
		if input.Type != "open" || input.ID == "" || len(input.ID) > 128 {
			return
		}
		parsed, err := url.ParseRequestURI(input.Path)
		if err != nil || parsed.IsAbs() || parsed.Host != "" || parsed.RawPath != "" || !allowed(input.Method, parsed.Path) {
			_ = send(frame{Type: "error", ID: input.ID, Status: http.StatusBadRequest})
			continue
		}
		mu.Lock()
		_, duplicate := streams[input.ID]
		if duplicate || len(streams) >= maxStreams {
			mu.Unlock()
			if duplicate {
				return
			} // Never re-execute a potentially mutating request.
			_ = send(frame{Type: "error", ID: input.ID, Status: http.StatusTooManyRequests})
			continue
		}
		streamCtx, stop := context.WithCancel(ctx)
		streams[input.ID] = stop
		mu.Unlock()
		go func(input command) {
			defer func() { stop(); mu.Lock(); delete(streams, input.ID); mu.Unlock() }()
			req, err := http.NewRequestWithContext(streamCtx, input.Method, input.Path, strings.NewReader(input.Payload))
			if err != nil {
				_ = send(frame{Type: "error", ID: input.ID, Status: http.StatusBadRequest})
				return
			}
			req.Header.Set("Authorization", input.Authorization)
			req.Header.Set("Accept", "text/event-stream")
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept-Language", input.Language)
			req.Header.Set("Last-Event-ID", input.LastEventID)
			if status := h.Authorize(req); status != http.StatusOK {
				_ = send(frame{Type: "error", ID: input.ID, Status: status})
				return
			}
			authenticated.Store(true)
			writer := &streamWriter{ctx: streamCtx, id: input.ID, header: make(http.Header), send: send}
			defer func() {
				if recover() != nil {
					_ = send(frame{Type: "error", ID: input.ID, Status: http.StatusInternalServerError})
				}
			}()
			if streamCtx.Err() == nil {
				h.Routes.ServeHTTP(writer, req)
			}
			writer.Flush()
			_ = send(frame{Type: "end", ID: input.ID})
		}(input)
	}
}

func containsProtocol(protocols []string, wanted string) bool {
	for _, protocol := range protocols {
		if protocol == wanted {
			return true
		}
	}
	return false
}

type streamWriter struct {
	ctx    context.Context
	id     string
	header http.Header
	status int
	err    error
	send   func(frame) error
}

func (w *streamWriter) Header() http.Header { return w.header }
func (w *streamWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.err = w.send(frame{Type: "headers", ID: w.id, Status: status})
}
func (w *streamWriter) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
}
func (w *streamWriter) Write(data []byte) (int, error) {
	w.Flush()
	if w.err != nil {
		return 0, w.err
	}
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	written := 0
	for len(data) > 0 {
		n := min(len(data), 32<<10)
		if err := w.send(frame{Type: "data", ID: w.id, Data: data[:n]}); err != nil {
			return written, err
		}
		data = data[n:]
		written += n
	}
	return written, nil
}

var _ http.Flusher = (*streamWriter)(nil)
var _ http.ResponseWriter = (*streamWriter)(nil)
