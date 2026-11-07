package mcpclient

import (
	"path/filepath"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
)

// inspectConfiguration always reads the Agent's files, including foreign entries.
func (a *Adapter) inspectConfiguration(status *agentintegration.Status) []agentintegration.Inspection {
	paths := []string{configPath(a.kind)}
	if a.desktopDSHMode() {
		paths = nil
		paths = append(paths, filepath.Join(dshHome(), "cordis.patch.yml"))
		for _, profile := range dshDesktopProfiles {
			paths = append(paths, a.forDSHProfile(profile, "").dshConfigPath())
		}
	}
	configured, unreadable, mismatch := false, false, false
	details := make([]agentintegration.Inspection, 0, len(paths))
	for _, path := range paths {
		state, err := readManagedConfig(a.kind, path, a.self, a.home, a.hostID)
		item := agentintegration.Inspection{Path: path, Command: state.command, Reference: state.reference}
		if item.Reference == "" {
			item.Reference = state.workflowReference
		}

		if err != nil {
			unreadable = true
			item.Reason = agentintegration.ErrorReason(err)
		} else if state.configured || state.workflowConfigured {
			configured = true
			if state.configured && (!state.owned || !agentexec.SameExecutable(state.command, a.self)) {
				mismatch = true
				item.Reason = "connection_mismatch"
				if !state.owned {
					item.Reason = "installation_mismatch"
				}
			}
		}
		details = append(details, item)
	}
	status.Enabled = &configured
	if unreadable && !configured {
		status.Enabled = nil
	}
	status.CanDisconnect = configured
	if a.desktopDSHMode() {
		status.CanConnect = status.CanConnect && !unreadable
	} else {
		status.CanConnect = !unreadable && !agentintegration.MissingRequirement(status.Requirements)
	}
	if unreadable {
		status.State = agentintegration.Failed
		if status.Issue == nil {
			status.Issue = &agentintegration.Issue{Code: "configuration_unreadable"}
		}
		status.Message = "Unable to read integration configuration."
	} else if mismatch && (status.State != agentintegration.Failed || status.RetryAction == "") && status.State != agentintegration.ActionRequired {
		status.State = agentintegration.Conflict
		status.Issue = &agentintegration.Issue{Code: "environment_mismatch"}
		status.Message = "Existing configuration points to another LazyMind environment."
	}
	return details
}
