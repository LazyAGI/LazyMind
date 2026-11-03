// Package integrationstate stores user intent separately from installation,
// authentication and runtime health. Only explicit connect/disconnect writes it.
package integrationstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"lazymind/agentconnector/internal/localfile"
)

func path(home, provider string) string {
	return filepath.Join(home, provider+"-workflow", "enabled.json")
}

func Read(home, provider string) (*bool, error) {
	body, err := os.ReadFile(path(home, provider))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var enabled *bool
	if json.Unmarshal(body, &enabled) != nil || enabled == nil {
		return nil, fmt.Errorf("invalid %s enable preference", provider)
	}
	return enabled, nil
}

func Set(home, provider string, enabled bool) error {
	body, _ := json.Marshal(enabled)
	return localfile.Write(path(home, provider), body, 0600)
}

// A missing preference is a legacy installation. Pairing validation remains
// mandatory even when the user has requested enablement.
func Allowed(home, provider string) bool {
	enabled, err := Read(home, provider)
	return err == nil && (enabled == nil || *enabled)
}
