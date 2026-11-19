package codexhost

import (
	"errors"
	"strings"

	"lazymind/agentconnector/internal/desktopsetup"
)

// SetupError carries a stable category across the CLI and Desktop boundary.
// Raw subprocess output is used only to identify our own error markers.
type SetupError struct {
	Code string
	Err  error
}

func (e *SetupError) Error() string { return e.Code + ": " + e.Err.Error() }
func (e *SetupError) Unwrap() error { return e.Err }

func setupError(code string, err error) error {
	if code == "quit_required" && !errors.Is(err, desktopsetup.ErrQuitRequired) {
		err = errors.Join(desktopsetup.ErrQuitRequired, err)
	}
	return &SetupError{Code: code, Err: err}
}

func desktopLaunchError(output string, err error) error {
	for _, code := range []string{"quit_required", "process_inspection_failed", "launch_failed"} {
		if strings.Contains(output, "LAZYMIND_AGENT_"+code+":") {
			return setupError(code, err)
		}
	}
	return setupError("launch_failed", err)
}

func SetupIssue(err error) string {
	var setup *SetupError
	if errors.As(err, &setup) {
		return setup.Code
	}
	if errors.Is(err, desktopsetup.ErrQuitRequired) {
		return "quit_required"
	}
	return ""
}
