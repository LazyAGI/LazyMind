package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"sort"
)

const (
	runtimeDiagnosticCodePortConflict          = "RUNTIME_PORT_CONFLICT"
	runtimeDiagnosticCodePermissionDenied      = "RUNTIME_PERMISSION_DENIED"
	runtimeDiagnosticCodeDependencyMissing     = "RUNTIME_DEPENDENCY_MISSING"
	runtimeDiagnosticCodeProcessExited         = "RUNTIME_PROCESS_EXITED"
	runtimeDiagnosticCodeHealthTimeout         = "RUNTIME_HEALTH_TIMEOUT"
	runtimeDiagnosticCodeStopTimeout           = "RUNTIME_STOP_TIMEOUT"
	runtimeDiagnosticCodeInstanceConflict      = "RUNTIME_INSTANCE_CONFLICT"
	runtimeDiagnosticCodeUnknown               = "RUNTIME_UNKNOWN"
	runtimeDiagnosticOperationUp               = "up"
	runtimeDiagnosticOperationDown             = "down"
	runtimeFailureFactProcessExited            = "process-exited"
	runtimeFailureFactDependencyMissing        = "dependency-missing"
	runtimeFailureFactHealthTimeout            = "health-timeout"
	runtimeFailureFactStopTimeout              = "stop-timeout"
	runtimeFailureFactInstanceConflict         = "instance-conflict"
	runtimeDiagnosticPhasePreflight            = "preflight"
	runtimeDiagnosticPhasePythonPayload        = "python-payload"
	runtimeDiagnosticPhasePythonRelocation     = "python-relocation"
	runtimeDiagnosticPhaseConfiguration        = "configuration"
	runtimeDiagnosticPhaseSupervisorStart      = "supervisor-start"
	runtimeDiagnosticPhaseServiceReadiness     = "service-readiness"
	runtimeDiagnosticPhaseShutdown             = "shutdown"
	runtimeDiagnosticPhaseShutdownVerification = "shutdown-verification"
)

type RuntimeDiagnostic struct {
	Code      string                    `json:"code"`
	Operation string                    `json:"operation"`
	Phase     string                    `json:"phase"`
	Service   string                    `json:"service,omitempty"`
	Message   string                    `json:"message"`
	Retryable bool                      `json:"retryable"`
	Action    string                    `json:"action"`
	LogPath   string                    `json:"logPath,omitempty"`
	Details   *RuntimeDiagnosticDetails `json:"details,omitempty"`
}
type RuntimeDiagnosticDetails struct {
	HealthURL        string   `json:"healthUrl,omitempty"`
	TimeoutMs        int64    `json:"timeoutMs,omitempty"`
	Address          string   `json:"address,omitempty"`
	Port             int      `json:"port,omitempty"`
	Path             string   `json:"path,omitempty"`
	Dependency       string   `json:"dependency,omitempty"`
	Attempt          int      `json:"attempt,omitempty"`
	MaxAttempts      int      `json:"maxAttempts,omitempty"`
	BlockingServices []string `json:"blockingServices,omitempty"`
}
type runtimeFailureContext struct {
	Operation        string
	Phase            string
	Fact             string
	Service          string
	LogPath          string
	HealthURL        string
	Address          string
	Port             int
	Path             string
	Dependency       string
	TimeoutMs        int64
	Attempt          int
	MaxAttempts      int
	BlockingServices []string
}
type runtimeDiagnosticError struct {
	Cause      error
	Diagnostic *RuntimeDiagnostic
}

func (e *runtimeDiagnosticError) Error() string { return e.Cause.Error() }
func (e *runtimeDiagnosticError) Unwrap() error { return e.Cause }

func attachRuntimeDiagnostic(err error, ctx runtimeFailureContext) error {
	if err == nil {
		return nil
	}
	var existing *runtimeDiagnosticError
	if errors.As(err, &existing) {
		return err
	}
	diagnostic := classifyRuntimeFailure(err, ctx)
	return &runtimeDiagnosticError{Cause: err, Diagnostic: &diagnostic}
}

func runtimeDiagnosticFromError(err error) (*RuntimeDiagnostic, bool) {
	var diagnosticErr *runtimeDiagnosticError
	if !errors.As(err, &diagnosticErr) || diagnosticErr.Diagnostic == nil {
		return nil, false
	}
	return diagnosticErr.Diagnostic, true
}

func classifyRuntimeFailure(err error, ctx runtimeFailureContext) RuntimeDiagnostic {
	code := runtimeDiagnosticCodeUnknown
	switch {
	case isStartupPortConflict(err):
		code = runtimeDiagnosticCodePortConflict
	case errors.Is(err, fs.ErrPermission):
		code = runtimeDiagnosticCodePermissionDenied
	case ctx.Fact == runtimeFailureFactDependencyMissing && ctx.Dependency != "" &&
		(errors.Is(err, fs.ErrNotExist) || errors.Is(err, exec.ErrNotFound)):
		code = runtimeDiagnosticCodeDependencyMissing
	case ctx.Fact == runtimeFailureFactProcessExited:
		code = runtimeDiagnosticCodeProcessExited
	case ctx.Fact == runtimeFailureFactHealthTimeout && !errors.Is(err, context.Canceled):
		code = runtimeDiagnosticCodeHealthTimeout
	case ctx.Fact == runtimeFailureFactStopTimeout && !errors.Is(err, context.Canceled):
		code = runtimeDiagnosticCodeStopTimeout
	case ctx.Fact == runtimeFailureFactInstanceConflict:
		code = runtimeDiagnosticCodeInstanceConflict
	}
	message, action, retryable := runtimeDiagnosticCopy(code, ctx.Service)
	return RuntimeDiagnostic{
		Code:      code,
		Operation: diagnosticOperation(ctx.Operation),
		Phase:     ctx.Phase,
		Service:   ctx.Service,
		Message:   message,
		Retryable: retryable,
		Action:    action,
		LogPath:   ctx.LogPath,
		Details:   runtimeDiagnosticDetails(ctx),
	}
}

func diagnosticOperation(operation string) string {
	if operation == runtimeDiagnosticOperationDown {
		return runtimeDiagnosticOperationDown
	}
	return runtimeDiagnosticOperationUp
}

func runtimeDiagnosticDetails(ctx runtimeFailureContext) *RuntimeDiagnosticDetails {
	details := &RuntimeDiagnosticDetails{
		HealthURL: ctx.HealthURL, TimeoutMs: ctx.TimeoutMs, Address: ctx.Address,
		Port: ctx.Port, Path: ctx.Path, Dependency: ctx.Dependency,
		Attempt: ctx.Attempt, MaxAttempts: ctx.MaxAttempts,
		BlockingServices: append([]string(nil), ctx.BlockingServices...),
	}
	if len(details.BlockingServices) > 0 {
		sort.Strings(details.BlockingServices)
		details.BlockingServices = uniqueStrings(details.BlockingServices)
	}
	if details.HealthURL == "" && details.TimeoutMs == 0 && details.Address == "" &&
		details.Port == 0 && details.Path == "" && details.Dependency == "" &&
		details.Attempt == 0 && details.MaxAttempts == 0 && len(details.BlockingServices) == 0 {
		return nil
	}
	return details
}

func uniqueStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func runtimeDiagnosticCopy(code, service string) (string, string, bool) {
	serviceLabel := service
	if serviceLabel == "" {
		serviceLabel = "runtime service"
	}
	switch code {
	case runtimeDiagnosticCodePortConflict:
		return fmt.Sprintf("%s could not bind the requested port.", serviceLabel), "Free the port or retry startup.", true
	case runtimeDiagnosticCodePermissionDenied:
		return "The runtime could not access a required file or directory.", "Check runtime permissions and retry.", true
	case runtimeDiagnosticCodeDependencyMissing:
		return "A required runtime dependency is missing.", "Restore the dependency and retry.", true
	case runtimeDiagnosticCodeProcessExited:
		return fmt.Sprintf("%s exited before the lifecycle operation completed.", serviceLabel), "Check the service log and retry.", true
	case runtimeDiagnosticCodeHealthTimeout:
		return fmt.Sprintf("%s did not become healthy before the startup timeout.", serviceLabel), "Check the service log and retry startup.", true
	case runtimeDiagnosticCodeStopTimeout:
		return fmt.Sprintf("%s did not stop before the shutdown timeout.", serviceLabel), "Check the service log and retry shutdown.", true
	case runtimeDiagnosticCodeInstanceConflict:
		return "Another runtime instance owns the requested runtime.", "Stop the other runtime instance or switch profiles.", false
	default:
		return "The runtime lifecycle operation failed for an unclassified reason.", "Check the service log and retry the operation.", false
	}
}
