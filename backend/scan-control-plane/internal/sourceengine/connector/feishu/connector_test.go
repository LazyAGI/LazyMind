package feishu

import (
	"context"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	"github.com/lazymind/scan_control_plane/internal/sourceengine/worker"
)

func TestValidateTargetStableFingerprintAndClientOnly(t *testing.T) {
	t.Parallel()

	auth := &authStub{}
	api := newFeishuAPIStub()
	conn := NewFeishuConnector(auth, api)
	temp := &feishuTempStoreStub{}
	conn.UseTempObjectStore(temp)
	ctx := context.Background()

	first := validateFeishuTarget(t, ctx, conn, TargetTypeDriveFolder, "drive:folder-root")
	second := validateFeishuTarget(t, ctx, conn, TargetTypeDriveFolder, "folder-root")
	if first.TargetFingerprint != second.TargetFingerprint || first.RootObjectKey != second.RootObjectKey {
		t.Fatalf("drive aliases should share fingerprint/root key: first=%+v second=%+v", first, second)
	}
	if first.DisplayName != "root" {
		t.Fatalf("drive target display name should come from metadata, got %+v", first)
	}
	if auth.calls != 2 || api.driveFolderCalls != 2 {
		t.Fatalf("expected auth/api clients only, auth=%d drive=%d", auth.calls, api.driveFolderCalls)
	}

	wiki := validateFeishuTarget(t, ctx, conn, TargetTypeWikiNode, "space-1:node-root")
	if wiki.TargetFingerprint != "feishu:wiki:space-1:node-root" {
		t.Fatalf("unexpected wiki fingerprint: %+v", wiki)
	}
	if wiki.DisplayName != "Wiki Root" {
		t.Fatalf("wiki target display name should come from metadata, got %+v", wiki)
	}
}

func TestDriveListFetchExportAndStableIDDedupe(t *testing.T) {
	t.Parallel()

	auth := &authStub{}
	api := newFeishuAPIStub()
	conn := NewFeishuConnector(auth, api)
	temp := &feishuTempStoreStub{}
	conn.UseTempObjectStore(temp)
	ctx := context.Background()

	children, err := conn.ListChildren(ctx, connector.ListChildrenRequest{
		TargetType:       TargetTypeDriveFolder,
		TargetRef:        "folder-root",
		ListMode:         connector.ListModeAllCurrentLevel,
		PageSize:         10,
		MaxItems:         10,
		AuthConnectionID: "auth-1",
	})
	if err != nil {
		t.Fatalf("list children: %v", err)
	}
	if got := feishuObjectKeys(children.Items); !sameStrings(got, []string{
		"feishu:drive:file-a",
		"feishu:drive:folder-guides",
	}) {
		t.Fatalf("expected duplicate drive paths to collapse by stable id, got %v", got)
	}

	normalized, err := conn.MapObject(ctx, children.Items[0])
	if err != nil {
		t.Fatalf("map object: %v", err)
	}
	if normalized.SourceVersion != "rev-a" || !normalized.IsDocument || normalized.IsContainer {
		t.Fatalf("unexpected mapped drive file: %+v", normalized)
	}

	page, err := conn.FetchPage(ctx, connector.FetchPageRequest{
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		TargetType:        TargetTypeDriveFolder,
		TargetRef:         "folder-root",
		ScopeType:         connector.ScopeTypeFull,
		PageSize:          10,
		AuthConnectionID:  "auth-1",
	})
	if err != nil {
		t.Fatalf("fetch page: %v", err)
	}
	if got := feishuObjectKeys(page.Items); !sameStrings(got, []string{
		"feishu:drive:file-a",
		"feishu:drive:folder-guides",
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
	if exported.ContentURI != "scan-temp://feishu-1" || exported.ExportedVersion != "rev-a" || temp.objects["feishu-1"] != "drive:file-a" {
		t.Fatalf("unexpected exported drive file: %+v", exported)
	}
	if api.downloadCalls != 1 {
		t.Fatalf("expected download through feishu api client, got %d calls", api.downloadCalls)
	}
}

func TestDriveDocumentExportUsesRawContent(t *testing.T) {
	t.Parallel()

	auth := &authStub{}
	api := newFeishuAPIStub()
	doc := Object{
		Kind:          ObjectKindDriveFile,
		Token:         "docx-a",
		ParentToken:   "folder-root",
		Name:          "perm_1",
		IsDocument:    true,
		Revision:      "rev-docx-a",
		FileExtension: ".docx",
		DriveType:     "docx",
		StableID:      "docx-a",
	}
	api.driveObjects["docx-a"] = doc
	api.driveChildren["folder-root"] = append(api.driveChildren["folder-root"], doc)
	conn := NewFeishuConnector(auth, api)
	temp := &feishuTempStoreStub{}
	conn.UseTempObjectStore(temp)
	ctx := context.Background()

	children, err := conn.ListChildren(ctx, connector.ListChildrenRequest{
		TargetType:       TargetTypeDriveFolder,
		TargetRef:        "folder-root",
		ListMode:         connector.ListModeAllCurrentLevel,
		PageSize:         10,
		MaxItems:         10,
		AuthConnectionID: "auth-1",
	})
	if err != nil {
		t.Fatalf("list children: %v", err)
	}
	var raw connector.RawObject
	for _, item := range children.Items {
		if item.ObjectKey == "feishu:drive:docx-a" {
			raw = item
			break
		}
	}
	if raw.ObjectKey == "" {
		t.Fatalf("expected drive doc in children, got %+v", children.Items)
	}
	normalized, err := conn.MapObject(ctx, raw)
	if err != nil {
		t.Fatalf("map object: %v", err)
	}
	if normalized.FileExtension != ".md" || normalized.MimeType != "text/markdown" {
		t.Fatalf("drive cloud document should be exposed as markdown, got ext=%q mime=%q", normalized.FileExtension, normalized.MimeType)
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
	if exported.ContentURI != "scan-temp://feishu-1" || temp.objects["feishu-1"] != "drive-doc:docx-a" {
		t.Fatalf("unexpected exported drive doc: %+v temp=%+v", exported, temp.objects)
	}
	if api.downloadCalls != 0 || api.driveExportCalls != 1 {
		t.Fatalf("expected drive document raw export only, download=%d export=%d", api.downloadCalls, api.driveExportCalls)
	}
}

func TestInitialRootsReturnDriveAndWikiVirtualBranches(t *testing.T) {
	t.Parallel()

	conn := NewFeishuConnector(&authStub{}, newFeishuAPIStub())
	page, err := conn.ListChildren(context.Background(), connector.ListChildrenRequest{PageSize: 10})
	if err != nil {
		t.Fatalf("list initial roots: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("expected drive/wiki virtual roots, got %+v", page.Items)
	}
	drive := page.Items[0]
	if !drive.Bindable || drive.BindingTargetType != TargetTypeDriveFolder || drive.BindingTargetRef != "drive:root" {
		t.Fatalf("drive virtual root should represent the drive root target, got %+v", drive)
	}
	wiki := page.Items[1]
	if wiki.Bindable || wiki.BindingTargetType != "" || wiki.BindingTargetRef != "" {
		t.Fatalf("wiki virtual root must not be bindable, got %+v", wiki)
	}
	if page.Items[0].ObjectRef != VirtualDriveRootRef || page.Items[1].ObjectRef != VirtualWikiSpacesRef {
		t.Fatalf("unexpected virtual roots: %+v", page.Items)
	}
}

func TestDriveVirtualRootListsRootChildrenAndWikiNodeExposesSemanticBindingTargets(t *testing.T) {
	t.Parallel()

	conn := NewFeishuConnector(&authStub{}, newFeishuAPIStub())
	drive, err := conn.ListChildren(context.Background(), connector.ListChildrenRequest{
		TargetType:       TargetTypeDriveFolder,
		NodeRef:          VirtualDriveRootRef,
		AuthConnectionID: "auth-1",
		PageSize:         10,
	})
	if err != nil {
		t.Fatalf("list drive root: %v", err)
	}
	if got := feishuObjectKeys(drive.Items); !sameStrings(got, []string{
		"feishu:drive:file-a",
		"feishu:drive:folder-guides",
	}) {
		t.Fatalf("drive virtual root should list real root children without an extra root layer, got %+v", drive.Items)
	}
	if drive.Items[1].BindingTargetType != TargetTypeDriveFolder || drive.Items[1].BindingTargetRef != "drive:folder-guides" {
		t.Fatalf("drive folder child should expose drive_folder target, got %+v", drive.Items[1])
	}

	wiki, err := conn.ListChildren(context.Background(), connector.ListChildrenRequest{
		TargetType:       TargetTypeWikiNode,
		NodeRef:          VirtualWikiSpacesRef,
		AuthConnectionID: "auth-1",
		PageSize:         10,
	})
	if err != nil {
		t.Fatalf("list wiki spaces: %v", err)
	}
	if len(wiki.Items) != 1 || wiki.Items[0].Bindable || wiki.Items[0].ObjectRef != "feishu:wiki:space:space-1" {
		t.Fatalf("wiki spaces should be virtual containers, got %+v", wiki.Items)
	}
}

func TestWikiFetchAndMarkdownExport(t *testing.T) {
	t.Parallel()

	auth := &authStub{}
	api := newFeishuAPIStub()
	conn := NewFeishuConnector(auth, api)
	temp := &feishuTempStoreStub{}
	conn.UseTempObjectStore(temp)
	ctx := context.Background()

	page, err := conn.FetchPage(ctx, connector.FetchPageRequest{
		SourceID:          "source-1",
		BindingID:         "binding-1",
		BindingGeneration: 1,
		TargetType:        TargetTypeWikiNode,
		TargetRef:         "space-1:node-root",
		ScopeType:         connector.ScopeTypeFull,
		PageSize:          10,
		AuthConnectionID:  "auth-1",
	})
	if err != nil {
		t.Fatalf("fetch wiki page: %v", err)
	}
	if got := feishuObjectKeys(page.Items); !sameStrings(got, []string{"feishu:wiki:space-1:node-child"}) {
		t.Fatalf("unexpected wiki fetch keys: %v", got)
	}
	normalized, err := conn.MapObject(ctx, page.Items[0])
	if err != nil {
		t.Fatalf("map wiki node: %v", err)
	}
	if !normalized.IsDocument || !normalized.IsContainer {
		t.Fatalf("expected wiki node to be dual-role, got %+v", normalized)
	}

	exported, err := conn.ExportObject(ctx, connector.ExportObjectRequest{
		ObjectKey:     normalized.ObjectKey,
		SourceVersion: normalized.SourceVersion,
		ExportFormat:  connector.ExportFormatMarkdown,
		ProviderMeta:  normalized.ProviderMeta,
	})
	if err != nil {
		t.Fatalf("export wiki markdown: %v", err)
	}
	if exported.ContentURI != "scan-temp://feishu-1" || exported.MimeType != "text/markdown" || temp.objects["feishu-1"] != "wiki:node-child" {
		t.Fatalf("unexpected wiki export: %+v", exported)
	}
}

func TestSearchReturnsUnsupportedForUnimplementedScopeAndDeltaUnsupported(t *testing.T) {
	t.Parallel()

	conn := NewFeishuConnector(&authStub{}, newFeishuAPIStub())
	if !conn.Spec().SupportsSearch {
		t.Fatalf("feishu spec should advertise search support per connector contract")
	}
	_, err := conn.Search(context.Background(), connector.SearchRequest{Keyword: "a"})
	assertFeishuErrorCode(t, err, connector.ErrorCodeUnsupported)

	_, err = conn.FetchPage(context.Background(), connector.FetchPageRequest{
		BindingGeneration: 1,
		TargetType:        TargetTypeDriveFolder,
		TargetRef:         "folder-root",
		ScopeType:         connector.ScopeTypeDelta,
		PageSize:          10,
		AuthConnectionID:  "auth-1",
	})
	assertFeishuErrorCode(t, err, connector.ErrorCodeUnsupportedDelta)
}

func TestVersionFallbackUsesProviderIdentity(t *testing.T) {
	t.Parallel()

	conn := NewFeishuConnector(&authStub{}, newFeishuAPIStub())
	raw := conn.rawObject("auth-1", Object{Kind: ObjectKindDriveFile, Token: "file-no-version", Name: "no-version.txt", IsDocument: true})
	normalized, err := conn.MapObject(context.Background(), raw)
	if err != nil {
		t.Fatalf("map object: %v", err)
	}
	if normalized.SourceVersion != "file-no-version" || normalized.SourceVersion == "unknown" {
		t.Fatalf("source version fallback should use provider identity, got %+v", normalized)
	}
}

func validateFeishuTarget(t *testing.T, ctx context.Context, conn *FeishuConnector, targetType connector.TargetType, targetRef string) connector.NormalizedTarget {
	t.Helper()

	target, err := conn.ValidateTarget(ctx, connector.ValidateTargetRequest{
		ConnectorType:    ConnectorType,
		TargetType:       targetType,
		TargetRef:        targetRef,
		AuthConnectionID: "auth-1",
		UserID:           "user-1",
	})
	if err != nil {
		t.Fatalf("validate target %s %q: %v", targetType, targetRef, err)
	}
	return target
}

type authStub struct {
	calls int
}

func (a *authStub) GetToken(context.Context, TokenRequest) (Token, error) {
	a.calls++
	return Token{AccessToken: "token-1"}, nil
}

type feishuAPIStub struct {
	driveObjects     map[string]Object
	wikiObjects      map[string]Object
	driveChildren    map[string][]Object
	wikiChildren     map[string][]Object
	wikiSpaces       []Object
	driveFolderCalls int
	downloadCalls    int
	driveExportCalls int
}

func newFeishuAPIStub() *feishuAPIStub {
	root := Object{Kind: ObjectKindDriveFolder, Token: "folder-root", Name: "root", IsContainer: true, HasChildren: true, Revision: "folder-rev"}
	file := Object{Kind: ObjectKindDriveFile, Token: "file-a", ParentToken: "folder-root", Name: "a.pdf", IsDocument: true, Revision: "rev-a", SizeBytes: 7, MimeType: "application/pdf", FileExtension: ".pdf", StableID: "file-a"}
	alias := file
	alias.Name = "alias-a.pdf"
	folder := Object{Kind: ObjectKindDriveFolder, Token: "folder-guides", ParentToken: "folder-root", Name: "guides", IsContainer: true, HasChildren: true, Revision: "folder-guides-rev"}
	wikiRoot := Object{Kind: ObjectKindWikiNode, Token: "node-root", SpaceID: "space-1", Name: "Wiki Root", IsDocument: true, IsContainer: true, HasChildren: true, Revision: "wiki-root-rev", MimeType: "text/markdown", FileExtension: ".md"}
	wikiChild := Object{Kind: ObjectKindWikiNode, Token: "node-child", ParentToken: "node-root", SpaceID: "space-1", Name: "Wiki Child", IsDocument: true, IsContainer: true, HasChildren: false, Revision: "wiki-child-rev", MimeType: "text/markdown", FileExtension: ".md"}
	return &feishuAPIStub{
		driveObjects: map[string]Object{
			"folder-root":   root,
			"file-a":        file,
			"folder-guides": folder,
		},
		wikiObjects: map[string]Object{
			"space-1:node-root":  wikiRoot,
			"space-1:node-child": wikiChild,
		},
		wikiSpaces: []Object{
			{Kind: ObjectKindWikiSpace, Token: "feishu:wiki:space:space-1", SpaceID: "space-1", Name: "Engineering Wiki", IsContainer: true, HasChildren: true, Revision: "space-rev"},
		},
		driveChildren: map[string][]Object{
			"folder-root": {file, alias, folder},
		},
		wikiChildren: map[string][]Object{
			"space-1:node-root": {wikiChild},
		},
	}
}

func (a *feishuAPIStub) GetDriveRoot(context.Context, string) (Object, error) {
	return a.GetDriveFolder(context.Background(), "", "folder-root")
}

func (a *feishuAPIStub) GetDriveFolder(context.Context, string, string) (Object, error) {
	a.driveFolderCalls++
	return a.driveObjects["folder-root"], nil
}

func (a *feishuAPIStub) ListDriveChildren(_ context.Context, _ string, folderToken, cursor string, pageSize int) (ObjectPage, error) {
	return objectPage(a.driveChildren[driveFolderToken(folderToken)], cursor, pageSize)
}

func (a *feishuAPIStub) DownloadDriveFile(_ context.Context, _ string, fileToken, expectedVersion string) (ExportedContent, error) {
	a.downloadCalls++
	object := a.driveObjects[fileToken]
	if versionFor(object) != expectedVersion {
		return ExportedContent{}, connector.NewError(connector.ErrorCodeVersionMismatch, "version mismatch")
	}
	return ExportedContent{Reader: strings.NewReader("drive:" + fileToken), MimeType: object.MimeType, FileExtension: object.FileExtension, SizeBytes: object.SizeBytes, ExportedVersion: expectedVersion}, nil
}

func (a *feishuAPIStub) ExportDriveDocumentMarkdown(_ context.Context, _ string, docToken, expectedVersion string) (ExportedContent, error) {
	a.driveExportCalls++
	object := a.driveObjects[docToken]
	if versionFor(object) != expectedVersion {
		return ExportedContent{}, connector.NewError(connector.ErrorCodeVersionMismatch, "version mismatch")
	}
	return ExportedContent{Content: []byte("drive-doc:" + docToken), MimeType: "text/markdown", FileExtension: ".md", SizeBytes: 16, ExportedVersion: expectedVersion}, nil
}

func (a *feishuAPIStub) ListWikiSpaces(_ context.Context, _ string, cursor string, pageSize int) (ObjectPage, error) {
	return objectPage(a.wikiSpaces, cursor, pageSize)
}

func (a *feishuAPIStub) GetWikiNode(_ context.Context, _ string, spaceID, nodeToken string) (Object, error) {
	return a.wikiObjects[spaceID+":"+nodeToken], nil
}

func (a *feishuAPIStub) ListWikiChildren(_ context.Context, _ string, spaceID, nodeToken, cursor string, pageSize int) (ObjectPage, error) {
	return objectPage(a.wikiChildren[spaceID+":"+nodeToken], cursor, pageSize)
}

func (a *feishuAPIStub) ExportWikiNodeMarkdown(_ context.Context, _ string, spaceID, nodeToken, expectedVersion string) (ExportedContent, error) {
	object := a.wikiObjects[spaceID+":"+nodeToken]
	if versionFor(object) != expectedVersion {
		return ExportedContent{}, connector.NewError(connector.ErrorCodeVersionMismatch, "version mismatch")
	}
	return ExportedContent{Content: []byte("wiki:" + nodeToken), MimeType: "text/markdown", FileExtension: ".md", SizeBytes: 9, ExportedVersion: expectedVersion}, nil
}

type feishuTempStoreStub struct {
	objects map[string]string
}

func (s *feishuTempStoreStub) Put(_ context.Context, input worker.TempObjectInput) (worker.TempObject, error) {
	if s.objects == nil {
		s.objects = map[string]string{}
	}
	content, err := io.ReadAll(input.Reader)
	if err != nil {
		return worker.TempObject{}, err
	}
	token := "feishu-" + strconv.Itoa(len(s.objects)+1)
	s.objects[token] = string(content)
	return worker.TempObject{URI: "scan-temp://" + token, CleanupToken: token, SizeBytes: int64(len(content))}, nil
}

func objectPage(items []Object, cursor string, pageSize int) (ObjectPage, error) {
	offset, _ := parseCursor(cursor)
	if offset >= len(items) {
		return ObjectPage{}, nil
	}
	end := offset + pageSize
	if end > len(items) {
		end = len(items)
	}
	page := ObjectPage{Items: items[offset:end], Watermark: "watermark-1"}
	if end < len(items) {
		page.HasMore = true
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}

func feishuObjectKeys(items []connector.RawObject) []string {
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

func assertFeishuErrorCode(t *testing.T, err error, code connector.ErrorCode) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error code %s, got nil", code)
	}
	got, ok := connector.ErrorCodeOf(err)
	if !ok || got != code {
		t.Fatalf("expected error code %s, got %v (ok=%v, err=%v)", code, got, ok, err)
	}
}
