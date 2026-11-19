package agentintegration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"lazymind/agentconnector/internal/desktopsetup"
	"lazymind/agentconnector/internal/localfile"
)

// Inspection contains only the connection fields needed to diagnose setup.
type Inspection struct {
	Path      string `json:"config_path"`
	Command   string `json:"configured_cli,omitempty"`
	Reference string `json:"connection_file,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

type Operation struct {
	home, agent, action, id string
	Stage                   string
	started                 time.Time
	CLI                     string
	Before                  []Inspection
	After                   []Inspection
	Connection              *ConnectionStatus
	Reason                  string
}

func NewOperation(home, agent, action, cli string) *Operation {
	var id [16]byte
	_, _ = rand.Read(id[:])
	return &Operation{home: home, agent: agent, action: action, CLI: cli, id: hex.EncodeToString(id[:]), started: time.Now()}
}

func (o *Operation) Enter(stage string) { o.Stage = stage; o.record("running", "", nil) }

func (o *Operation) Finish(status *Status) {
	o.Connection, o.Reason = status.Connection, status.DiagnosticReason
	code := ""
	if status.Issue != nil {
		status.Issue.Stage, status.Issue.OperationID = o.Stage, o.id
		code = status.Issue.Code
	}
	status.DiagnosticID, status.DiagnosticLog = o.id, diagnosticPath(o.home)
	status.DiagnosticContext = diagnosticContext(o.home, o.action, o.Stage, o.CLI, o.After)
	o.record(string(status.State), code, status.Enabled)
}

func diagnosticContext(home, action, stage, cli string, inspections []Inspection) *DiagnosticContext {
	return &DiagnosticContext{Action: action, Stage: stage, CurrentCLI: safePath(cli),
		AppVersion: os.Getenv("LAZYMIND_DESKTOP_APP_VERSION"), Environment: diagnosticEnvironment(home), Configuration: safeInspections(inspections)}
}

// Only bootstrap paths and profiles are copied. Empty values must also be
// applied when rechecking, so the caller cannot inherit another installation.
func diagnosticEnvironment(home string) map[string]string {
	env := map[string]string{"LAZYMIND_HOME": safePath(home)}
	for _, name := range []string{"LAZYMIND_RUNTIME_PROFILE", "LAZYMIND_CONNECTOR_CONFIG",
		"CODEX_HOME", "LAZYMIND_HOST_HOME", "DSH_HOME", "LAZYMIND_DSH_PROFILE",
		"LAZYMIND_CODEX_BIN", "LAZYMIND_DSH_PATH", "LAZYMIND_DSH_DESKTOP_PATH"} {
		env[name] = safePath(strings.TrimSpace(os.Getenv(name)))
	}
	return env
}

func (o *Operation) record(state, code string, configured *bool) {
	row := map[string]any{"time": time.Now().UTC().Format(time.RFC3339Nano), "agent": o.agent,
		"operation_id": o.id, "action": o.action, "stage": o.Stage, "state": state, "code": code,
		"elapsed_ms": time.Since(o.started).Milliseconds(), "current_cli": safePath(o.CLI), "configured": configured,
		"before": safeInspections(o.Before), "after": safeInspections(o.After), "reason": o.Reason, "connection": o.Connection, "app_version": os.Getenv("LAZYMIND_DESKTOP_APP_VERSION")}
	appendDiagnostic(o.home, row)
}

func diagnosticPath(home string) string { return filepath.Join(home, "logs", "agent-setup.log") }

func safePath(path string) string {
	user, err := os.UserHomeDir()
	if err == nil && user != "" {
		if path == user {
			return "~"
		}
		if strings.HasPrefix(path, user+string(filepath.Separator)) {
			return "~" + strings.TrimPrefix(path, user)
		}
	}
	return path
}

func safeInspections(items []Inspection) []Inspection {
	result := make([]Inspection, len(items))
	for i, item := range items {
		item.Path, item.Command, item.Reference = safePath(item.Path), safePath(item.Command), safePath(item.Reference)
		result[i] = item
	}
	return result
}

func appendDiagnostic(home string, row any) {
	path := diagnosticPath(home)
	if os.MkdirAll(filepath.Dir(path), 0700) != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer file.Close()
	// Encode one allowlisted record per write. Status messages and raw config are excluded.
	body, err := json.Marshal(row)
	if err == nil {
		_, _ = file.Write(append(body, '\n'))
	}
}

// RecordInspection reuses the diagnostic ID while a polled configuration stays unchanged.
// The small cache is diagnostic metadata, never an input to installation or authorization.
func RecordInspection(home, cli string, inspections []Inspection, status *Status) {
	status.DiagnosticContext = diagnosticContext(home, "inspect", "inspect", cli, inspections)
	if os.MkdirAll(filepath.Join(home, "logs"), 0700) != nil {
		return
	}
	path := filepath.Join(home, "logs", "agent-"+status.Agent+"-inspection.json")
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	unlock, err := localfile.LockContext(ctx, path+".lock")
	if err != nil {
		return
	}
	defer unlock()
	code := ""
	if status.Issue != nil {
		code = status.Issue.Code
	}
	body, _ := json.Marshal(struct {
		CLI         string
		Items       []Inspection
		Enabled     *bool
		State       State
		Code        string
		Connection  *ConnectionStatus
		AppVersion  string
		Environment map[string]string
	}{cli, inspections, status.Enabled, status.State, code, status.Connection, status.DiagnosticContext.AppVersion, status.DiagnosticContext.Environment})
	hash := sha256.Sum256(body)
	fingerprint := hex.EncodeToString(hash[:])
	var previous struct {
		Fingerprint string `json:"fingerprint"`
		ID          string `json:"id"`
	}
	old, _ := os.ReadFile(path)
	if json.Unmarshal(old, &previous) == nil && previous.Fingerprint == fingerprint {
		status.DiagnosticID, status.DiagnosticLog = previous.ID, diagnosticPath(home)
		if status.Issue != nil {
			status.Issue.Stage, status.Issue.OperationID = "inspect", previous.ID
		}
		return
	}
	operation := NewOperation(home, status.Agent, "inspect", cli)
	operation.Stage = "inspect"
	operation.After = inspections
	operation.Finish(status)
	previous.Fingerprint, previous.ID = fingerprint, operation.id
	saved, _ := json.Marshal(previous)
	_ = localfile.Write(path, saved, 0600)
}

// ErrorReason classifies errors without recording raw command output or credentials.
func ErrorReason(err error) string {
	if errors.Is(err, context.Canceled) {
		return "cancelled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	if errors.Is(err, os.ErrPermission) {
		return "permission_denied"
	}
	if errors.Is(err, os.ErrNotExist) {
		return "file_missing"
	}
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		return "invalid_json"
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno.Error()
	}
	return "operation_failed"
}

func FailError(status Status, err error) Status {
	if errors.Is(err, desktopsetup.ErrQuitRequired) {
		return RequireQuit(status, status.RetryAction)
	}
	status = Fail(status, err.Error())
	status.DiagnosticReason = ErrorReason(err)
	return status
}
