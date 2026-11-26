package assistantbridge

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWorkBuddyWorkflowLogsPreserveNativeTimesAndLateReceipts(t *testing.T) {
	read := workflowLogRead{DriverID: "driver", StartedMS: 100, CutoffMS: 200, ResultsCutoffMS: 300}
	source := strings.NewReader(`
{"type":"function_call","sessionId":"driver","callId":"prior-turn","timestamp":70,"name":"prior-turn"}
{"type":"message","role":"user","sessionId":"driver","timestamp":80,"content":"PRIVATE_PROMPT"}
{"type":"function_call","sessionId":"driver","callId":"start","timestamp":90,"name":"DeferExecuteTool","arguments":"{\"toolName\":\"mcp__lazymind__workflow.start\",\"params\":\"PRIVATE_ARGUMENT\"}"}
{"type":"function_call_result","sessionId":"driver","callId":"start","timestamp":101,"status":"completed","output":"PRIVATE_RESULT"}
{"type":"function_call","sessionId":"other","callId":"other","timestamp":190,"name":"other-session"}
{"type":"function_call","sessionId":"driver","callId":"last","timestamp":190,"name":"finish"}
{"type":"function_call","sessionId":"driver","callId":"late","timestamp":201,"name":"outside-workflow"}
{"type":"function_call_result","sessionId":"driver","callId":"last","timestamp":210,"status":"completed"}
{"type":"function_call_result","sessionId":"driver","callId":"late","timestamp":218,"status":"completed"}
{"type":"function_call_result","sessionId":"driver","callId":"last","timestamp":301,"status":"completed"}
`)
	rows := filterWorkflowHostLogs(read, workBuddyTranscriptLogs(t.Context(), read, source))
	if len(rows) != 4 || rows[0].Tool != "mcp__lazymind__workflow.start" || rows[0].Time.UnixMilli() != 90 ||
		rows[3].Event != "tool.result" || rows[3].Tool != "finish" || rows[3].Time.UnixMilli() != 210 ||
		rows[3].DurationMS == nil || *rows[3].DurationMS != 20 {
		t.Fatalf("unexpected WorkBuddy timing: %+v", rows)
	}
	encoded, err := json.Marshal(rows)
	if err != nil || strings.Contains(string(encoded), "PRIVATE_") {
		t.Fatal("expected only tool metadata")
	}
}
