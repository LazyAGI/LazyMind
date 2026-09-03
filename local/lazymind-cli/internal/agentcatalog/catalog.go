package agentcatalog

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode"

	"lazymind/agentconnector/internal/chatagent"
)

const maxSessions = 5000

var codexRolloutPaths sync.Map
var codexDesktopProjectCache sync.Map
var codexSessionTitleCache sync.Map

type codexDesktopProject struct {
	Reference string
	Name      string
}

type cachedCodexDesktopProjects struct {
	modTime  int64
	size     int64
	projects map[string]codexDesktopProject
}

type cachedCodexSessionTitles struct {
	modTime int64
	size    int64
	titles  map[string]string
}

type cursorChat struct {
	ID  string
	CWD string
}

func Project(provider, reference, name string) (string, string) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	reference = strings.TrimSpace(reference)
	name = cleanName(name)
	if reference == "" {
		return "", ""
	}
	if name == "" {
		name = cleanName(filepath.Base(filepath.Clean(reference)))
	}
	if name == "" || name == "." || name == string(filepath.Separator) {
		name = "其他项目"
	}
	hash := sha256.Sum256([]byte(provider + "\x00" + reference))
	return provider + "-" + hex.EncodeToString(hash[:12]), name
}

func ProjectPath(provider, path, name string) (string, string) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", ""
	}
	return Project(provider, filepath.Clean(path), name)
}

func CodexProject(threadID string) (string, string) {
	home := codexHome()
	if projects, authoritative, err := codexDesktopProjects(home); err == nil && authoritative {
		project, ok := projects[strings.TrimSpace(threadID)]
		if !ok {
			return "", ""
		}
		return Project("codex", project.Reference, project.Name)
	}
	path := CodexRolloutPath(threadID)
	if path == "" {
		return "", ""
	}
	meta, ok := codexSessionMeta(path)
	if !ok {
		return "", ""
	}
	return ProjectPath("codex", meta.CWD, "")
}

func CodexSessions(ctx context.Context) ([]chatagent.NativeSession, error) {
	home := codexHome()
	if home == "" {
		return nil, nil
	}
	projects, authoritative, err := codexDesktopProjects(home)
	if err != nil {
		return nil, err
	}
	titles, err := codexSessionTitles(home)
	if err != nil {
		return nil, err
	}
	byThread := map[string]chatagent.NativeSession{}
	err = filepath.WalkDir(filepath.Join(home, "sessions"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		if ctx.Err() != nil || len(byThread) >= maxSessions {
			return fs.SkipAll
		}
		meta, ok := codexSessionMeta(path)
		if !ok {
			return nil
		}
		project, visible := projects[meta.ID]
		if authoritative && !visible {
			return nil
		}
		session, ok := nativeSession(path, "codex", meta.CWD, "", true)
		if !ok {
			return nil
		}
		if authoritative {
			session.ProjectKey, session.ProjectName = Project("codex", project.Reference, project.Name)
		}
		if title := titles[meta.ID]; title != "" {
			session.DisplayName = title
		}
		codexRolloutPaths.Store(meta.ID, path)
		byThread[meta.ID] = session
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sessions := make([]chatagent.NativeSession, 0, len(byThread))
	for _, session := range byThread {
		sessions = append(sessions, session)
	}
	return sessions, ctx.Err()
}

func codexSessionTitles(home string) (map[string]string, error) {
	path := filepath.Join(home, "session_index.jsonl")
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if cached, ok := codexSessionTitleCache.Load(path); ok {
		entry := cached.(cachedCodexSessionTitles)
		if entry.size == info.Size() && entry.modTime == info.ModTime().UnixNano() {
			return entry.titles, nil
		}
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	titles := map[string]string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 16<<10), 1<<20)
	for scanner.Scan() {
		var row struct {
			ID   string `json:"id"`
			Name string `json:"thread_name"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) == nil && validSessionID(row.ID) {
			if name := cleanName(row.Name); name != "" {
				titles[row.ID] = name
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	codexSessionTitleCache.Store(path, cachedCodexSessionTitles{
		modTime: info.ModTime().UnixNano(), size: info.Size(), titles: titles,
	})
	return titles, nil
}

func codexDesktopProjects(home string) (map[string]codexDesktopProject, bool, error) {
	path := filepath.Join(home, ".codex-global-state.json")
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, err
	}
	if cached, ok := codexDesktopProjectCache.Load(path); ok {
		entry := cached.(cachedCodexDesktopProjects)
		if entry.size == info.Size() && entry.modTime == info.ModTime().UnixNano() {
			return entry.projects, true, nil
		}
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, true, err
	}
	var state struct {
		Projects map[string]struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"local-projects"`
		Assignments map[string]struct {
			ProjectKind string `json:"projectKind"`
			ProjectID   string `json:"projectId"`
		} `json:"thread-project-assignments"`
		Projectless []string `json:"projectless-thread-ids"`
	}
	if err := json.Unmarshal(body, &state); err != nil {
		return nil, true, err
	}
	projects := make(map[string]codexDesktopProject, len(state.Assignments)+len(state.Projectless))
	for threadID, assignment := range state.Assignments {
		if assignment.ProjectKind != "local" || !validSessionID(threadID) {
			continue
		}
		project, ok := state.Projects[assignment.ProjectID]
		if !ok {
			continue
		}
		projectID := strings.TrimSpace(project.ID)
		if projectID == "" {
			projectID = strings.TrimSpace(assignment.ProjectID)
		}
		name := cleanName(project.Name)
		if projectID != "" && name != "" {
			projects[threadID] = codexDesktopProject{Reference: "desktop-project:" + projectID, Name: name}
		}
	}
	for _, threadID := range state.Projectless {
		threadID = strings.TrimSpace(threadID)
		if validSessionID(threadID) {
			if _, assigned := projects[threadID]; !assigned {
				projects[threadID] = codexDesktopProject{Reference: "desktop-projectless", Name: "最近"}
			}
		}
	}
	codexDesktopProjectCache.Store(path, cachedCodexDesktopProjects{
		modTime: info.ModTime().UnixNano(), size: info.Size(), projects: projects,
	})
	return projects, true, nil
}

func WorkBuddySessions(ctx context.Context) ([]chatagent.NativeSession, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	root := filepath.Join(home, ".workbuddy", "projects")
	byThread := map[string]chatagent.NativeSession{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		if ctx.Err() != nil || len(byThread) >= maxSessions {
			return fs.SkipAll
		}
		meta, ok := workBuddySessionMeta(path)
		if !ok || isLazyMindWorkspace(meta.CWD) {
			return nil
		}
		if session, ok := nativeSession(path, "workbuddy", meta.CWD, "", true); ok {
			byThread[meta.ID] = session
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sessions := make([]chatagent.NativeSession, 0, len(byThread))
	for _, session := range byThread {
		sessions = append(sessions, session)
	}
	return sessions, ctx.Err()
}

func CursorSessions(ctx context.Context) ([]chatagent.NativeSession, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	chats, err := cursorChats(ctx, home)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]cursorChat, len(chats))
	for _, chat := range chats {
		byID[chat.ID] = chat
	}
	root := filepath.Join(home, ".cursor", "projects")
	byThread := map[string]chatagent.NativeSession{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") ||
			!strings.Contains(path, string(filepath.Separator)+"agent-transcripts"+string(filepath.Separator)) {
			return nil
		}
		if ctx.Err() != nil || len(byThread) >= maxSessions {
			return fs.SkipAll
		}
		threadID := filepath.Base(filepath.Dir(path))
		if entry.Name() != threadID+".jsonl" {
			return nil
		}
		projectReference, projectName, workspaceFound := cursorTranscriptProject(path, home)
		if chat, found := byID[threadID]; found && strings.TrimSpace(chat.CWD) != "" {
			projectReference = filepath.Clean(chat.CWD)
			projectName = filepath.Base(projectReference)
			workspaceFound = true
		}
		if workspaceFound && isLazyMindWorkspace(projectReference) {
			return nil
		}
		if session, ok := nativeSession(path, "cursor", projectReference, projectName, true); ok &&
			len(session.Turns) > 0 {
			byThread[threadID] = session
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	sessions := make([]chatagent.NativeSession, 0, len(byThread))
	for _, session := range byThread {
		sessions = append(sessions, session)
	}
	return sessions, ctx.Err()
}

func cursorChats(ctx context.Context, home string) ([]cursorChat, error) {
	root := filepath.Join(home, ".cursor", "chats")
	chats := make([]cursorChat, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || entry.Name() != "meta.json" {
			return nil
		}
		if ctx.Err() != nil {
			return fs.SkipAll
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var meta struct {
			CWD string `json:"cwd"`
		}
		threadID := filepath.Base(filepath.Dir(path))
		if json.Unmarshal(body, &meta) == nil && validSessionID(threadID) && strings.TrimSpace(meta.CWD) != "" {
			chats = append(chats, cursorChat{ID: threadID, CWD: filepath.Clean(meta.CWD)})
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return chats, ctx.Err()
}

func findCursorChat(ctx context.Context, threadID string) (cursorChat, bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return cursorChat{}, false, err
	}
	chats, err := cursorChats(ctx, home)
	if err != nil {
		return cursorChat{}, false, err
	}
	for _, chat := range chats {
		if chat.ID == threadID && strings.TrimSpace(chat.CWD) != "" {
			return chat, true, nil
		}
	}
	path, err := findNativeSessionPath(ctx, "cursor", threadID)
	if err != nil || path == "" {
		return cursorChat{}, false, err
	}
	workspace, _, found := cursorTranscriptProject(path, home)
	if !found {
		return cursorChat{}, false, nil
	}
	return cursorChat{ID: threadID, CWD: workspace}, true, nil
}

func cursorTranscriptProject(path, home string) (string, string, bool) {
	projectDirectory := filepath.Dir(filepath.Dir(filepath.Dir(path)))
	if workspace := cursorTerminalWorkspace(projectDirectory); workspace != "" {
		return workspace, filepath.Base(workspace), true
	}
	slug := filepath.Base(projectDirectory)
	for _, root := range []string{home, filepath.Join(string(filepath.Separator), "private", "tmp"), os.TempDir()} {
		if workspace := resolveCursorWorkspace(root, slug, 16); workspace != "" {
			return workspace, filepath.Base(workspace), true
		}
	}
	name := slug
	if separator := strings.LastIndex(name, "-"); separator >= 0 && separator+1 < len(name) {
		name = name[separator+1:]
	}
	return "cursor-project:" + slug, cleanName(name), false
}

func cursorTerminalWorkspace(projectDirectory string) string {
	entries, err := os.ReadDir(filepath.Join(projectDirectory, "terminals"))
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}
		file, err := os.Open(filepath.Join(projectDirectory, "terminals", entry.Name()))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		for line := 0; line < 8 && scanner.Scan(); line++ {
			raw := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(raw, "cwd:") {
				continue
			}
			value := strings.TrimSpace(strings.TrimPrefix(raw, "cwd:"))
			if value == "" {
				continue
			}
			workspace, err := strconv.Unquote(value)
			if err == nil && filepath.IsAbs(workspace) {
				_ = file.Close()
				return filepath.Clean(workspace)
			}
		}
		_ = file.Close()
	}
	return ""
}

func resolveCursorWorkspace(root, slug string, depth int) string {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "." || slug == "" || depth < 0 {
		return ""
	}
	encoded := cursorWorkspaceSlug(root)
	if encoded == slug {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return root
		}
		return ""
	}
	if !strings.HasPrefix(slug, encoded+"-") {
		return ""
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		child := filepath.Join(root, entry.Name())
		childSlug := cursorWorkspaceSlug(child)
		if slug != childSlug && !strings.HasPrefix(slug, childSlug+"-") {
			continue
		}
		if resolved := resolveCursorWorkspace(child, slug, depth-1); resolved != "" {
			return resolved
		}
	}
	return ""
}

func cursorWorkspaceSlug(path string) string {
	path = filepath.Clean(path)
	path = strings.TrimPrefix(path, filepath.VolumeName(path))
	path = strings.TrimLeft(path, `/\`)
	return strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(path)
}

func isLazyMindWorkspace(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	home := strings.TrimSpace(os.Getenv("LAZYMIND_HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return false
		}
		home = filepath.Join(userHome, ".lazymind")
	}
	root, err := filepath.Abs(filepath.Join(home, "agent-workspaces"))
	if err != nil {
		return false
	}
	resolved, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(root, resolved)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func CodexRolloutPath(threadID string) string {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return ""
	}
	if cached, ok := codexRolloutPaths.Load(threadID); ok {
		return cached.(string)
	}
	home := codexHome()
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

type sessionMeta struct {
	ID  string
	CWD string
}

func codexSessionMeta(path string) (sessionMeta, bool) {
	file, err := os.Open(path)
	if err != nil {
		return sessionMeta{}, false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 16<<10), 1<<20)
	for scanner.Scan() {
		var row struct {
			Type    string `json:"type"`
			Payload struct {
				ID  string `json:"id"`
				CWD string `json:"cwd"`
			} `json:"payload"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil || row.Type != "session_meta" {
			continue
		}
		row.Payload.ID = strings.TrimSpace(row.Payload.ID)
		row.Payload.CWD = strings.TrimSpace(row.Payload.CWD)
		return sessionMeta{ID: row.Payload.ID, CWD: row.Payload.CWD}, row.Payload.ID != "" && row.Payload.CWD != ""
	}
	return sessionMeta{}, false
}

func workBuddySessionMeta(path string) (sessionMeta, bool) {
	file, err := os.Open(path)
	if err != nil {
		return sessionMeta{}, false
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 16<<10), 1<<20)
	for scanner.Scan() {
		var row struct {
			SessionID string `json:"sessionId"`
			CWD       string `json:"cwd"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil {
			continue
		}
		row.SessionID = strings.TrimSpace(row.SessionID)
		row.CWD = strings.TrimSpace(row.CWD)
		return sessionMeta{ID: row.SessionID, CWD: row.CWD}, row.SessionID != "" && row.CWD != ""
	}
	return sessionMeta{}, false
}

func codexHome() string {
	if configured := strings.TrimSpace(os.Getenv("CODEX_HOME")); configured != "" {
		return configured
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

func cleanName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, value)
	if len([]rune(value)) > 200 {
		value = string([]rune(value)[:200])
	}
	return value
}
