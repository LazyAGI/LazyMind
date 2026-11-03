package assistantbridge

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"lazymind/agentconnector/internal/codexhost"
	"lazymind/agentconnector/internal/integrationstate"
	"lazymind/agentconnector/internal/localfile"
	"lazymind/agentconnector/internal/workflowhost"
)

// The broker carries UI requests only. HTTP clients cannot approve a restart.
// A restart is owned by Electron after a click in its dedicated local window.
type codexRecoveryBroker struct {
	mu        sync.Mutex
	changed   chan struct{}
	pending   *codexhost.RecoveryRequest
	connector string
	storage   string
}

var recoveryThreadID = regexp.MustCompile(`(?i)^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var recoveryIdentifier = regexp.MustCompile(`^[a-f0-9]{32}$`)

func (b *codexRecoveryBroker) bindStorage(home, identity string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	sum := sha256.Sum256([]byte(identity))
	path := filepath.Join(home, "codex-workflow", "recovery", hex.EncodeToString(sum[:])+".json")
	if b.storage == path {
		return nil
	}
	var saved struct {
		Connector string                     `json:"connector"`
		Request   *codexhost.RecoveryRequest `json:"request"`
	}
	body, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil && (len(body) > 1<<20 || json.Unmarshal(body, &saved) != nil) {
		return errors.New("invalid saved recovery request")
	}
	b.storage, b.connector, b.pending = path, saved.Connector, saved.Request
	if b.changed != nil {
		close(b.changed)
	}
	b.changed = make(chan struct{})
	return nil
}

func (b *codexRecoveryBroker) persist(connector string, request *codexhost.RecoveryRequest) error {
	if b.storage == "" {
		return nil
	}
	if request == nil {
		err := os.Remove(b.storage)
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	body, err := json.Marshal(struct {
		Connector string                     `json:"connector"`
		Request   *codexhost.RecoveryRequest `json:"request"`
	}{connector, request})
	if err != nil {
		return err
	}
	if len(body) > 1<<20 {
		return errors.New("saved recovery request is too large")
	}
	if err = os.MkdirAll(filepath.Dir(b.storage), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(b.storage), ".recovery-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(body); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return localfile.Replace(file.Name(), b.storage)
}

func copyRecovery(request *codexhost.RecoveryRequest) *codexhost.RecoveryRequest {
	copy := *request
	copy.Intents = append([]codexhost.RecoveryIntent(nil), request.Intents...)
	return &copy
}

func (b *codexRecoveryBroker) current(connector string) (*codexhost.RecoveryRequest, <-chan struct{}) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.changed == nil {
		b.changed = make(chan struct{})
	}
	if b.pending != nil && b.connector == connector {
		return copyRecovery(b.pending), b.changed
	}
	return nil, b.changed
}

func (b *codexRecoveryBroker) request(connector string, issue codexhost.ConnectionIssue, submissions ...codexhost.RecoverySubmission) (*codexhost.RecoveryRequest, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var next *codexhost.RecoveryRequest
	if b.pending != nil && b.connector == connector {
		next = copyRecovery(b.pending)
	}
	if next == nil {
		var nonce [16]byte
		if _, err := rand.Read(nonce[:]); err != nil {
			return nil, err
		}
		next = &codexhost.RecoveryRequest{ID: hex.EncodeToString(nonce[:])}
	}
	// Coalesce saved operations, but always use the latest connection diagnosis.
	next.ConnectionIssue = issue
	for _, input := range submissions {
		if !input.ViewOnly {
			next.ViewOnly = false
		}
		if input.ViewOnly && len(next.Intents) == 0 {
			next.ViewOnly = true
		}
		if next.ThreadID == "" {
			next.ThreadID = input.ThreadID
		}
		if input.Intent == nil {
			continue
		}
		duplicate := false
		for _, saved := range next.Intents {
			if saved.Fingerprint == input.Intent.Fingerprint {
				duplicate = true
				break
			}
		}
		if !duplicate {
			if len(next.Intents) >= 16 {
				return nil, errors.New("too many pending recovery operations")
			}
			next.Intents = append(next.Intents, *input.Intent)
			next.ViewOnly = false
		}
	}
	if err := b.persist(connector, next); err != nil {
		return nil, err
	}
	b.pending = next
	b.connector = connector
	if b.changed != nil {
		close(b.changed)
	}
	b.changed = make(chan struct{})
	return copyRecovery(b.pending), nil
}

func (b *codexRecoveryBroker) dismiss(connector, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pending != nil && b.connector == connector && b.pending.ID == id {
		if err := b.persist(connector, nil); err != nil {
			return err
		}
		b.pending = nil
		if b.changed != nil {
			close(b.changed)
		}
		b.changed = make(chan struct{})
	}
	return nil
}

func (b *codexRecoveryBroker) accepted(connector, request, intent string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pending == nil || b.connector != connector || b.pending.ID != request {
		return errors.New("recovery request expired")
	}
	next := copyRecovery(b.pending)
	found := false
	for i := range next.Intents {
		if next.Intents[i].ID == intent {
			next.Intents[i].Accepted = true
			found = true
		}
	}
	if !found {
		return errors.New("recovery operation expired")
	}
	if err := b.persist(connector, next); err != nil {
		return err
	}
	b.pending = next
	return nil
}

func (b *codexRecoveryBroker) complete(connector, request string, viewOnly ...bool) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.pending == nil || b.connector != connector || b.pending.ID != request {
		return errors.New("recovery request expired")
	}
	if len(viewOnly) > 0 && viewOnly[0] && (!b.pending.ViewOnly || len(b.pending.Intents) > 0) {
		return errors.New("recovery is no longer presentation-only")
	}
	for _, intent := range b.pending.Intents {
		if !intent.Accepted {
			return errors.New("recovery operations remain pending")
		}
	}
	if err := b.persist(connector, nil); err != nil {
		return err
	}
	b.pending = nil
	if b.changed != nil {
		close(b.changed)
	}
	b.changed = make(chan struct{})
	return nil
}

func (s *Server) recoveryPair(w http.ResponseWriter, r *http.Request) (workflowhost.Pairing, bool) {
	scope, err := s.store.AccountScope()
	if err == nil && integrationstate.Allowed(s.store.Directory(), "codex") {
		pair, e := workflowhost.Verify(s.store.Directory(), r.Header.Get("X-LazyMind-Connector-Id"), strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), scope)
		if e == nil && pair.Provider == "codex" {
			return pair, true
		}
	}
	writeJSON(w, http.StatusForbidden, map[string]string{"error": "Active Codex pairing required"})
	return workflowhost.Pairing{}, false
}

func (s *Server) handleCodexRecovery(w http.ResponseWriter, r *http.Request) {
	pair, ok := s.recoveryPair(w, r)
	if !ok {
		return
	}
	if err := s.codexRecovery.bindStorage(s.store.Directory(), pair.ConnectorID+":"+pair.Token); err != nil {
		writeJSON(w, 500, map[string]string{"error": "Cannot read saved recovery request"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodPost:
		if r.PathValue("request") != "" {
			var err error
			if intent := r.PathValue("intent"); intent != "" {
				err = s.codexRecovery.accepted(pair.ConnectorID, r.PathValue("request"), intent)
			} else if strings.HasSuffix(r.URL.Path, "/view-complete") {
				err = s.codexRecovery.complete(pair.ConnectorID, r.PathValue("request"), true)
			} else {
				err = s.codexRecovery.complete(pair.ConnectorID, r.PathValue("request"))
			}
			if err != nil {
				writeJSON(w, 409, map[string]string{"error": err.Error()})
				return
			}
			writeJSON(w, 200, map[string]bool{"ok": true})
			return
		}
		var submission codexhost.RecoverySubmission
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&submission) != nil {
			writeJSON(w, 422, map[string]string{"error": "Invalid recovery request"})
			return
		}
		issue := submission.ConnectionIssue
		if submission.ThreadID != "" && !recoveryThreadID.MatchString(submission.ThreadID) {
			writeJSON(w, 422, map[string]string{"error": "Invalid recovery conversation"})
			return
		}
		if intent := submission.Intent; intent != nil {
			if !validRecoveryIntent(*intent) || intent.ThreadID != submission.ThreadID {
				writeJSON(w, 422, map[string]string{"error": "Invalid recovery operation"})
				return
			}
		}
		if issue.Code == "CODEX_CONNECTION_CHECK" {
			bounded, cancel := context.WithTimeout(r.Context(), 10*time.Second)
			defer cancel()
			err := codexhost.CheckConnection(bounded, s.store.Directory())
			if err == nil {
				writeJSON(w, 200, codexhost.RecoveryResponse{})
				return
			}
			var checked *codexhost.ConnectionIssue
			if !errors.As(err, &checked) {
				writeJSON(w, 503, map[string]string{"error": "Connection check unavailable"})
				return
			}
			issue = *checked
			if _, ok := s.recoveryPair(w, r); !ok {
				return
			}
		}
		// Do not accept arbitrary commands or client-controlled restart eligibility.
		switch issue.Code {
		case "CODEX_CONNECTION_REQUIRED":
			issue.CanRestart = true
		case "CODEX_CDP_UNAVAILABLE", "CODEX_CONTROL_UNAVAILABLE", "CODEX_CONFIGURATION_UNAVAILABLE":
			issue.CanRestart = false
		default:
			writeJSON(w, 422, map[string]string{"error": "Unsupported connection issue"})
			return
		}
		if len(issue.Message) > 2000 {
			issue.Message = issue.Message[:2000]
		}
		request, err := s.codexRecovery.request(pair.ConnectorID, issue, submission)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "Cannot create recovery request"})
			return
		}
		writeJSON(w, 200, codexhost.RecoveryResponse{Request: request})
	case http.MethodDelete:
		if err := s.codexRecovery.dismiss(pair.ConnectorID, r.PathValue("request")); err != nil {
			writeJSON(w, 500, map[string]string{"error": "Cannot dismiss recovery"})
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	case http.MethodGet:
		request, changed := s.codexRecovery.current(pair.ConnectorID)
		if request == nil && r.URL.Query().Get("wait") != "0" {
			timer := time.NewTimer(20 * time.Second)
			defer timer.Stop()
			select {
			case <-r.Context().Done():
				return
			case <-timer.C:
			case <-changed:
			}
		}
		// A long poll must not hand a previous account's request to a new login.
		if _, ok := s.recoveryPair(w, r); !ok {
			return
		}
		request, _ = s.codexRecovery.current(pair.ConnectorID)
		writeJSON(w, 200, codexhost.RecoveryResponse{Request: request})
	}
}

func validRecoveryIntent(intent codexhost.RecoveryIntent) bool {
	if !recoveryIdentifier.MatchString(intent.ID) || !recoveryThreadID.MatchString(intent.ThreadID) || len(intent.Fingerprint) != 64 || intent.Accepted || len(intent.Arguments) > 128<<10 {
		return false
	}
	if _, err := hex.DecodeString(intent.Fingerprint); err != nil {
		return false
	}
	var args map[string]any
	if json.Unmarshal(intent.Arguments, &args) != nil || args == nil {
		return false
	}
	text := func(key string) string { value, _ := args[key].(string); return strings.TrimSpace(value) }
	switch intent.Tool {
	case "workflow.start":
		return text("workflow_id") != "" && len(text("idempotency_key")) >= 16 && len(text("idempotency_key")) <= 256 && text("driver_session_id") == intent.ThreadID
	case "workflow.step.begin":
		return text("session_id") != "" && text("step_id") != "" && text("command_id") != ""
	case "workflow.step.claim", "workflow.step.resume":
		return text("session_id") != "" && text("execution_id") != ""
	}
	return false
}
