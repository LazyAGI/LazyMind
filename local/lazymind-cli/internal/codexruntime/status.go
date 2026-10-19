package codexruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Configuration and a live desktop transport are separate observations. Never
// start a backend while polling the integration settings page.
func ConnectionState(registration *Registration) string {
	if registration == nil {
		return "unconfigured"
	}
	if registration.Installation == "" {
		return "waiting_restart"
	}
	c, err := readRuntime(registration.Installation)
	if err != nil || !runtimeFilesValid(c) {
		return "invalidated"
	}
	if _, err := os.Stat(filepath.Join(c.RuntimeDir, "startup-invalid.json")); err == nil {
		return "invalidated"
	}
	if startupRestoring(registration.Installation) {
		return "invalidated"
	}
	if c.Endpoint != registration.Endpoint || !SameProfile(c.CodexHome, registration.Profile) {
		return "invalidated"
	}
	var backend backendRecord
	file := filepath.Join(c.RuntimeDir, "backend.json")
	data, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		if _, err := os.Stat(filepath.Join(c.RuntimeDir, "adapter.jsonl")); err == nil {
			return "failed"
		}
		return "waiting_restart"
	}
	if err != nil || checkPrivate(file) != nil || json.Unmarshal(data, &backend) != nil || backend.Endpoint != c.Endpoint || !sameProcess(backend.Process) || verifyListener(c, &backend) != nil {
		return "failed"
	}
	if desktopConnected(c, &backend, sameProcess) {
		return "connected"
	}
	return "waiting_restart"
}

func desktopConnected(c *runtimeConfig, backend *backendRecord, alive func(*ProcessInfo) bool) bool {
	files, _ := filepath.Glob(filepath.Join(c.RuntimeDir, "desktop-*.json"))
	for _, file := range files {
		var connection struct {
			Process *ProcessInfo `json:"process"`
			Desktop *ProcessInfo `json:"desktop"`
			Backend *ProcessInfo `json:"backend"`
		}
		data, err := os.ReadFile(file)
		if err == nil && checkPrivate(file) == nil && json.Unmarshal(data, &connection) == nil &&
			connection.Backend != nil && connection.Backend.Identity == backend.Process.Identity &&
			connection.Desktop != nil && connection.Desktop.Executable == c.DesktopExecutable &&
			alive(connection.Process) && alive(connection.Desktop) {
			return true
		}
	}
	return false
}

func startupRestoring(file string) bool {
	data, err := os.ReadFile(filepath.Join(filepath.Dir(file), "activation.json"))
	var state struct {
		Disabling bool `json:"disabling"`
	}
	return err == nil && json.Unmarshal(data, &state) == nil && state.Disabling
}
