//go:build !windows && !darwin

package agentexec

func platformDesktopInstalled(_ DesktopApplication, initialized bool) bool {
	return initialized
}
