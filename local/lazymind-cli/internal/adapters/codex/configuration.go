package codex

import (
	"errors"
	"os"
	"path/filepath"

	"lazymind/agentconnector/internal/agentexec"
	"lazymind/agentconnector/internal/agentintegration"
	"lazymind/agentconnector/internal/codexplugin"
	"lazymind/agentconnector/internal/connectorconfig"
)

func (a *Adapter) inspectConfiguration(status *agentintegration.Status) []agentintegration.Inspection {
	profile, err := codexHome()
	item := agentintegration.Inspection{Path: filepath.Join(profile, "config.toml")}
	config, exists, readErr := a.getConfig()
	unreadable := err != nil || readErr != nil
	mismatch := false
	if exists {
		item.Command = config.Transport.Command
		item.Reference = config.Transport.Env[connectorconfig.EnvironmentKey]
		mismatch = !a.isOwned(config)
	}
	record, recordErr := codexplugin.Read()
	if recordErr == nil {
		installed, nativeEnabled, e := codexplugin.Registration(profile, record.Marketplace)
		if e != nil {
			unreadable = true
		}
		if installed {
			if !nativeEnabled && status.State != agentintegration.Failed {
				status.State = agentintegration.Enabled
				status.Connection = &agentintegration.ConnectionStatus{MCPConfigured: true, MCPTrusted: true, MCPEnabled: false, PresentationStatus: "unavailable"}
				status.Issue = &agentintegration.Issue{Code: "mcp_enable_required"}
				status.Message = "Enable the installed plugin in Codex."
			}
			exists = true
			item.Command = record.Self
			item.Reference = connectorconfig.Path(record.Home, "codex", record.Profile)
			mismatch = mismatch || record.Home != a.home || record.Profile != profile || !agentexec.SameExecutable(record.Self, a.self)
		}
	} else if !errors.Is(recordErr, os.ErrNotExist) {
		unreadable = true
	}
	status.Enabled = &exists
	status.CanDisconnect = exists
	status.CanConnect = !unreadable && !agentintegration.MissingRequirement(status.Requirements)
	if unreadable {
		if !exists {
			status.Enabled = nil
		}
		item.Reason = "configuration_unreadable"
		status.State = agentintegration.Failed
		status.Issue = &agentintegration.Issue{Code: "configuration_unreadable"}
		status.Message = "Unable to read integration configuration."
	} else if mismatch && (status.State != agentintegration.Failed || status.RetryAction == "") {
		item.Reason = "installation_mismatch"
		status.State = agentintegration.Conflict
		status.Issue = &agentintegration.Issue{Code: "environment_mismatch"}
		status.Message = "Existing configuration points to another LazyMind environment."
	}
	return []agentintegration.Inspection{item}
}
