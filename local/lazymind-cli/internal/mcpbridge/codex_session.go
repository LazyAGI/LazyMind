package mcpbridge

import (
	"bufio"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"lazymind/agentconnector/internal/coreapi"
)

var codexRolloutPaths sync.Map
var codexTurnSyncs sync.Map

type codexTurnSyncer interface {
	SyncExternalTurn(context.Context, coreapi.ExternalTurnSync) error
}

func codexTurnUserMessage(threadID, turnID string) string {
	message, _, _ := codexTurnSnapshot(threadID, turnID)
	return message
}

func codexTurnSnapshot(threadID, turnID string) (userMessage, assistantMessage string, completed bool) {
	if !validIdentifier(threadID) || !validIdentifier(turnID) {
		return "", "", false
	}
	path := codexRolloutPath(threadID)
	if path == "" {
		return "", "", false
	}
	file, err := os.Open(path)
	if err != nil {
		return "", "", false
	}
	defer file.Close()

	currentTurn := ""
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 16<<20)
	for scanner.Scan() {
		var row struct {
			Type    string `json:"type"`
			Payload struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				TurnID  string `json:"turn_id"`
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"payload"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil {
			continue
		}
		if row.Type == "event_msg" && row.Payload.Type == "task_started" {
			currentTurn = strings.TrimSpace(row.Payload.TurnID)
			continue
		}
		if row.Type == "event_msg" && row.Payload.Type == "task_complete" && strings.TrimSpace(row.Payload.TurnID) == turnID {
			completed = true
			continue
		}
		if currentTurn != turnID || row.Type != "response_item" || row.Payload.Type != "message" {
			continue
		}
		for _, content := range row.Payload.Content {
			text := strings.TrimSpace(content.Text)
			if row.Payload.Role == "user" && content.Type == "input_text" && text != "" && !codexInjectedContext(text) {
				userMessage = text
			}
			if row.Payload.Role == "assistant" && (content.Type == "output_text" || content.Type == "input_text") && text != "" {
				assistantMessage = text
			}
		}
	}
	return userMessage, assistantMessage, completed
}

func scheduleCodexTurnSync(recorder invocationRecorder, source *coreapi.InvocationSource) {
	if source == nil || source.Provider != "codex" || source.ThreadID == "" || source.TurnID == "" {
		return
	}
	syncer, ok := recorder.(codexTurnSyncer)
	if !ok {
		return
	}
	key := source.ThreadID + "\x00" + source.TurnID
	if _, loaded := codexTurnSyncs.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	copySource := *source
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			userMessage, answer, completed := codexTurnSnapshot(copySource.ThreadID, copySource.TurnID)
			if completed && answer != "" {
				if userMessage != "" {
					copySource.Message = userMessage
				}
				if syncer.SyncExternalTurn(ctx, coreapi.ExternalTurnSync{Source: copySource, Answer: answer}) == nil {
					return
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func codexRolloutPath(threadID string) string {
	if cached, ok := codexRolloutPaths.Load(threadID); ok {
		return cached.(string)
	}
	home := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if home == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(userHome, ".codex")
		}
	}
	for _, root := range []string{filepath.Join(home, "sessions"), filepath.Join(home, "archived_sessions")} {
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return nil
			}
			if strings.HasSuffix(entry.Name(), threadID+".jsonl") {
				codexRolloutPaths.Store(threadID, path)
				return fs.SkipAll
			}
			return nil
		})
		if cached, ok := codexRolloutPaths.Load(threadID); ok {
			return cached.(string)
		}
	}
	return ""
}

func codexInjectedContext(text string) bool {
	return strings.HasPrefix(text, "<recommended_plugins>") ||
		strings.HasPrefix(text, "# AGENTS.md instructions") ||
		strings.HasPrefix(text, "<environment_context>")
}
