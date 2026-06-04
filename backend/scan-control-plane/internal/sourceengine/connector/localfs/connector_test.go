package localfs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/worker"
)

func TestValidateTargetCanonicalFingerprintAndClientOnly(t *testing.T) {
	t.Parallel()

	agent := newAgentStub()
	conn := NewLocalFSConnector(agent, WithAllowedPrefixes("/workspace"))
	ctx := context.Background()
	first := validateLocalTarget(t, ctx, conn, "/workspace/docs/../docs")
	second := validateLocalTarget(t, ctx, conn, "/workspace/docs")

	if first.TargetRef != "/workspace/docs" || second.TargetRef != "/workspace/docs" {
		t.Fatalf("target refs were not canonical: first=%q second=%q", first.TargetRef, second.TargetRef)
	}
	if first.TargetFingerprint != second.TargetFingerprint || first.RootObjectKey != second.RootObjectKey {
		t.Fatalf("canonical aliases should share fingerprint/root key: first=%+v second=%+v", first, second)
	}
	if agent.validateCalls != 2 {
		t.Fatalf("expected validation to use only the agent client, got %d calls", agent.validateCalls)
	}
}

func TestListFetchMapExportAndStableIDDedupe(t *testing.T) {
	t.Parallel()

	agent := newAgentStub()
	temp := &localTempStoreStub{}
	conn := NewLocalFSConnector(agent, WithTempObjectStore(temp))
	ctx := context.Background()
	stagedPath := filepath.Join(t.TempDir(), "a.md")
	if err := os.WriteFile(stagedPath, []byte("content from staging"), 0o600); err != nil {
		t.Fatalf("write staged file: %v", err)
	}
	agent.exportContentURI = "file://" + stagedPath

	children, err := conn.ListChildren(ctx, connector.ListChildrenRequest{
		TargetType: TargetTypeLocalPath,
		TargetRef:  "/workspace/docs",
		ListMode:   connector.ListModeAllCurrentLevel,
		PageSize:   10,
		MaxItems:   10,
		AgentID:    "agent-1",
	})
	if err != nil {
		t.Fatalf("list children: %v", err)
	}
	if got := localObjectKeys(children.Items); !sameStrings(got, []string{
		"local_fs:agent-1:id:file-a",
		"local_fs:agent-1:id:folder-guides",
	}) {
		t.Fatalf("expected duplicate path alias to collapse by stable id, got %v", got)
	}

	normalized, err := conn.MapObject(ctx, children.Items[0])
	if err != nil {
		t.Fatalf("map object: %v", err)
	}
	if normalized.SourceVersion != "10:5" || !normalized.IsDocument || normalized.IsContainer {
		t.Fatalf("unexpected mapped local file: %+v", normalized)
	}

	page, err := conn.FetchPage(ctx, connector.FetchPageRequest{
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		TargetType:        TargetTypeLocalPath,
		TargetRef:         "/workspace/docs",
		ScopeType:         connector.ScopeTypeFull,
		PageSize:          10,
		AgentID:           "agent-1",
	})
	if err != nil {
		t.Fatalf("fetch page: %v", err)
	}
	if got := localObjectKeys(page.Items); !sameStrings(got, []string{
		"local_fs:agent-1:id:file-a",
		"local_fs:agent-1:id:folder-guides",
	}) {
		t.Fatalf("expected fetch dedupe by stable id, got %v", got)
	}

	exported, err := conn.ExportObject(ctx, connector.ExportObjectRequest{
		ObjectKey:     normalized.ObjectKey,
		SourceVersion: normalized.SourceVersion,
		ExportFormat:  connector.ExportFormatOriginal,
		ProviderMeta:  normalized.ProviderMeta,
	})
	if err != nil {
		t.Fatalf("export object: %v", err)
	}
	if exported.ContentURI != "scan-temp://local-1" || exported.CleanupToken != "local-1" || exported.ExportedVersion != "10:5" || temp.objects["local-1"] != "content from staging" {
		t.Fatalf("unexpected exported local file: %+v", exported)
	}
	if agent.exportCalls != 1 {
		t.Fatalf("expected export to go through agent client, got %d calls", agent.exportCalls)
	}
}

func TestInitialRootsReturnBindableLocalPathTargets(t *testing.T) {
	t.Parallel()

	agent := newAgentStub()
	conn := NewLocalFSConnector(agent, WithRecommendedRoots("/workspace/docs"))
	page, err := conn.ListChildren(context.Background(), connector.ListChildrenRequest{
		AgentID:  "agent-1",
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("list initial roots: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected one local root, got %+v", page.Items)
	}
	root := page.Items[0]
	if !root.Bindable || root.BindingTargetType != TargetTypeLocalPath || root.BindingTargetRef != "/workspace/docs" || root.TreeKey == "" {
		t.Fatalf("initial root should be bindable local_path target, got %+v", root)
	}
}

func TestBindingTargetBrowseAndValidateUseDefaultAgentID(t *testing.T) {
	t.Parallel()

	agent := newAgentStub()
	conn := NewLocalFSConnector(agent, WithDefaultAgentID("agent-default"), WithRecommendedRoots("/workspace/docs"))
	ctx := context.Background()

	target, err := conn.ValidateTarget(ctx, connector.ValidateTargetRequest{
		ConnectorType: ConnectorType,
		TargetType:    TargetTypeLocalPath,
		TargetRef:     "/workspace/docs",
		UserID:        "user-1",
	})
	if err != nil {
		t.Fatalf("validate target without agent_id: %v", err)
	}
	if target.ProviderMeta["agent_id"] != "agent-default" || target.RootObjectKey != "local_fs:agent-default:id:root-docs" {
		t.Fatalf("validate did not use default agent id: %+v", target)
	}

	page, err := conn.ListChildren(ctx, connector.ListChildrenRequest{
		TargetType: TargetTypeLocalPath,
		TargetRef:  "/workspace/docs",
		ListMode:   connector.ListModePage,
		PageSize:   10,
	})
	if err != nil {
		t.Fatalf("list children without agent_id: %v", err)
	}
	if got := localObjectKeys(page.Items); !sameStrings(got, []string{
		"local_fs:agent-default:id:file-a",
		"local_fs:agent-default:id:folder-guides",
	}) {
		t.Fatalf("expected default agent object keys, got %v", got)
	}

	roots, err := conn.ListChildren(ctx, connector.ListChildrenRequest{
		PageSize: 10,
	})
	if err != nil {
		t.Fatalf("list roots without agent_id: %v", err)
	}
	if len(roots.Items) != 1 || roots.Items[0].ProviderMeta["agent_id"] != "agent-default" {
		t.Fatalf("initial roots did not use default agent id: %+v", roots.Items)
	}
}

func TestPublicRootMapsVirtualPathsForBrowseAndValidation(t *testing.T) {
	t.Parallel()

	agent := newAgentStub()
	conn := NewLocalFSConnector(agent, WithDefaultAgentID("agent-default"), WithRecommendedRoots("/"), WithPublicRoot("/host/root"))
	ctx := context.Background()

	target, err := conn.ValidateTarget(ctx, connector.ValidateTargetRequest{
		ConnectorType: ConnectorType,
		TargetType:    TargetTypeLocalPath,
		TargetRef:     "/project-a",
		UserID:        "user-1",
	})
	if err != nil {
		t.Fatalf("validate virtual target: %v", err)
	}
	if target.TargetRef != "/host/root/project-a" || target.ProviderMeta["path"] != "/host/root/project-a" {
		t.Fatalf("validation should save public host path, got %+v", target)
	}
	if agent.lastValidatePath != "/host/root/project-a" {
		t.Fatalf("validation sent wrong path to agent: %q", agent.lastValidatePath)
	}

	page, err := conn.ListChildren(ctx, connector.ListChildrenRequest{
		TargetType: TargetTypeLocalPath,
		TargetRef:  "/",
		ListMode:   connector.ListModePage,
		PageSize:   10,
	})
	if err != nil {
		t.Fatalf("list virtual root: %v", err)
	}
	if agent.lastListPath != "/host/root" {
		t.Fatalf("list root sent wrong path to agent: %q", agent.lastListPath)
	}
	if len(page.Items) != 1 {
		t.Fatalf("expected one virtual root child, got %+v", page.Items)
	}
	child := page.Items[0]
	if child.ObjectRef != "/project-a" || child.BindingTargetRef != "/project-a" {
		t.Fatalf("browse result should expose virtual path, got %+v", child)
	}
	if child.ObjectKey != "local_fs:agent-default:path:/host/root/project-a" || child.ProviderMeta["path"] != "/host/root/project-a" {
		t.Fatalf("browse result should keep public path metadata/key, got %+v", child)
	}

	nested, err := conn.ListChildren(ctx, connector.ListChildrenRequest{
		TargetType: TargetTypeLocalPath,
		TargetRef:  "/project-a",
		ListMode:   connector.ListModePage,
		PageSize:   10,
	})
	if err != nil {
		t.Fatalf("list virtual child: %v", err)
	}
	if agent.lastListPath != "/host/root/project-a" {
		t.Fatalf("list child sent wrong path to agent: %q", agent.lastListPath)
	}
	if len(nested.Items) != 1 || nested.Items[0].ObjectRef != "/project-a/readme.md" {
		t.Fatalf("nested browse result should expose virtual child path, got %+v", nested.Items)
	}

	fetched, err := conn.FetchPage(ctx, connector.FetchPageRequest{
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		TargetType:        TargetTypeLocalPath,
		TargetRef:         target.TargetRef,
		ScopeType:         connector.ScopeTypeFull,
		PageSize:          10,
		AgentID:           "agent-default",
	})
	if err != nil {
		t.Fatalf("fetch mapped target: %v", err)
	}
	if agent.lastStatPath != "/host/root/project-a" || agent.lastListPath != "/host/root/project-a" {
		t.Fatalf("fetch should use public host path, stat=%q list=%q", agent.lastStatPath, agent.lastListPath)
	}
	if len(fetched.Items) != 1 || fetched.Items[0].ObjectRef != "/host/root/project-a/readme.md" {
		t.Fatalf("fetch should keep public object paths for indexing, got %+v", fetched.Items)
	}
}

func TestPublicRootRejectsPathsOutsideMountedRoot(t *testing.T) {
	t.Parallel()

	agent := newAgentStub()
	conn := NewLocalFSConnector(agent, WithDefaultAgentID("agent-default"), WithRecommendedRoots("/"), WithPublicRoot("/host/root"))

	_, err := conn.ValidateTarget(context.Background(), connector.ValidateTargetRequest{
		ConnectorType: ConnectorType,
		TargetType:    TargetTypeLocalPath,
		TargetRef:     "/escape",
		UserID:        "user-1",
	})
	assertLocalErrorCode(t, err, connector.ErrorCodePermissionDenied)
}

func TestPublicRootDoesNotProbeExternalAbsolutePath(t *testing.T) {
	t.Parallel()

	agent := newAgentStub()
	conn := NewLocalFSConnector(agent, WithDefaultAgentID("agent-default"), WithRecommendedRoots("/"), WithPublicRoot("/host/root"))

	_, err := conn.ValidateTarget(context.Background(), connector.ValidateTargetRequest{
		ConnectorType: ConnectorType,
		TargetType:    TargetTypeLocalPath,
		TargetRef:     "/home/sensetime/project/docs",
		UserID:        "user-1",
	})
	assertLocalErrorCode(t, err, ErrorCodeTargetNotFound)
	if agent.lastValidatePath != "/host/root/home/sensetime/project/docs" {
		t.Fatalf("external absolute path escaped public root: %q", agent.lastValidatePath)
	}
}

func TestPublicRootRejectsStaleExternalExportPath(t *testing.T) {
	t.Parallel()

	agent := newAgentStub()
	conn := NewLocalFSConnector(agent, WithDefaultAgentID("agent-default"), WithRecommendedRoots("/"), WithPublicRoot("/host/root"))

	_, err := conn.ExportObject(context.Background(), connector.ExportObjectRequest{
		ObjectKey:     "local_fs:agent-default:path:/home/sensetime/project/docs/a.md",
		SourceVersion: "10:5",
		ProviderMeta: connector.ProviderMeta{
			"agent_id": "agent-default",
			"path":     "/home/sensetime/project/docs/a.md",
		},
	})
	assertLocalErrorCode(t, err, connector.ErrorCodePermissionDenied)
	if agent.exportCalls != 0 {
		t.Fatalf("stale external path should be rejected before agent export, calls=%d", agent.exportCalls)
	}
}

func TestSearchAndDeltaAreUnsupported(t *testing.T) {
	t.Parallel()

	conn := NewLocalFSConnector(newAgentStub())
	_, err := conn.Search(context.Background(), connector.SearchRequest{Keyword: "a"})
	assertLocalErrorCode(t, err, connector.ErrorCodeUnsupported)

	_, err = conn.FetchPage(context.Background(), connector.FetchPageRequest{
		BindingGeneration: 1,
		TargetType:        TargetTypeLocalPath,
		TargetRef:         "/workspace/docs",
		ScopeType:         connector.ScopeTypeDelta,
		PageSize:          10,
		AgentID:           "agent-1",
	})
	assertLocalErrorCode(t, err, connector.ErrorCodeUnsupportedDelta)
}

func TestWatchDeleteEventReturnsExplicitTombstoneWithoutStat(t *testing.T) {
	t.Parallel()

	agent := newAgentStub()
	conn := NewLocalFSConnector(agent)
	ctx := context.Background()

	page, err := conn.FetchPage(ctx, connector.FetchPageRequest{
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		TargetType:        TargetTypeLocalPath,
		TargetRef:         "/workspace/docs",
		ScopeType:         connector.ScopeTypeWatchEvent,
		ScopeRef:          connector.ScopeRef{"path": "/workspace/docs/deleted.md", "event_type": "deleted"},
		PageSize:          10,
		AgentID:           "agent-1",
	})
	if err != nil {
		t.Fatalf("fetch watch delete: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].DeletedAtSource == nil {
		t.Fatalf("expected one tombstone item, got %+v", page)
	}
	if page.Items[0].ObjectKey != "local_fs:agent-1:path:/workspace/docs/deleted.md" {
		t.Fatalf("unexpected tombstone object key: %+v", page.Items[0])
	}
	if agent.statCalls != 0 {
		t.Fatalf("delete watch event should not stat missing path, stat_calls=%d", agent.statCalls)
	}
}

func validateLocalTarget(t *testing.T, ctx context.Context, conn *LocalFSConnector, targetRef string) connector.NormalizedTarget {
	t.Helper()

	target, err := conn.ValidateTarget(ctx, connector.ValidateTargetRequest{
		ConnectorType: ConnectorType,
		TargetType:    TargetTypeLocalPath,
		TargetRef:     targetRef,
		AgentID:       "agent-1",
		UserID:        "user-1",
	})
	if err != nil {
		t.Fatalf("validate target %q: %v", targetRef, err)
	}
	return target
}

type agentStub struct {
	infos            map[string]PathInfo
	children         map[string][]PathInfo
	validateCalls    int
	statCalls        int
	exportCalls      int
	lastValidatePath string
	lastListPath     string
	lastStatPath     string
	lastExportPath   string
	exportContentURI string
}

func newAgentStub() *agentStub {
	root := PathInfo{Path: "/workspace/docs", NormalizedPath: "/workspace/docs", DisplayName: "docs", Exists: true, Readable: true, IsDir: true, MTimeUnixNano: 20, StableID: "root-docs"}
	file := PathInfo{Path: "/workspace/docs/a.md", NormalizedPath: "/workspace/docs/a.md", DisplayName: "a.md", Exists: true, Readable: true, SizeBytes: 5, MTimeUnixNano: 10, MimeType: "text/markdown", FileExtension: ".md", StableID: "file-a", ParentStableID: "root-docs", ParentPath: "/workspace/docs"}
	alias := file
	alias.Path = "/workspace/docs/alias-a.md"
	alias.NormalizedPath = "/workspace/docs/alias-a.md"
	alias.DisplayName = "alias-a.md"
	folder := PathInfo{Path: "/workspace/docs/guides", NormalizedPath: "/workspace/docs/guides", DisplayName: "guides", Exists: true, Readable: true, IsDir: true, MTimeUnixNano: 30, StableID: "folder-guides", ParentStableID: "root-docs", ParentPath: "/workspace/docs"}
	hostRoot := PathInfo{Path: "/host/root", NormalizedPath: "/host/root", DisplayName: "root", Exists: true, Readable: true, IsDir: true, MTimeUnixNano: 40}
	project := PathInfo{Path: "/host/root/project-a", NormalizedPath: "/host/root/project-a", DisplayName: "project-a", Exists: true, Readable: true, IsDir: true, MTimeUnixNano: 50, ParentPath: "/host/root"}
	readme := PathInfo{Path: "/host/root/project-a/readme.md", NormalizedPath: "/host/root/project-a/readme.md", DisplayName: "readme.md", Exists: true, Readable: true, SizeBytes: 6, MTimeUnixNano: 60, MimeType: "text/markdown", FileExtension: ".md", ParentPath: "/host/root/project-a"}
	outside := PathInfo{Path: "/outside", NormalizedPath: "/outside", DisplayName: "outside", Exists: true, Readable: true, IsDir: true, MTimeUnixNano: 70}
	escape := PathInfo{Path: "/host/root/escape", NormalizedPath: "/outside", DisplayName: "escape", Exists: true, Readable: true, IsDir: true, MTimeUnixNano: 80}
	return &agentStub{
		infos: map[string]PathInfo{
			"/workspace/docs":                root,
			"/workspace/docs/a.md":           file,
			"/workspace/docs/guides":         folder,
			"/workspace/docs/alias-a.md":     alias,
			"/host/root":                     hostRoot,
			"/host/root/project-a":           project,
			"/host/root/project-a/readme.md": readme,
			"/outside":                       outside,
			"/host/root/escape":              escape,
		},
		children: map[string][]PathInfo{
			"/workspace/docs":      {file, alias, folder},
			"/host/root":           {project},
			"/host/root/project-a": {readme},
		},
	}
}

func (a *agentStub) ValidatePath(_ context.Context, req ValidatePathRequest) (PathInfo, error) {
	a.validateCalls++
	path := cleanPath(req.Path)
	a.lastValidatePath = path
	if path == "/workspace/docs/../docs" {
		path = "/workspace/docs"
	}
	info, ok := a.infos[path]
	if !ok {
		return PathInfo{Path: path, NormalizedPath: path, Exists: false}, nil
	}
	return info, nil
}

func (a *agentStub) ListDir(_ context.Context, req ListDirRequest) (ListDirPage, error) {
	path := cleanPath(req.Path)
	a.lastListPath = path
	items := a.children[path]
	offset, _ := parseCursor(req.Cursor)
	if offset >= len(items) {
		return ListDirPage{}, nil
	}
	end := offset + req.PageSize
	if end > len(items) {
		end = len(items)
	}
	page := ListDirPage{Items: items[offset:end]}
	if end < len(items) {
		page.HasMore = true
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}

func (a *agentStub) StatPath(_ context.Context, req StatPathRequest) (PathInfo, error) {
	a.statCalls++
	path := cleanPath(req.Path)
	a.lastStatPath = path
	info, ok := a.infos[path]
	if !ok {
		return PathInfo{}, connector.NewError(ErrorCodeObjectNotFound, "missing path")
	}
	return info, nil
}

func (a *agentStub) ExportFile(_ context.Context, req ExportFileRequest) (ExportedFile, error) {
	a.exportCalls++
	path := cleanPath(req.Path)
	a.lastExportPath = path
	info, ok := a.infos[path]
	if !ok {
		return ExportedFile{}, connector.NewError(ErrorCodeObjectNotFound, "missing path")
	}
	if got := versionFor(info); got != req.ExpectedVersion {
		return ExportedFile{}, connector.NewError(connector.ErrorCodeVersionMismatch, fmt.Sprintf("got %s", got))
	}
	return ExportedFile{
		ContentURI:    a.exportContentURI,
		SizeBytes:     info.SizeBytes,
		MTimeUnixNano: info.MTimeUnixNano,
		MimeType:      info.MimeType,
		FileExtension: info.FileExtension,
		CleanupToken:  "cleanup-" + info.StableID,
	}, nil
}

type localTempStoreStub struct {
	objects map[string]string
}

func (s *localTempStoreStub) Put(_ context.Context, input worker.TempObjectInput) (worker.TempObject, error) {
	if s.objects == nil {
		s.objects = map[string]string{}
	}
	content, err := io.ReadAll(input.Reader)
	if err != nil {
		return worker.TempObject{}, err
	}
	token := "local-" + strconv.Itoa(len(s.objects)+1)
	s.objects[token] = string(content)
	return worker.TempObject{URI: "scan-temp://" + token, CleanupToken: token, SizeBytes: int64(len(content))}, nil
}

func localObjectKeys(items []connector.RawObject) []string {
	keys := make([]string, len(items))
	for i, item := range items {
		keys[i] = item.ObjectKey
	}
	return keys
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func assertLocalErrorCode(t *testing.T, err error, code connector.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %s, got nil", code)
	}
	got, ok := connector.ErrorCodeOf(err)
	if !ok || got != code {
		t.Fatalf("expected error code %s, got %v (ok=%v, err=%v)", code, got, ok, err)
	}
}
