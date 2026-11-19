package workbuddyhost

import (
	"errors"
	"strings"
)

// SetupError carries an actionable category across the CLI and Desktop boundary.
// The wrapped error is retained for local debugging, never used as UI copy.
type SetupError struct {
	Code string
	Err  error
}

func (e *SetupError) Error() string { return e.Code + ": " + e.Err.Error() }
func (e *SetupError) Unwrap() error { return e.Err }

func setupError(code string, err error) error { return &SetupError{Code: code, Err: err} }

func desktopLaunchError(output string, err error) error {
	// These markers are emitted by our own PowerShell, not localized OS errors.
	for _, code := range []string{"quit_required", "process_inspection_failed", "launch_failed"} {
		if strings.Contains(output, "LAZYMIND_WB_"+code) {
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
	var compatibility *CompatibilityError
	if errors.As(err, &compatibility) {
		return compatibility.Code
	}
	return ""
}
