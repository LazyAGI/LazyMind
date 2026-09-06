package feishu

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRealD3ReadOnlyInventory(t *testing.T) {
	if os.Getenv("FEISHU_REAL_D3_PROBE") != "1" {
		t.Skip("real D3 probe is disabled")
	}
	connectionID := strings.TrimSpace(os.Getenv("FEISHU_REAL_CONNECTION_ID"))
	ownerID := strings.TrimSpace(os.Getenv("FEISHU_REAL_OWNER_ID"))
	internalTokenPath := strings.TrimSpace(os.Getenv("FEISHU_REAL_INTERNAL_TOKEN_FILE"))
	internalToken := []byte(strings.TrimSpace(os.Getenv("FEISHU_REAL_INTERNAL_TOKEN")))
	if connectionID == "" || ownerID == "" || (internalTokenPath == "" && len(internalToken) == 0) {
		t.Fatal("real D3 probe configuration is incomplete")
	}
	if len(internalToken) == 0 {
		var err error
		internalToken, err = os.ReadFile(internalTokenPath)
		if err != nil {
			t.Fatal("read internal token file failed")
		}
	}
	authServiceBaseURL := strings.TrimSpace(os.Getenv("FEISHU_REAL_AUTH_SERVICE_BASE_URL"))
	if authServiceBaseURL == "" {
		authServiceBaseURL = "http://auth-service:8000"
	}
	coreBaseURL := strings.TrimSpace(os.Getenv("FEISHU_REAL_CORE_BASE_URL"))
	if coreBaseURL == "" {
		coreBaseURL = "http://core:8000"
	}
	feishuBaseURL := strings.TrimSpace(os.Getenv("FEISHU_REAL_API_BASE_URL"))
	if feishuBaseURL == "" {
		feishuBaseURL = "https://open.feishu.cn/open-apis"
	}
	resolver, err := NewHTTPAuthConnectionClient(authServiceBaseURL, strings.TrimSpace(string(internalToken)), nil)
	internalToken = nil
	if err != nil {
		t.Fatal("create token resolver failed")
	}
	if err := resolver.UseCoreTokenBridge(coreBaseURL); err != nil {
		t.Fatal("configure Core Token Bridge failed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	token, err := resolver.GetToken(ctx, TokenRequest{
		AuthConnectionID:   connectionID,
		UserID:             ownerID,
		ContextMode:        TokenContextModePreBindingBrowse,
		Consumer:           "datasource",
		RequiredCapability: "datasource.browse",
	})
	if err != nil {
		t.Fatal("resolve managed user token failed")
	}
	if token.Provider != "feishu" || token.SubjectType != "user" || token.Status != "ACTIVE" || token.TokenVersion < 1 {
		t.Fatal("managed Feishu token contract is incomplete")
	}

	api, err := NewDefaultFeishuAPIClient(feishuBaseURL, nil)
	if err != nil {
		t.Fatal("create Feishu API client failed")
	}
	root, err := api.GetDriveRoot(ctx, token.AccessToken)
	if err != nil || strings.TrimSpace(root.Token) == "" {
		t.Fatal("read Feishu drive root failed")
	}
	driveObjects := make([]Object, 0, 100)
	driveFolders := []string{root.Token}
	visitedDriveFolders := map[string]struct{}{}
	for len(driveFolders) > 0 && len(driveObjects) < 100 {
		folderToken := driveFolders[0]
		driveFolders = driveFolders[1:]
		if _, seen := visitedDriveFolders[folderToken]; seen {
			continue
		}
		visitedDriveFolders[folderToken] = struct{}{}
		page, listErr := api.ListDriveChildren(ctx, token.AccessToken, folderToken, "", 100)
		if listErr != nil {
			t.Fatal("list Feishu drive children failed")
		}
		for _, item := range page.Items {
			driveObjects = append(driveObjects, item)
			if item.IsContainer && strings.TrimSpace(item.Token) != "" {
				driveFolders = append(driveFolders, item.Token)
			}
		}
	}
	wikiSpaces, err := api.ListWikiSpaces(ctx, token.AccessToken, "", 50)
	if err != nil {
		t.Fatal("list Feishu wiki spaces failed")
	}

	wikiNodes := make([]Object, 0, 100)
	type wikiParent struct {
		spaceID   string
		nodeToken string
	}
	wikiParents := make([]wikiParent, 0, len(wikiSpaces.Items))
	for _, space := range wikiSpaces.Items {
		spaceID := firstNonEmpty(space.SpaceID, wikiSpaceIDFromRef(space.Token))
		if spaceID == "" {
			continue
		}
		wikiParents = append(wikiParents, wikiParent{spaceID: spaceID})
	}
	visitedWikiParents := map[string]struct{}{}
	for len(wikiParents) > 0 && len(wikiNodes) < 100 {
		parent := wikiParents[0]
		wikiParents = wikiParents[1:]
		key := parent.spaceID + ":" + parent.nodeToken
		if _, seen := visitedWikiParents[key]; seen {
			continue
		}
		visitedWikiParents[key] = struct{}{}
		nodes, listErr := api.ListWikiChildren(ctx, token.AccessToken, parent.spaceID, parent.nodeToken, "", 50)
		if listErr != nil {
			t.Fatal("list Feishu wiki nodes failed")
		}
		for _, node := range nodes.Items {
			wikiNodes = append(wikiNodes, node)
			if node.HasChildren && strings.TrimSpace(node.Token) != "" {
				wikiParents = append(wikiParents, wikiParent{spaceID: parent.spaceID, nodeToken: node.Token})
			}
		}
	}

	docRead := false
	fileDownloaded := false
	objectTypeCounts := map[string]int{}
	for _, item := range driveObjects {
		objectToken := firstNonEmpty(item.StableID, item.Token)
		objectType := normalizedFeishuObjectType(firstNonEmpty(item.DriveType, item.FileExtension))
		objectTypeCounts[objectType]++
		switch objectType {
		case "doc", "docx":
			if docRead || objectToken == "" {
				continue
			}
			content, readErr := api.ExportDriveDocumentMarkdown(ctx, token.AccessToken, objectToken, item.Revision)
			if readErr == nil && len(content.Content) > 0 {
				docRead = true
			}
		case "file":
			if fileDownloaded || objectToken == "" {
				continue
			}
			content, downloadErr := api.DownloadDriveFile(ctx, token.AccessToken, objectToken, item.Revision)
			if downloadErr == nil && len(content.Content) > 0 {
				fileDownloaded = true
			}
		}
	}
	for _, node := range wikiNodes {
		if (docRead && fileDownloaded) || !node.IsDocument || strings.TrimSpace(node.Token) == "" {
			continue
		}
		exported, exportErr := api.ExportWikiNodeMarkdown(ctx, token.AccessToken, node.SpaceID, node.Token, node.Revision)
		if exportErr != nil || len(exported.Content) == 0 {
			continue
		}
		if exported.FileExtension == ".md" || strings.Contains(exported.MimeType, "markdown") {
			docRead = true
		} else {
			fileDownloaded = true
		}
	}

	t.Logf("subject_user=true drive_root=true drive_items=%d drive_types=%v wiki_spaces=%d wiki_nodes=%d doc_read=%t file_downloaded=%t",
		len(driveObjects), objectTypeCounts, len(wikiSpaces.Items), len(wikiNodes), docRead, fileDownloaded)
}
