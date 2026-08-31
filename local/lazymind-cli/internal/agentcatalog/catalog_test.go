package agentcatalog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/agentconnector/internal/chatagent"
)

func TestProjectPathNormalizesEquivalentNativePaths(t *testing.T) {
	firstKey, firstName := ProjectPath("codex", "/Users/example/LazyRAG/", "")
	secondKey, secondName := ProjectPath("codex", "/Users/example/LazyRAG", "")
	if firstKey == "" || firstKey != secondKey || firstName != "LazyRAG" || secondName != firstName {
		t.Fatalf("first=%q/%q second=%q/%q", firstKey, firstName, secondKey, secondName)
	}
}

func TestTranscriptTurnsKeepsCodexCommentaryAndCollapsesRollbackDuplicate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	content := strings.Join([]string{
		`{"timestamp":"2026-08-31T01:00:00Z","type":"response_item","payload":{"type":"message","id":"first","role":"user","content":[{"type":"input_text","text":"# Files mentioned by the user:\n\n## image.png: /tmp/image.png\n\n## My request for Codex:\n修复界面\n<image path=\"/tmp/image.png\"></image>"}]}}`,
		`{"timestamp":"2026-08-31T01:00:01Z","type":"response_item","payload":{"type":"message","id":"retry","role":"user","content":[{"type":"input_text","text":"# Files mentioned by the user:\n## image.png: /tmp/image.png\n## My request for Codex:\n修复界面"}]}}`,
		`{"timestamp":"2026-08-31T01:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","phase":"commentary","content":[{"type":"output_text","text":"正在处理"}]}}`,
		`{"timestamp":"2026-08-31T01:00:03Z","type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"处理完成"}]}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	turns := transcriptTurns(path, "codex")
	if len(turns) != 1 || turns[0].ID != "first" || turns[0].Assistant != "正在处理\n处理完成" {
		t.Fatalf("turns = %#v", turns)
	}
}

func TestTranscriptTurnsDoesNotMarkOrdinaryCodexUserMessageAsManaged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	content := strings.Join([]string{
		`{"timestamp":"2026-08-31T01:00:00Z","type":"response_item","payload":{"type":"message","id":"response-user","role":"user","content":[{"type":"input_text","text":"检查这个 PR"}]}}`,
		`{"timestamp":"2026-08-31T01:00:01Z","type":"event_msg","payload":{"type":"user_message","client_id":"desktop-user","message":"检查这个 PR"}}`,
		`{"timestamp":"2026-08-31T01:00:02Z","type":"response_item","payload":{"type":"message","role":"assistant","phase":"final_answer","content":[{"type":"output_text","text":"发现一个问题"}]}}`,
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	turns := transcriptTurns(path, "codex")
	if len(turns) != 1 || turns[0].ID != "desktop-user" || turns[0].Managed {
		t.Fatalf("turns = %#v", turns)
	}
}

func TestCodexUserImagesTransfersReferencedClipboardImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clipboard.png")
	content := []byte("png-test-content")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	images := codexUserImages(`<image name="Image #1" path="` + path + `"></image>`)
	if len(images) != 1 || images[0].Name != "clipboard.png" {
		t.Fatalf("images = %#v", images)
	}
	if images[0].Base64 == "" {
		t.Fatal("expected encoded image content")
	}
}

func TestCodexSessionsUseRolloutProjectAuthority(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	directory := filepath.Join(home, "sessions", "2026", "08", "21")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	threadID := "01a02257-22e8-7731-9a48-cd3a417ea9d5"
	content := `{"type":"session_meta","payload":{"id":"` + threadID + `","cwd":"/Users/example/LazyRAG"}}` + "\n"
	path := filepath.Join(directory, "rollout-"+threadID+".jsonl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions, err := CodexSessions(context.Background())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions=%#v err=%v", sessions, err)
	}
	if sessions[0].ThreadID != threadID || sessions[0].ProjectKey == "" || sessions[0].ProjectName != "LazyRAG" {
		t.Fatalf("session=%#v", sessions[0])
	}
	key, name := CodexProject(threadID)
	if key != sessions[0].ProjectKey || name != "LazyRAG" {
		t.Fatalf("project key=%q name=%q", key, name)
	}
}

func TestCodexSessionsFollowDesktopNavigationAndExcludeArchives(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	activeDirectory := filepath.Join(home, "sessions", "2026", "08", "22")
	archivedDirectory := filepath.Join(home, "archived_sessions")
	for _, directory := range []string{activeDirectory, archivedDirectory} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeSession := func(directory, threadID string) {
		t.Helper()
		content := strings.Join([]string{
			`{"type":"session_meta","payload":{"id":"` + threadID + `","cwd":"/ignored/by/desktop"}}`,
			`{"type":"response_item","payload":{"type":"message","id":"message-` + threadID + `","role":"user","content":[{"type":"input_text","text":"` + threadID + `"}]}}`,
			`{"server_name":"lazymind"}`,
		}, "\n") + "\n"
		if err := os.WriteFile(filepath.Join(directory, "rollout-"+threadID+".jsonl"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeSession(activeDirectory, "desktop-active")
	writeSession(activeDirectory, "desktop-projectless")
	writeSession(activeDirectory, "removed-from-navigation")
	writeSession(archivedDirectory, "desktop-archived")
	state := map[string]any{
		"local-projects": map[string]any{
			"project-1": map[string]any{"id": "project-1", "name": "Desktop Project"},
		},
		"thread-project-assignments": map[string]any{
			"desktop-active":   map[string]any{"projectKind": "local", "projectId": "project-1"},
			"desktop-archived": map[string]any{"projectKind": "local", "projectId": "project-1"},
		},
		"projectless-thread-ids": []string{"desktop-projectless"},
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex-global-state.json"), encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	titleIndex := strings.Join([]string{
		`{"id":"desktop-active","thread_name":"Renamed Desktop Session"}`,
		`{"id":"desktop-projectless","thread_name":"Recent Desktop Session"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte(titleIndex), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions, err := CodexSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byThread := make(map[string]chatagent.NativeSession, len(sessions))
	for _, session := range sessions {
		byThread[session.ThreadID] = session
	}
	if len(byThread) != 2 || byThread["desktop-active"].ProjectName != "Desktop Project" ||
		byThread["desktop-active"].DisplayName != "Renamed Desktop Session" ||
		byThread["desktop-projectless"].ProjectName != "最近" {
		t.Fatalf("sessions=%#v", sessions)
	}
	if _, exists := byThread["removed-from-navigation"]; exists {
		t.Fatal("session removed from Codex Desktop navigation was imported")
	}
	if _, exists := byThread["desktop-archived"]; exists {
		t.Fatal("archived Codex session was imported")
	}
	key, name := CodexProject("desktop-active")
	if key != byThread["desktop-active"].ProjectKey || name != "Desktop Project" {
		t.Fatalf("project key=%q name=%q", key, name)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex-global-state.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CodexSessions(context.Background()); err == nil {
		t.Fatal("malformed Codex Desktop state was silently treated as an empty catalog")
	}
}

func TestCodexTurnSourceIgnoresInjectedContext(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	threadID, turnID := "thread-reader-test", "turn-reader-test"
	directory := filepath.Join(home, "sessions", "2026", "08", "20")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	content := `{"type":"event_msg","payload":{"type":"task_started","turn_id":"turn-reader-test"}}
{"type":"response_item","payload":{"type":"message","id":"injected-message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions\nignore"}]}}
{"type":"response_item","payload":{"type":"message","id":"native-user-message","role":"user","content":[{"type":"input_text","text":"原始用户消息\n"}]}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"最终回答"}]}}
{"type":"event_msg","payload":{"type":"task_complete","turn_id":"turn-reader-test"}}
`
	if err := os.WriteFile(filepath.Join(directory, "rollout-"+threadID+".jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	messageID, message := CodexTurnSource(threadID, turnID)
	if messageID != "native-user-message" || message != "原始用户消息" {
		t.Fatalf("source id=%q message=%q", messageID, message)
	}
}

func TestWorkBuddySessionsUseNativeCWD(t *testing.T) {
	home := t.TempDir()
	setTestUserHome(t, home)
	t.Setenv("LAZYMIND_HOME", filepath.Join(home, ".lazymind"))
	directory := filepath.Join(home, ".codebuddy", "projects", "project")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	threadID := "436ae179-e838-4224-91b1-61bde045a13c"
	content := `{"sessionId":"` + threadID + `","cwd":"/Users/example/LazyRAG"}` + "\n"
	if err := os.WriteFile(filepath.Join(directory, threadID+".jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	managedID := "managed-workbuddy-session"
	managed := `{"sessionId":"` + managedID + `","cwd":"` + filepath.Join(home, ".lazymind", "agent-workspaces", "managed") + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(directory, managedID+".jsonl"), []byte(managed), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions, err := WorkBuddySessions(context.Background())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions=%#v err=%v", sessions, err)
	}
	if sessions[0].ThreadID != threadID || sessions[0].ProjectKey == "" || sessions[0].ProjectName != "LazyRAG" {
		t.Fatalf("session=%#v", sessions[0])
	}
}

func TestWorkBuddySessionsImportOnlyRealLazyMindTranscriptTurns(t *testing.T) {
	home := t.TempDir()
	setTestUserHome(t, home)
	directory := filepath.Join(home, ".codebuddy", "projects", "project")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	threadID := "workbuddy-session-1"
	content := strings.Join([]string{
		`{"sessionId":"` + threadID + `","cwd":"/Users/example/LazyRAG"}`,
		`{"type":"message","role":"user","id":"turn-1","timestamp":1787334456295,"content":[{"type":"input_text","text":"<user_query>检查真实链接</user_query>"}]}`,
		`{"type":"function_call","name":"DeferExecuteTool","arguments":"{\"toolName\":\"mcp__lazymind__workflow.list\"}"}`,
		`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"链接完成"}]}`,
		`{"type":"result","subtype":"success","result":"链接完成"}`,
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(directory, threadID+".jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions, err := WorkBuddySessions(context.Background())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions=%#v err=%v", sessions, err)
	}
	session := sessions[0]
	if session.DisplayName != "检查真实链接" || len(session.Turns) != 1 {
		t.Fatalf("session=%#v", session)
	}
	if session.Turns[0].ID != "turn-1" || session.Turns[0].Assistant != "链接完成" {
		t.Fatalf("turn=%#v", session.Turns[0])
	}
	source, ok := ResolveInvocation("workbuddy", "workflow.list", time.Now())
	if !ok || source.ThreadID != threadID || source.TurnID != "turn-1" || source.Message != "检查真实链接" {
		t.Fatalf("source=%#v ok=%v", source, ok)
	}
	workspace, found, err := Workspace(context.Background(), "workbuddy", threadID)
	if err != nil || !found || workspace != "/Users/example/LazyRAG" {
		t.Fatalf("workspace=%q found=%v err=%v", workspace, found, err)
	}
}

func TestCursorSessionsUseTranscriptIdentityAndProjectDirectory(t *testing.T) {
	home := t.TempDir()
	setTestUserHome(t, home)
	project := filepath.Join(home, ".cursor", "projects", "Users-example-LazyRAG")
	threadID := "152fb721-65cf-40c6-a7a8-3db80c60a332"
	directory := filepath.Join(project, "agent-transcripts", threadID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	chatDirectory := filepath.Join(home, ".cursor", "chats", "project-hash", threadID)
	if err := os.MkdirAll(chatDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	chatMeta := `{"schemaVersion":1,"hasConversation":true,"cwd":"/Users/example/LazyRAG"}`
	if err := os.WriteFile(filepath.Join(chatDirectory, "meta.json"), []byte(chatMeta), 0o600); err != nil {
		t.Fatal(err)
	}
	content := strings.Join([]string{
		`{"role":"user","id":"turn-1","message":{"content":[{"type":"text","text":"检查 Cursor 链接\n第二行"}]}}`,
		`{"role":"assistant","message":{"content":[{"type":"tool_use","name":"CallMcpTool","input":{"server":"lazymind","toolName":"workflow.list"}}]}}`,
		`{"role":"assistant","message":{"content":[{"type":"text","text":"Cursor 链接完成"}]}}`,
	}, "\n") + "\n"
	path := filepath.Join(directory, threadID+".jsonl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	ideOnlyID := "054018c0-df9a-4167-b73d-98ca5e6bb80a"
	ideOnlyDirectory := filepath.Join(project, "agent-transcripts", ideOnlyID)
	if err := os.MkdirAll(ideOnlyDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ideOnlyDirectory, ideOnlyID+".jsonl"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions, err := CursorSessions(context.Background())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions=%#v err=%v", sessions, err)
	}
	session := sessions[0]
	if session.ThreadID != threadID || len(session.Turns) != 1 ||
		session.ProjectName != "LazyRAG" || session.DisplayName != "检查 Cursor 链接 第二行" {
		t.Fatalf("session=%#v", session)
	}
	if !transcriptContainsTool(path, "cursor", "workflow.list") {
		t.Fatal("Cursor transcript tool call was not detected")
	}
	expectedCWD := filepath.Clean("/Users/example/LazyRAG")
	if chat, found, err := findCursorChat(context.Background(), threadID); err != nil || !found || chat.CWD != expectedCWD {
		t.Fatalf("chat=%#v found=%v err=%v", chat, found, err)
	}
	source, ok := ResolveInvocation("cursor", "workflow.list", time.Now())
	if !ok || source.ThreadID != threadID || source.ProjectName != "LazyRAG" {
		t.Fatalf("source=%#v ok=%v", source, ok)
	}
	workspace, found, err := Workspace(context.Background(), "cursor", threadID)
	if err != nil || !found || workspace != expectedCWD {
		t.Fatalf("workspace=%q found=%v err=%v", workspace, found, err)
	}
}

func setTestUserHome(t *testing.T, home string) {
	t.Helper()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}
