//go:build !windows && !darwin

package agentexec

import "errors"

func OpenInteractiveCommand(string) error {
	return errors.New("opening an interactive login terminal is supported on macOS and Windows")
}
