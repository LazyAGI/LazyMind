package mcpbridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"

	"lazymind/agentconnector/internal/credentials"
	"lazymind/agentconnector/internal/workflowhost"
)

// Binding occurs within workflow.start, using the same paired connector as
// the queue dispatcher. Revalidate the account and pairing on every bind.
func (b *Bridge) bindCodexController(ctx context.Context, runID, threadID string) error {
	path := os.Getenv("LAZYMIND_WORKFLOW_PAIRING_FILE")
	if path == "" {
		return errors.New("set LAZYMIND_WORKFLOW_PAIRING_FILE to the queue dispatcher's pairing file")
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	// Windows reports synthetic POSIX mode bits; access is governed by the
	// profile's ACL, as with the credential store, not chmod permissions.
	if !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return errors.New("Codex pairing must be a private file (0600)")
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var pair workflowhost.Pairing
	if json.Unmarshal(body, &pair) != nil || pair.Provider != "codex" {
		return errors.New("invalid Codex pairing file")
	}
	store, err := credentials.NewStore(b.home, "")
	if err != nil {
		return err
	}
	account, err := store.AccountScope()
	if err != nil {
		return err
	}
	pair, err = workflowhost.Verify(b.home, pair.ConnectorID, pair.Token, account)
	if err != nil {
		return err
	}
	profile := os.Getenv("CODEX_HOME")
	if profile == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		profile = filepath.Join(home, ".codex")
	}
	profile, err = filepath.Abs(profile)
	if err != nil {
		return err
	}
	if pair.Provider != "codex" || pair.Profile != profile {
		return errors.New("pairing does not match this Codex profile")
	}
	var result map[string]any
	return b.api.DoJSON(ctx, http.MethodPost, "/workflow-sessions/"+url.PathEscape(runID)+"/host-binding", map[string]any{
		"connector_id": pair.ConnectorID, "credential": pair.Token, "provider": pair.Provider,
		"driver_session_id": threadID,
	}, &result)
}
