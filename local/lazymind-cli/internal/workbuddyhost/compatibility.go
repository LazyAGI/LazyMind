package workbuddyhost

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"lazymind/agentconnector/internal/agentexec"
)

const MinimumVersion = "5.8.3"

type CompatibilityError struct{ Code string }

func (e *CompatibilityError) Error() string { return "WorkBuddy: " + e.Code }

var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

// Compare with a stable minimum. A prerelease of the minimum is still too old.
func VersionIssue(version string) string {
	m := versionPattern.FindStringSubmatch(version)
	if m == nil {
		return "version_unreadable"
	}
	for _, part := range strings.Split(m[4], ".") {
		if len(part) > 1 && part[0] == '0' && strings.Trim(part, "0123456789") == "" {
			return "version_unreadable"
		}
	}
	values := [3]uint64{}
	for i := range values {
		v, err := strconv.ParseUint(m[i+1], 10, 53)
		if err != nil {
			return "version_unreadable"
		}
		values[i] = v
	}
	for i, minimum := range [3]uint64{5, 8, 3} {
		if values[i] < minimum {
			return "unsupported_version"
		}
		if values[i] > minimum {
			return ""
		}
	}
	if m[4] != "" {
		return "unsupported_version"
	}
	return ""
}

func desktopApplication() (string, error) {
	return agentexec.FindDesktopApplication(agentexec.DesktopApplication{
		BindingTarget: agentexec.WorkBuddyDesktop, DisplayNames: []string{"WorkBuddy", "WorkBuddy CN"},
		ExecutableNames: []string{"WorkBuddy.exe", "WorkBuddy CN.exe"},
	})
}

// Inspect the desktop package, never the bundled CLI (which has its own version).
// Both macOS and Windows ship the Electron manifest in resources/app.asar.
func Preflight() error {
	application, err := desktopApplication()
	if err != nil {
		return fmt.Errorf("%w: %v", &CompatibilityError{Code: "host_unavailable"}, err)
	}
	version, err := agentexec.ElectronVersion(application)
	if err != nil {
		return fmt.Errorf("%w: %v", &CompatibilityError{Code: "version_unreadable"}, err)
	}
	if issue := VersionIssue(version); issue != "" {
		return fmt.Errorf("%w: desktop version %s", &CompatibilityError{Code: issue}, version)
	}
	return nil
}

func CurrentVersion() string {
	application, err := desktopApplication()
	if err != nil {
		return ""
	}
	version, _ := agentexec.ElectronVersion(application)
	if VersionIssue(version) == "version_unreadable" {
		return ""
	}
	return version
}

// ControlIssue reports the native capability independently of Bridge health.
func ControlIssue(status Status) string {
	switch status.Issue {
	case "unsupported_version", "version_unreadable", "capability_unavailable", "host_unavailable":
		return status.Issue
	case "lazymind_unavailable", "lazymind_auth_required", "pairing_required", "pairing_disabled",
		"pairing_account_mismatch", "pairing_credential_mismatch", "pairing_invalid", "pairing_unreadable":
		// Legacy writers only set a Bridge issue after the native probe passed.
	case "":
		if !status.Ready {
			return "host_unavailable"
		}
	default:
		return "host_unavailable"
	}
	if issue := VersionIssue(status.Version); issue != "" {
		return issue
	}
	// Managed connectors advertise "none" and report their build identity.
	// Retain support for the legacy session-capable heartbeat.
	if status.Cancellation == "session" || status.Cancellation == "none" && status.Ready && status.Runtime.HasBuildIdentity() {
		return ""
	}
	return "capability_unavailable"
}
