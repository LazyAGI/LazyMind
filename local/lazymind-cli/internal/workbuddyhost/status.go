package workbuddyhost

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lazymind/agentconnector/internal/workflowconnector"
	"lazymind/agentconnector/internal/workflowhost"
)

// ReadLocalStatus observes native control without granting access to a pairing.
func ReadLocalStatus(home string) (Status, error) {
	config, err := readConfig(home)
	if err != nil {
		return Status{}, err
	}
	return ReadStatus(config, strings.TrimSuffix(filepath.Base(config.PairingFile), ".json"))
}

type Status struct {
	workflowconnector.Runtime
	workflowconnector.ConnectionEvidence
	Issue        string             `json:"issue,omitempty"`
	ConnectorID  string             `json:"connector_id"`
	CheckedAt    int64              `json:"checked_at"`
	Ready        bool               `json:"ready"`
	Version      string             `json:"version"`
	Cancellation string             `json:"cancellation"`
	BridgeURL    string             `json:"bridge_url"`
	Presentation PresentationStatus `json:"presentation"`
}

type PresentationStatus struct {
	Status     string `json:"status"`
	PageStatus string `json:"page_status,omitempty"`
}

func ReadStatus(config Config, connector string) (Status, error) {
	var status Status
	body, err := os.ReadFile(config.PairingFile + ".workbuddy-status.json")
	if err != nil {
		return status, err
	}
	if json.Unmarshal(body, &status) != nil || status.ConnectorID != connector {
		return Status{}, errors.New("invalid connector status")
	}
	age := time.Now().UnixMilli() - status.CheckedAt
	if age < 0 || age > 12000 {
		return Status{}, errors.New("WorkBuddy connector heartbeat expired")
	}
	if status.Cancellation == "none" {
		// Observing the active pairing does not grant permission to use it.
		pairing, err := os.ReadFile(config.PairingFile)
		var pair workflowhost.Pairing
		if err != nil || json.Unmarshal(pairing, &pair) != nil || !pair.Enabled || pair.Provider != "workbuddy" ||
			pair.ConnectorID != connector || pair.RuntimeRevision == "" || status.PairingRevision != pair.RuntimeRevision {
			return Status{}, errors.New("WorkBuddy status does not match the active pairing")
		}
	}
	u, err := url.Parse(status.BridgeURL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost" && u.Hostname() != "::1" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return Status{}, errors.New("invalid local identity bridge URL")
	}
	return status, nil
}
