package codexcontrol

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrThreadBusy = errors.New("the selected Codex thread is currently executing; wait for it to finish or interrupt it in Codex")

type SessionEvent struct {
	Type     string
	Text     string
	ThreadID string
}

type Thread struct {
	ID        string
	Name      string
	Preview   string
	CWD       string
	UpdatedAt int64
	Status    string
}

func (c *Controller) Execute(
	ctx context.Context, threadID, workspace, clientMessageID, prompt, applicationContext string,
	emit func(SessionEvent) error,
) error {
	if strings.TrimSpace(prompt) == "" {
		return errors.New("Codex prompt is required")
	}
	notifications, unsubscribe := c.Subscribe()
	defer unsubscribe()
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		result, err := c.Request(ctx, "thread/start", map[string]any{
			"cwd": strings.TrimSpace(workspace), "approvalPolicy": "never", "sandbox": "workspace-write",
		})
		if err != nil {
			return err
		}
		threadID = resultThreadID(result)
		if threadID == "" {
			return errors.New("Codex thread/start returned no thread ID")
		}
		if emit != nil {
			if err := emit(SessionEvent{Type: "thread_started", ThreadID: threadID}); err != nil {
				return err
			}
		}
	} else {
		result, err := c.Request(ctx, "thread/resume", map[string]any{
			"threadId": threadID, "excludeTurns": true,
		})
		if err != nil {
			result, err = c.Request(ctx, "thread/resume", map[string]any{"threadId": threadID})
		}
		if err != nil {
			return err
		}
		if status := resultThreadStatus(result); status == "active" {
			return ErrThreadBusy
		}
	}
	params := map[string]any{
		"threadId":       threadID,
		"input":          []map[string]string{{"type": "text", "text": prompt}},
		"approvalPolicy": "never",
		"sandboxPolicy":  map[string]string{"type": "workspaceWrite"},
	}
	if clientMessageID = strings.TrimSpace(clientMessageID); clientMessageID != "" {
		params["clientUserMessageId"] = clientMessageID
	}
	if applicationContext = strings.TrimSpace(applicationContext); applicationContext != "" {
		params["additionalContext"] = map[string]any{
			"lazymind": map[string]string{"value": applicationContext, "kind": "application"},
		}
	}
	turnResult, err := c.Request(ctx, "turn/start", params)
	if err != nil {
		return err
	}
	turnID := resultTurnID(turnResult)
	if turnID == "" {
		return errors.New("Codex turn/start returned no turn ID")
	}
	return c.waitForTurn(ctx, notifications, threadID, turnID, emit)
}

func (c *Controller) waitForTurn(
	ctx context.Context,
	notifications <-chan Notification,
	threadID, turnID string,
	emit func(SessionEvent) error,
) error {
	var delta strings.Builder
	finalMessage := ""
	for {
		select {
		case <-ctx.Done():
			interruptCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = c.Request(interruptCtx, "turn/interrupt", map[string]any{"threadId": threadID, "turnId": turnID})
			cancel()
			return ctx.Err()
		case notification, ok := <-notifications:
			if !ok {
				return ErrUnavailable
			}
			if !notificationFor(notification.Params, threadID, turnID) {
				continue
			}
			switch notification.Method {
			case "item/agentMessage/delta":
				var params struct {
					Delta string `json:"delta"`
				}
				if json.Unmarshal(notification.Params, &params) == nil {
					delta.WriteString(params.Delta)
				}
			case "item/completed":
				var params struct {
					Item struct {
						Type string `json:"type"`
						Text string `json:"text"`
					} `json:"item"`
				}
				if json.Unmarshal(notification.Params, &params) == nil && params.Item.Type == "agentMessage" {
					finalMessage = params.Item.Text
				}
			case "turn/completed":
				status, failure := completedTurnStatus(notification.Params)
				if status != "completed" {
					if failure == "" {
						failure = "Codex turn ended with status " + status
					}
					return errors.New(failure)
				}
				message := strings.TrimSpace(finalMessage)
				if message == "" {
					message = strings.TrimSpace(delta.String())
				}
				if message == "" {
					return errors.New("Codex completed without an assistant response")
				}
				if emit != nil {
					if err := emit(SessionEvent{Type: "message", Text: message, ThreadID: threadID}); err != nil {
						return err
					}
					return emit(SessionEvent{Type: "turn_completed", ThreadID: threadID})
				}
				return nil
			}
		}
	}
}

func (c *Controller) ListThreads(ctx context.Context) ([]Thread, error) {
	threads := make([]Thread, 0)
	cursor := ""
	for {
		params := map[string]any{
			"limit": 100, "sortKey": "updated_at", "sortDirection": "desc",
			"archived": false, "sourceKinds": []string{"cli", "vscode", "appServer"},
		}
		if cursor != "" {
			params["cursor"] = cursor
		}
		result, err := c.Request(ctx, "thread/list", params)
		if err != nil {
			return nil, err
		}
		var page struct {
			Data []struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				Preview   string `json:"preview"`
				CWD       string `json:"cwd"`
				UpdatedAt int64  `json:"updatedAt"`
				Status    struct {
					Type string `json:"type"`
				} `json:"status"`
			} `json:"data"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(result, &page); err != nil {
			return nil, err
		}
		for _, item := range page.Data {
			if strings.TrimSpace(item.ID) != "" {
				threads = append(threads, Thread{
					ID: item.ID, Name: item.Name, Preview: item.Preview, CWD: item.CWD,
					UpdatedAt: item.UpdatedAt, Status: item.Status.Type,
				})
			}
		}
		cursor = strings.TrimSpace(page.NextCursor)
		if cursor == "" {
			return threads, nil
		}
	}
}

func notificationFor(params json.RawMessage, threadID, turnID string) bool {
	var identity struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Turn     struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"turn"`
	}
	if json.Unmarshal(params, &identity) != nil {
		return false
	}
	actualThread := identity.ThreadID
	if actualThread == "" {
		actualThread = identity.Turn.ThreadID
	}
	actualTurn := identity.TurnID
	if actualTurn == "" {
		actualTurn = identity.Turn.ID
	}
	return (actualThread == "" || actualThread == threadID) && (actualTurn == "" || actualTurn == turnID)
}

func completedTurnStatus(params json.RawMessage) (string, string) {
	var value struct {
		Turn struct {
			Status string          `json:"status"`
			Error  json.RawMessage `json:"error"`
		} `json:"turn"`
	}
	if json.Unmarshal(params, &value) != nil {
		return "unknown", "invalid Codex turn completion"
	}
	failure := ""
	if len(value.Turn.Error) > 0 && string(value.Turn.Error) != "null" {
		var typed struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(value.Turn.Error, &typed) == nil {
			failure = typed.Message
		}
		if failure == "" {
			failure = string(value.Turn.Error)
		}
	}
	return value.Turn.Status, failure
}

func resultThreadID(result json.RawMessage) string {
	var value struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	_ = json.Unmarshal(result, &value)
	return strings.TrimSpace(value.Thread.ID)
}

func resultThreadStatus(result json.RawMessage) string {
	var value struct {
		Thread struct {
			Status struct {
				Type string `json:"type"`
			} `json:"status"`
		} `json:"thread"`
	}
	_ = json.Unmarshal(result, &value)
	return strings.TrimSpace(value.Thread.Status.Type)
}

func resultTurnID(result json.RawMessage) string {
	var value struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	_ = json.Unmarshal(result, &value)
	return strings.TrimSpace(value.Turn.ID)
}
