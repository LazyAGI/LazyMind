package codexhost

import (
	"context"
	"time"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/desktopsetup"
)

func desktopApplication() (string, error) {
	return agentexec.FindDesktopApplication(desktopApplicationSpec())
}

func desktopApplicationSpec() agentexec.DesktopApplication {
	return agentexec.DesktopApplication{BindingTarget: agentexec.CodexDesktop, PackageNames: []string{"OpenAI.Codex"}, DisplayNames: []string{"ChatGPT", "Codex"}, ExecutableNames: []string{"ChatGPT.exe", "Codex.exe"}, Protocols: []string{"codex"}}
}

var startupApplication = desktopApplication
var startupRunning = desktopsetup.Running
var startupListenerMatches = desktopsetup.ListenerMatches
var startupDesktopReady = DesktopReady

// This read-only status concerns the current application, not a managed shortcut.
// No persistent launch entry is created, modified, restored or required.
func StartupHealth(ctx context.Context, home string) string {
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, err := readConfig(home)
	if err != nil {
		return "startup_unavailable"
	}
	application, err := startupApplication()
	if err != nil {
		return "startup_unavailable"
	}
	running, err := startupRunning(bounded, application)
	if err != nil {
		return "host_unavailable"
	}
	if !running {
		return "waiting_for_app"
	}
	ready, err := startupDesktopReady(bounded, c.Port)
	if err != nil {
		return "cdp_unavailable"
	}
	if !ready {
		return "connection_required"
	}
	if !startupListenerMatches(bounded, application, c.Port) {
		return "cdp_unavailable"
	}
	return ""
}
