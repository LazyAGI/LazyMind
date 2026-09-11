package browser

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"

	"lazymind/core/common"
	"lazymind/core/store"
)

type HTTPHandler struct {
	Hub *Hub
}

func NewHTTPHandler(hub *Hub) *HTTPHandler {
	if hub == nil {
		hub = DefaultHub
	}
	return &HTTPHandler{Hub: hub}
}

func (h *HTTPHandler) CreatePairing(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "browser pairing requires an authenticated user", http.StatusUnauthorized)
		return
	}
	result, err := h.Hub.CreatePairing(userID)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, result)
}

func (h *HTTPHandler) ListDevices(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "browser devices require an authenticated user", http.StatusUnauthorized)
		return
	}
	common.ReplyOK(w, map[string]any{"devices": h.Hub.ListDevices(userID)})
}

func (h *HTTPHandler) RevokeDevice(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "browser device revoke requires an authenticated user", http.StatusUnauthorized)
		return
	}
	if err := h.Hub.RevokeDevice(userID, mux.Vars(r)["device_id"]); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrDeviceNotFound) {
			status = http.StatusNotFound
		}
		common.ReplyErr(w, err.Error(), status)
		return
	}
	common.ReplyOK(w, map[string]any{"revoked": true})
}

func (h *HTTPHandler) RevokeAllDevices(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "browser device revoke requires an authenticated user", http.StatusUnauthorized)
		return
	}
	common.ReplyOK(w, map[string]any{"revoked": h.Hub.RevokeAllDevices(userID)})
}

func (h *HTTPHandler) PairExtension(w http.ResponseWriter, r *http.Request) {
	var input PairExtensionInput
	if err := decodeJSONBody(w, r, &input); err != nil {
		return
	}
	result, err := h.Hub.PairExtension(input)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrPairingInvalid) {
			status = http.StatusUnauthorized
		}
		writeJSON(w, status, map[string]any{"error": map[string]any{"code": "PAIRING_INVALID", "message": err.Error()}})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *HTTPHandler) ConnectExtension(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{
		HandshakeTimeout: 10 * time.Second,
		CheckOrigin:      validExtensionOrigin,
		Subprotocols:     []string{"lazymind.browser.v1"},
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ws.SetReadLimit(maxMessageBytes)
	_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	var hello helloEnvelope
	if err := ws.ReadJSON(&hello); err != nil {
		_ = ws.WriteJSON(map[string]any{"type": "hello_error", "code": "INVALID_HELLO"})
		_ = ws.Close()
		return
	}
	if hello.Type != "hello" || hello.ProtocolVersion != ProtocolVersion {
		_ = ws.WriteJSON(map[string]any{"type": "hello_error", "code": "PROTOCOL_MISMATCH", "protocol_version": ProtocolVersion})
		_ = ws.Close()
		return
	}
	device, err := h.Hub.AuthenticateDevice(hello.DeviceID, hello.DeviceToken)
	if err != nil {
		_ = ws.WriteJSON(map[string]any{"type": "hello_error", "code": "DEVICE_UNAUTHORIZED"})
		_ = ws.Close()
		return
	}
	_ = ws.SetReadDeadline(time.Time{})
	conn := newDeviceConnection(ws)
	if err := h.Hub.Attach(device.ID, conn); err != nil {
		_ = ws.Close()
		return
	}
	defer func() {
		conn.close(ErrDeviceOffline)
		h.Hub.Detach(device.ID, conn)
	}()
	if err := ws.WriteJSON(map[string]any{"type": "hello_ack", "protocol_version": ProtocolVersion, "device_id": device.ID}); err != nil {
		return
	}
	go connectionWriter(conn)
	connectionReader(conn)
}

func connectionWriter(conn *deviceConnection) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case command := <-conn.send:
			_ = conn.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.ws.WriteJSON(command); err != nil {
				conn.close(err)
				return
			}
		case <-ticker.C:
			_ = conn.ws.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
				conn.close(err)
				return
			}
		case <-conn.done:
			return
		}
	}
}

func connectionReader(conn *deviceConnection) {
	conn.ws.SetPongHandler(func(string) error { return nil })
	for {
		var message resultEnvelope
		if err := conn.ws.ReadJSON(&message); err != nil {
			conn.close(err)
			return
		}
		if message.Type != "result" || strings.TrimSpace(message.ID) == "" {
			continue
		}
		conn.resolve(message)
	}
}

func validExtensionOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if parsed.Scheme == "chrome-extension" || parsed.Scheme == "edge-extension" {
		return true
	}
	host := strings.ToLower(parsed.Hostname())
	return (parsed.Scheme == "http" || parsed.Scheme == "https") && (host == "127.0.0.1" || host == "localhost")
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]any{"code": "INVALID_REQUEST", "message": err.Error()}})
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
