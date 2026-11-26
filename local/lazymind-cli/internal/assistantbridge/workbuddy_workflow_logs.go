package assistantbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"lazymind/agentconnector/internal/agentcatalog"
)

func readWorkBuddyWorkflowLogs(ctx context.Context, read workflowLogRead) []workflowHostLog {
	path, err := agentcatalog.WorkBuddyTranscriptPath(ctx, read.DriverID)
	if err != nil || path == "" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	return workBuddyTranscriptLogs(ctx, read, file)
}

func workBuddyTranscriptLogs(ctx context.Context, read workflowLogRead, reader io.Reader) []workflowHostLog {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	calls := make(map[string]workflowHostLog)
	entries := make([]workflowHostLog, 0)
	anchored := false
	for scanner.Scan() {
		if ctx.Err() != nil {
			return nil
		}
		var row struct {
			Type      string          `json:"type"`
			Role      string          `json:"role"`
			SessionID string          `json:"sessionId"`
			CallID    string          `json:"callId"`
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Timestamp int64           `json:"timestamp"`
			Status    string          `json:"status"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil || row.SessionID != read.DriverID ||
			row.Timestamp <= 0 || row.Timestamp > read.ResultsCutoffMS {
			continue
		}
		if row.Type == "message" && row.Role == "user" && !anchored && row.Timestamp <= read.StartedMS {
			// Include setup tools in the Workflow's user turn.
			entries = entries[:0]
			clear(calls)
		}
		if row.CallID == "" {
			continue
		}
		entry := workflowHostLog{Time: time.UnixMilli(row.Timestamp).UTC()}
		switch row.Type {
		case "function_call":
			if row.Timestamp > read.CutoffMS {
				continue
			}
			entry.Event, entry.Status, entry.Tool = "tool.call", "running", row.Name
			if row.Name == "DeferExecuteTool" {
				var args struct {
					ToolName string `json:"toolName"`
				}
				var encoded string
				if json.Unmarshal(row.Arguments, &encoded) == nil && encoded != "" {
					row.Arguments = json.RawMessage(encoded)
				}
				_ = json.Unmarshal(row.Arguments, &args)
				entry.Tool = args.ToolName
			}
			anchored = anchored || entry.Tool == "mcp__lazymind__workflow.start"
			calls[row.CallID] = entry
		case "function_call_result":
			call, ok := calls[row.CallID]
			if !ok {
				continue
			}
			duration := row.Timestamp - call.Time.UnixMilli()
			if duration >= 0 {
				entry.DurationMS = &duration
			}
			entry.Event, entry.Status, entry.Tool = "tool.result", row.Status, call.Tool
			switch row.Status {
			case "error":
				entry.Status = "failed"
			case "canceled", "cancelled":
				entry.Status = "interrupted"
			}
		default:
			continue
		}
		entries = append(entries, entry)
	}
	if scanner.Err() != nil {
		return nil
	}
	if !anchored {
		return []workflowHostLog{}
	}
	return entries
}
