package agentcatalog

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"lazymind/agentconnector/internal/chatagent"
)

const (
	maxTranscriptLine = 16 << 20
	maxTurnRunes      = 1 << 20
)

var codexImagePathPattern = regexp.MustCompile(`(?i)<image\b[^>]*\bpath="([^"]+)"[^>]*>`)

type cachedSession struct {
	modTime time.Time
	size    int64
	session chatagent.NativeSession
}

var nativeSessionCache sync.Map

type InvocationSource struct {
	Provider    string
	ThreadID    string
	TurnID      string
	ProjectKey  string
	ProjectName string
	Message     string
}

func nativeSession(path, provider, projectReference, projectName string, fullTranscript bool) (chatagent.NativeSession, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return chatagent.NativeSession{}, false
	}
	cacheKey := provider + "\x00" + path + "\x00" + strconv.FormatBool(fullTranscript)
	if cached, ok := nativeSessionCache.Load(cacheKey); ok {
		entry := cached.(cachedSession)
		if entry.size == info.Size() && entry.modTime.Equal(info.ModTime()) {
			return entry.session, true
		}
	}

	meta, ok := sessionMetadata(path, provider)
	if !ok {
		return chatagent.NativeSession{}, false
	}
	if projectReference == "" {
		projectReference = meta.CWD
	}
	projectKey, displayProject := ProjectPath(provider, projectReference, projectName)
	if provider == "cursor" {
		projectKey, displayProject = Project(provider, projectReference, projectName)
	}
	if projectKey == "" {
		return chatagent.NativeSession{}, false
	}
	turns := []chatagent.NativeTurn(nil)
	if fullTranscript {
		turns = transcriptTurns(path, provider)
	}
	displayName := activityName(provider, meta.ID)
	for _, turn := range turns {
		if title := compactLabel(turn.User, 100); title != "" {
			displayName = title
			break
		}
	}
	if displayName == activityName(provider, meta.ID) {
		if title := transcriptTitle(path, provider); title != "" {
			displayName = title
		}
	}
	session := chatagent.NativeSession{
		ThreadID: meta.ID, ProjectKey: projectKey, ProjectName: displayProject,
		DisplayName: displayName, NativeUpdated: info.ModTime().UTC(),
		TurnCount: len(turns), Turns: turns,
	}
	nativeSessionCache.Store(cacheKey, cachedSession{modTime: info.ModTime(), size: info.Size(), session: session})
	return session, true
}

func ResolveInvocation(provider, toolName string, now time.Time) (InvocationSource, bool) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	toolName = strings.TrimSpace(toolName)
	if provider != "cursor" && provider != "workbuddy" || toolName == "" {
		return InvocationSource{}, false
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return InvocationSource{}, false
	}
	root := filepath.Join(home, ".codebuddy", "projects")
	cursorByID := map[string]cursorChat{}
	if provider == "cursor" {
		root = filepath.Join(home, ".cursor", "projects")
		chats, err := cursorChats(context.Background(), home)
		if err != nil {
			return InvocationSource{}, false
		}
		for _, chat := range chats {
			if chat.HasConversation && !isLazyMindWorkspace(chat.CWD) {
				cursorByID[chat.ID] = chat
			}
		}
	}
	cutoff := now.Add(-5 * time.Minute)
	bestPath := ""
	var bestInfo os.FileInfo
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(info.Name(), ".jsonl") || info.ModTime().Before(cutoff) {
			return nil
		}
		if provider == "cursor" && !strings.Contains(path, string(filepath.Separator)+"agent-transcripts"+string(filepath.Separator)) {
			return nil
		}
		if provider == "cursor" {
			threadID := filepath.Base(filepath.Dir(path))
			if _, resumable := cursorByID[threadID]; !resumable {
				return nil
			}
		}
		if !transcriptContainsTool(path, provider, toolName) {
			return nil
		}
		if bestInfo == nil || info.ModTime().After(bestInfo.ModTime()) {
			bestPath, bestInfo = path, info
		}
		return nil
	})
	if bestPath == "" {
		return InvocationSource{}, false
	}
	projectReference, projectName := "", ""
	if provider == "cursor" {
		threadID := filepath.Base(filepath.Dir(bestPath))
		if chat, found := cursorByID[threadID]; found {
			projectReference = chat.CWD
			projectName = filepath.Base(filepath.Clean(chat.CWD))
		}
	}
	session, ok := nativeSession(bestPath, provider, projectReference, projectName, true)
	if !ok || len(session.Turns) == 0 {
		return InvocationSource{}, false
	}
	turn := session.Turns[len(session.Turns)-1]
	return InvocationSource{
		Provider: provider, ThreadID: session.ThreadID, TurnID: turn.ID,
		ProjectKey: session.ProjectKey, ProjectName: session.ProjectName, Message: turn.User,
	}, true
}

// CodexTurnSource resolves the real user message associated with one Codex
// Desktop turn. Injected Codex context is filtered by the same transcript
// normalization used by the native session catalog.
func CodexTurnSource(threadID, turnID string) (string, string) {
	if !validSessionID(threadID) || !validSessionID(turnID) {
		return "", ""
	}
	path := CodexRolloutPath(threadID)
	file, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer file.Close()

	currentTurn, messageID, userMessage := "", "", ""
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), maxTranscriptLine)
	for scanner.Scan() {
		var row struct {
			Type    string `json:"type"`
			Payload struct {
				Type          string `json:"type"`
				Role          string `json:"role"`
				ID            string `json:"id"`
				ClientID      string `json:"clientId"`
				EventClientID string `json:"client_id"`
				TurnID        string `json:"turn_id"`
				Message       string `json:"message"`
				Content       any    `json:"content"`
			} `json:"payload"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil {
			continue
		}
		if row.Type == "event_msg" && row.Payload.Type == "task_started" {
			currentTurn = strings.TrimSpace(row.Payload.TurnID)
			continue
		}
		if currentTurn == turnID && row.Type == "event_msg" && row.Payload.Type == "user_message" {
			if message := cleanUserText(row.Payload.Message); message != "" {
				if clientID := strings.TrimSpace(row.Payload.EventClientID); clientID != "" {
					messageID = clientID
				}
				userMessage = message
			}
			continue
		}
		if currentTurn != turnID || row.Type != "response_item" ||
			row.Payload.Type != "message" || row.Payload.Role != "user" {
			continue
		}
		if message := cleanUserText(contentText(row.Payload.Content)); message != "" {
			messageID = strings.TrimSpace(row.Payload.ClientID)
			if messageID == "" {
				messageID = strings.TrimSpace(row.Payload.ID)
			}
			userMessage = message
		}
	}
	return messageID, userMessage
}

func Workspace(ctx context.Context, provider, threadID string) (string, bool, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	threadID = strings.TrimSpace(threadID)
	if err := ctx.Err(); err != nil || !validSessionID(threadID) {
		return "", false, err
	}
	if provider == "cursor" {
		meta, found, err := findCursorChat(ctx, threadID)
		return meta.CWD, found, err
	}
	path, err := findNativeSessionPath(ctx, provider, threadID)
	if err != nil || path == "" {
		return "", false, err
	}
	meta, ok := sessionMetadata(path, provider)
	if !ok || strings.TrimSpace(meta.CWD) == "" {
		return "", false, nil
	}
	return meta.CWD, true, nil
}

func findNativeSessionPath(ctx context.Context, provider, threadID string) (string, error) {
	if provider == "codex" {
		return CodexRolloutPath(threadID), ctx.Err()
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(home, ".codebuddy", "projects")
	if provider == "cursor" {
		root = filepath.Join(home, ".cursor", "projects")
	} else if provider != "workbuddy" {
		return "", nil
	}
	found := ""
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		if ctx.Err() != nil {
			return fs.SkipAll
		}
		if provider == "cursor" {
			if strings.Contains(path, string(filepath.Separator)+"agent-transcripts"+string(filepath.Separator)) &&
				filepath.Base(filepath.Dir(path)) == threadID && entry.Name() == threadID+".jsonl" {
				found = path
				return fs.SkipAll
			}
			return nil
		}
		meta, ok := workBuddySessionMeta(path)
		if ok && meta.ID == threadID {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return found, nil
}

func transcriptContainsTool(path, provider, toolName string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), maxTranscriptLine)
	deferredName := `mcp__lazymind__` + toolName
	for scanner.Scan() {
		line := scanner.Bytes()
		if provider == "workbuddy" && bytes.Contains(line, []byte(deferredName)) {
			return true
		}
		if provider == "cursor" && bytes.Contains(line, []byte(`"server":"lazymind"`)) &&
			bytes.Contains(line, []byte(`"toolName":"`+toolName+`"`)) {
			return true
		}
	}
	return false
}

func sessionMetadata(path, provider string) (sessionMeta, bool) {
	switch provider {
	case "codex":
		return codexSessionMeta(path)
	case "workbuddy":
		return workBuddySessionMeta(path)
	case "cursor":
		threadID := filepath.Base(filepath.Dir(path))
		return sessionMeta{ID: threadID}, validSessionID(threadID)
	default:
		return sessionMeta{}, false
	}
}

func transcriptTurns(path, provider string) []chatagent.NativeTurn {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), maxTranscriptLine)
	turns := make([]chatagent.NativeTurn, 0)
	var current *chatagent.NativeTurn
	lineNumber := 0
	flush := func() {
		if current == nil || strings.TrimSpace(current.User) == "" {
			current = nil
			return
		}
		current.User = compactText(current.User, maxTurnRunes)
		current.Assistant = compactText(current.Assistant, maxTurnRunes)
		if provider == "codex" {
			current.Images = codexUserImages(current.User)
		}
		turns = append(turns, *current)
		current = nil
	}
	for scanner.Scan() {
		lineNumber++
		message := decodeTranscriptMessage(scanner.Bytes(), provider)
		switch message.role {
		case "user":
			managed := managedUserText(message.text) || strings.HasPrefix(message.id, "h_")
			text := cleanUserText(message.text)
			if text == "" {
				continue
			}
			if current != nil && current.Assistant == "" && equivalentCodexUserText(current.User, text) {
				current.User = text
				if !message.timestamp.IsZero() {
					current.CreatedAt = message.timestamp
				}
				continue
			}
			flush()
			turnID := message.id
			if turnID == "" {
				turnID = stableTurnID(path, lineNumber, text)
			}
			current = &chatagent.NativeTurn{ID: turnID, User: text, CreatedAt: message.timestamp, Managed: managed}
		case "identity":
			if current != nil && message.id != "" && cleanUserText(message.text) == current.User {
				current.ID, current.Managed = message.id, true
			}
		case "assistant":
			if current != nil && message.text != "" {
				if current.Assistant != "" {
					current.Assistant += "\n"
				}
				current.Assistant += message.text
			}
		case "result":
			if current != nil && current.Assistant == "" && message.text != "" {
				current.Assistant = message.text
			}
		}
	}
	flush()
	return turns
}

func codexUserImages(text string) []chatagent.NativeImage {
	const maxImageBytes = 5 << 20
	images := make([]chatagent.NativeImage, 0)
	for _, match := range codexImagePathPattern.FindAllStringSubmatch(text, 4) {
		if len(match) < 2 {
			continue
		}
		path := filepath.Clean(strings.TrimSpace(match[1]))
		switch strings.ToLower(filepath.Ext(path)) {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		default:
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxImageBytes {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		images = append(images, chatagent.NativeImage{
			Name: filepath.Base(path), Base64: base64.StdEncoding.EncodeToString(content),
		})
	}
	return images
}

func transcriptTitle(path, provider string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), maxTranscriptLine)
	for scanner.Scan() {
		message := decodeTranscriptMessage(scanner.Bytes(), provider)
		if message.role == "user" {
			if title := compactLabel(cleanUserText(message.text), 100); title != "" {
				return title
			}
		}
	}
	return ""
}

type transcriptMessage struct {
	role      string
	id        string
	text      string
	timestamp time.Time
}

func decodeTranscriptMessage(line []byte, provider string) transcriptMessage {
	var row map[string]any
	if json.Unmarshal(line, &row) != nil {
		return transcriptMessage{}
	}
	switch provider {
	case "codex":
		payload, _ := row["payload"].(map[string]any)
		if row["type"] == "event_msg" && payload["type"] == "user_message" {
			return transcriptMessage{
				role: "identity", id: stringValue(payload["client_id"]),
				text: stringValue(payload["message"]), timestamp: timestampValue(row["timestamp"]),
			}
		}
		if row["type"] != "response_item" {
			return transcriptMessage{}
		}
		if payload["type"] != "message" {
			return transcriptMessage{}
		}
		role := stringValue(payload["role"])
		id := stringValue(payload["clientId"])
		if id == "" {
			id = stringValue(payload["id"])
		}
		return transcriptMessage{
			role: role, id: id,
			text: contentText(payload["content"]), timestamp: timestampValue(row["timestamp"]),
		}
	case "workbuddy":
		role := stringValue(row["role"])
		if row["type"] == "message" && (role == "user" || role == "assistant") {
			return transcriptMessage{
				role: role, id: stringValue(row["id"]), text: contentText(row["content"]),
				timestamp: timestampValue(row["timestamp"]),
			}
		}
		if row["type"] == "result" && row["subtype"] == "success" {
			return transcriptMessage{role: "result", text: stringValue(row["result"]), timestamp: timestampValue(row["timestamp"])}
		}
	case "cursor":
		role := stringValue(row["role"])
		message, _ := row["message"].(map[string]any)
		if role == "user" || role == "assistant" {
			return transcriptMessage{
				role: role, id: stringValue(row["id"]), text: contentText(message["content"]),
				timestamp: timestampValue(row["timestamp_ms"]),
			}
		}
		if row["type"] == "result" && row["subtype"] == "success" {
			return transcriptMessage{role: "result", text: stringValue(row["result"]), timestamp: timestampValue(row["timestamp_ms"])}
		}
	}
	return transcriptMessage{}
}

func equivalentCodexUserText(left, right string) bool {
	canonical := func(value string) string {
		if marker := strings.LastIndex(strings.ToLower(value), "my request for codex:"); marker >= 0 {
			value = value[marker+len("my request for codex:"):]
		}
		if image := strings.Index(strings.ToLower(value), "<image"); image >= 0 {
			value = value[:image]
		}
		return strings.Join(strings.Fields(value), " ")
	}
	return canonical(left) != "" && canonical(left) == canonical(right)
}

func contentText(value any) string {
	items, ok := value.([]any)
	if !ok {
		return stringValue(value)
	}
	parts := make([]string, 0, len(items))
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		kind := stringValue(item["type"])
		if kind != "text" && kind != "input_text" && kind != "output_text" {
			continue
		}
		if text := stringValue(item["text"]); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func cleanUserText(text string) string {
	text = strings.TrimSpace(text)
	if managedUserText(text) {
		const marker = "\n\nCurrent user request:\n"
		return compactText(text[strings.LastIndex(text, marker)+len(marker):], maxTurnRunes)
	}
	if start := strings.LastIndex(text, "<user_query>"); start >= 0 {
		start += len("<user_query>")
		if end := strings.Index(text[start:], "</user_query>"); end >= 0 {
			return compactText(text[start:start+end], maxTurnRunes)
		}
	}
	if strings.HasPrefix(text, "<system-reminder") || strings.HasPrefix(text, "<recommended_plugins>") ||
		strings.HasPrefix(text, "# AGENTS.md instructions") || strings.HasPrefix(text, "<environment_context>") {
		return ""
	}
	return compactText(text, maxTurnRunes)
}

func managedUserText(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "You are the execution Agent for one LazyMind Chat turn.") &&
		strings.Contains(text, "\n\nCurrent user request:\n")
}

func stringValue(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func timestampValue(value any) time.Time {
	switch raw := value.(type) {
	case string:
		parsed, _ := time.Parse(time.RFC3339Nano, raw)
		return parsed.UTC()
	case float64:
		seconds := int64(raw) / 1000
		nanos := (int64(raw) % 1000) * int64(time.Millisecond)
		return time.Unix(seconds, nanos).UTC()
	case json.Number:
		milliseconds, _ := strconv.ParseInt(string(raw), 10, 64)
		return time.UnixMilli(milliseconds).UTC()
	default:
		return time.Time{}
	}
}

func stableTurnID(path string, line int, text string) string {
	hash := sha256.Sum256([]byte(path + "\x00" + strconv.Itoa(line) + "\x00" + text))
	return "turn-" + hex.EncodeToString(hash[:12])
}

func compactText(value string, limit int) string {
	value = strings.Map(func(character rune) rune {
		if character == 0 || character < 32 && character != '\n' && character != '\t' {
			return -1
		}
		return character
	}, value)
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit])
	}
	return value
}

func compactLabel(value string, limit int) string {
	return compactText(strings.Join(strings.Fields(value), " "), limit)
}

func activityName(provider, threadID string) string {
	name := map[string]string{"codex": "Codex", "cursor": "Cursor", "workbuddy": "WorkBuddy"}[provider]
	if len(threadID) > 20 {
		threadID = threadID[:12] + "…" + threadID[len(threadID)-4:]
	}
	return name + " · " + threadID
}

func validSessionID(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && len([]rune(value)) <= 128
}
