//go:build !windows

package agentexec

func platformDesktopInstalled(_ DesktopApplication, initialized bool) bool {
	return initialized
}
