package mcpbridge

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/agentconnector/internal/connectorconfig"
	"lazymind/agentconnector/internal/workflowhost"
)

type dshRecorder struct {
	recordingInvocationRecorder
	account string
}

func (r *dshRecorder) AccountScope() (string, error) { return r.account, nil }

func TestDSHSourceBoundToPairingAccountAndNativeThread(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, "desktop")
	pair, err := workflowhost.Ensure(home, profile, "account-A")
	if err != nil {
		t.Fatal(err)
	}
	record, err := connectorconfig.Save(connectorconfig.Config{Version: 1, Home: home, HostID: "host-fixture", Provider: workflowhost.DSHProvider, Profile: profile, Controlled: true, PairingFile: filepath.Join(home, "workflow-hosts", pair.ConnectorID+".json")})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(connectorconfig.EnvironmentKey, record)
	const publicName = "mcp__lazymind__workflow_list_6fc624ade2b2"
	build := func(key string, when time.Time, managed bool) json.RawMessage {
		body, _ := json.Marshal(map[string]any{"thread_id": "concurrent-thread-B", "turn_id": "human-2", "issued_at": when.UnixMilli(), "message": "second prompt", "cwd": "/work/project", "tool": publicName, "managed": managed})
		signed := hmac.New(sha256.New, []byte(key))
		signed.Write(body)
		args, _ := json.Marshal(map[string]any{"limit": 1, "__lazymind_dsh_source": map[string]string{"body": string(body), "mac": hex.EncodeToString(signed.Sum(nil))}})
		return args
	}
	recorder := &dshRecorder{account: "account-A"}
	args := build(pair.Token, time.Now(), false)
	got, err := dshInvocationSource(recorder, "workflow.list", &args)
	if err != nil || got == nil || got.ThreadID != "concurrent-thread-B" || got.TurnID != "human-2" || got.ProjectName != "project" || got.HostID != "host-fixture" {
		t.Fatalf("source=%+v err=%v", got, err)
	}
	if string(args) != `{"limit":1}` {
		t.Fatalf("unstripped invocation fields: %s", args)
	}
	args = build(pair.Token, time.Now(), true)
	got, err = dshInvocationSource(recorder, "workflow.list", &args)
	if err != nil || got == nil || got.HostID != pair.ConnectorID {
		t.Fatalf("managed thread did not retain its execution host: %+v, %v", got, err)
	}
	for _, tc := range []struct {
		key, account, tool string
		age                time.Duration
	}{
		{"forged", "account-A", "workflow.list", 0}, {pair.Token, "account-B", "workflow.list", 0}, {pair.Token, "account-A", "skill.list", 0}, {pair.Token, "account-A", "workflow.list", 2 * time.Minute},
	} {
		args = build(tc.key, time.Now().Add(-tc.age), true)
		recorder.account = tc.account
		if _, err := dshInvocationSource(recorder, tc.tool, &args); err == nil || strings.Contains(string(args), "source") {
			t.Errorf("wrong account, signature, freshness or tool not rejected")
		}
	}
}
