package mcpbridge

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"lazymind/agentconnector/internal/agentcatalog"
	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/workflowhost"
)

var errInvalidDSHSource = errors.New("invalid paired DSH conversation source")

func dshInvocationSource(recorder invocationRecorder, tool string, arguments *json.RawMessage) (*coreapi.InvocationSource, error) {
	var args map[string]json.RawMessage
	if json.Unmarshal(*arguments, &args) != nil {
		return nil, nil
	}
	raw, present := args["__lazymind_dsh_source"]
	if !present {
		return nil, nil
	}
	delete(args, "__lazymind_dsh_source")
	clean, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	*arguments = clean
	account, ok := recorder.(interface{ AccountScope() (string, error) })
	if !ok {
		return nil, errInvalidDSHSource
	}
	scope, err := account.AccountScope()
	if err != nil {
		return nil, err
	}
	cfg, err := connectorconfig.Current()
	if err != nil || cfg == nil || !cfg.Controlled || cfg.Provider != workflowhost.DSHProvider {
		return nil, errInvalidDSHSource
	}
	id := strings.TrimSuffix(filepath.Base(cfg.PairingFile), ".json")
	pair, err := workflowhost.Load(cfg.Home, id, scope)
	if err != nil || pair.Provider != workflowhost.DSHProvider || pair.Profile != cfg.Profile {
		return nil, errInvalidDSHSource
	}
	return verifyDSHSource(raw, pair, cfg.HostID, tool, time.Now())
}

func verifyDSHSource(raw json.RawMessage, pair workflowhost.Pairing, host, tool string, now time.Time) (*coreapi.InvocationSource, error) {
	var signed struct {
		Body string
		MAC  string
	}
	if json.Unmarshal(raw, &signed) != nil || len(signed.Body) > 64<<10 {
		return nil, errInvalidDSHSource
	}
	supplied, err := hex.DecodeString(signed.MAC)
	mac := hmac.New(sha256.New, []byte(pair.Token))
	mac.Write([]byte(signed.Body))
	if err != nil || !hmac.Equal(mac.Sum(nil), supplied) {
		return nil, errInvalidDSHSource
	}
	var entry struct {
		Thread             string `json:"thread_id"`
		Turn               string `json:"turn_id"`
		Issued             int64  `json:"issued_at"`
		Managed            bool   `json:"managed"`
		Message, CWD, Tool string
	}
	if json.Unmarshal([]byte(signed.Body), &entry) != nil || entry.Thread == "" || entry.Turn == "" || entry.Tool != dshPublicToolName(tool) || now.Sub(time.UnixMilli(entry.Issued)).Abs() > time.Minute {
		return nil, errInvalidDSHSource
	}
	project, name := agentcatalog.ProjectPath(workflowhost.DSHProvider, entry.CWD, "")
	if entry.Managed {
		host = pair.ConnectorID
	}
	return &coreapi.InvocationSource{Provider: workflowhost.DSHProvider, HostID: host, ThreadID: entry.Thread, TurnID: entry.Turn, ThreadSource: "user", ProjectKey: project, ProjectName: name, Message: entry.Message}, nil
}

// DSH disambiguates normalized MCP names with a hash of their original spelling.
func dshPublicToolName(raw string) string {
	name := "mcp__lazymind__" + raw
	normalized := strings.Map(func(c rune) rune {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			return c
		}
		return '_'
	}, name)
	if normalized == name && len(name) <= 64 {
		return name
	}
	sum := sha256.Sum256([]byte("lazymind\x00" + raw))
	if len(normalized) > 51 {
		normalized = normalized[:51]
	}
	return normalized + "_" + hex.EncodeToString(sum[:6])
}
