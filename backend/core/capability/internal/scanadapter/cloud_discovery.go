package scanadapter

import (
	"context"
	"net/url"
	"strings"

	"lazymind/core/capability"
)

type runtimeDiscoveryPage struct {
	SourceID string `json:"source_id"`
	Provider string `json:"provider"`
	Items    []struct {
		DocumentID  string   `json:"document_id"`
		Title       string   `json:"title"`
		Type        string   `json:"type"`
		SourceURL   string   `json:"source_url"`
		MIMEType    string   `json:"mime_type"`
		ParentIDs   []string `json:"parent_ids"`
		ReadLocator string   `json:"read_locator"`
		FolderID    string   `json:"folder_id"`
	} `json:"items"`
	NextPageToken string   `json:"next_page_token"`
	Incomplete    bool     `json:"incomplete"`
	Warnings      []string `json:"warnings"`
}

func (a *CloudDocumentReader) googlePage(ctx context.Context, call capability.InvocationContext, op, operation string, account cloudAccount, nodeRef, targetType, targetRef, query, queryMode, cursor string, pageSize int) (runtimeDiscoveryPage, error) {
	var result runtimeDiscoveryPage
	if nodeRef != "" && targetRef != "" && nodeRef != targetRef {
		return result, capability.NewError(capability.InvalidArgument, op, "node_ref and target_ref must identify the same scope", false, nil)
	}
	if nodeRef == "" {
		nodeRef = targetRef
	}
	folder, drive := "", ""
	switch targetType {
	case "", "folder":
		folder = nodeRef
	case "drive":
		drive = nodeRef
	default:
		return result, capability.NewError(capability.InvalidArgument, op, "Google Drive target_type must be folder or drive", false, nil)
	}
	if queryMode == "" {
		queryMode = "name"
	}
	err := a.runtimeRequest(ctx, call, op, operation, account, map[string]any{
		"folder_id": folder, "drive_id": drive, "query": query, "query_mode": queryMode,
		"page_size": pageSize, "page_token": cursor,
	}, &result)
	if err == nil && (result.SourceID != account.ConnectionID || result.Provider != account.Provider || result.Items == nil) {
		err = capability.NewError(capability.Unavailable, op, "invalid discovery identity in runtime response", false, nil)
	}
	return result, err
}

func (a *CloudDocumentReader) browseGoogleDrive(ctx context.Context, call capability.InvocationContext, account cloudAccount, input capability.GetCloudDocumentInput) (capability.GetCloudDocumentResult, error) {
	page, err := a.googlePage(ctx, call, "cloud_document.get", "browse", account, input.NodeRef, input.TargetType, input.TargetRef, "", "name", input.ProviderCursor, input.DocumentsPage.PageSize)
	if err != nil {
		return capability.GetCloudDocumentResult{}, err
	}
	return capability.GetCloudDocumentResult{
		Source: accountSource(account), Documents: googleDocuments(account, page),
		DocumentsPage: &capability.CursorPageInfo{ProviderCursor: page.NextPageToken},
		Incomplete:    page.Incomplete, Warnings: page.Warnings,
	}, nil
}

func (a *CloudDocumentReader) searchGoogleDrive(ctx context.Context, call capability.InvocationContext, account cloudAccount, input capability.SearchCloudDocumentsInput) (capability.SearchCloudDocumentsResult, error) {
	page, err := a.googlePage(ctx, call, "cloud_document.search", "search", account, input.NodeRef, input.TargetType, input.TargetRef, input.Query, input.QueryMode, input.ProviderCursor, input.Page.PageSize)
	if err != nil {
		return capability.SearchCloudDocumentsResult{}, err
	}
	hits := make([]capability.CloudDocumentSearchHit, 0, len(page.Items))
	for _, item := range googleDocuments(account, page) {
		if (input.IncludeDocuments || input.IncludeContainers) && !(item.IsDocument && input.IncludeDocuments || item.IsContainer && input.IncludeContainers) {
			continue
		}
		hits = append(hits, capability.CloudDocumentSearchHit{
			Key: item.ID, SourceID: item.SourceID, Provider: item.Provider, DisplayName: item.DisplayName,
			NodeRef: item.NodeRef, TargetType: item.TargetType, TargetRef: item.TargetRef,
			ObjectKey: item.ObjectKey, ParentKey: item.ParentKey, FileType: item.FileType,
			ReadLocator: item.ReadLocator, SourceURL: item.SourceURL,
			IsDocument: item.IsDocument, IsContainer: item.IsContainer, HasChildren: item.HasChildren, Selectable: item.Selectable,
		})
	}
	return capability.SearchCloudDocumentsResult{Hits: hits, Page: capability.CursorPageInfo{ProviderCursor: page.NextPageToken}, Incomplete: page.Incomplete, Warnings: page.Warnings}, nil
}

func googleDocuments(account cloudAccount, page runtimeDiscoveryPage) []capability.CloudDocumentMetadata {
	items := make([]capability.CloudDocumentMetadata, 0, len(page.Items))
	for _, entry := range page.Items {
		directory := entry.Type == "directory"
		item := capability.CloudDocumentMetadata{
			ID: entry.DocumentID, SourceID: account.ConnectionID, Provider: account.Provider,
			ObjectKey: entry.DocumentID, DisplayName: entry.Title, FileType: entry.MIMEType,
			ReadLocator: entry.ReadLocator, SourceURL: safeSourceURL(entry.SourceURL),
			IsDocument: !directory, IsContainer: directory, HasChildren: directory, Selectable: true,
		}
		if directory {
			item.NodeRef, item.TargetRef, item.TargetType = entry.FolderID, entry.FolderID, "folder"
		}
		if len(entry.ParentIDs) > 0 {
			item.ParentKey = entry.ParentIDs[0]
		}
		items = append(items, item)
	}
	return items
}

func safeSourceURL(raw string) string {
	u, err := url.Parse(raw)
	if err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil {
		return raw
	}
	return ""
}

func documentLocation(provider string, item treeNode) (string, string) {
	value := func(key string) string { v, _ := item.ProviderMeta[key].(string); return strings.TrimSpace(v) }
	sourceURL := safeSourceURL(value("url"))
	if !item.IsDocument {
		return "", sourceURL
	}
	switch provider {
	case "notion":
		kind, id := value("kind"), value("id")
		if id == "" {
			id = item.TargetRef
		}
		if id != "" && kind == "page" {
			return "notion:/~page/" + id, sourceURL
		}
		// Writer's Notion reader loads pages, not database query results.
	case "feishu":
		token := value("token")
		if token != "" && value("kind") == "wiki_node" {
			return "feishu:/~node/" + token, sourceURL
		}
		kind := value("file_type")
		if kind == "shortcut" {
			kind, token = value("shortcut_target_type"), value("shortcut_target_token")
		}
		if token != "" && (kind == "docx" || kind == "doc") {
			return "feishu:/~" + kind + "/" + token, sourceURL
		}
	}
	return "", sourceURL
}
