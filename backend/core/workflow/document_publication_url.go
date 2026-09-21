package workflow

import (
	"context"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"time"

	"lazymind/core/algo"
	"lazymind/core/modelconfig"
)

var feishuInternalTarget = regexp.MustCompile(`^feishu(?:@[^:/]+)?:/{1,2}~(docx|doc|node)/([A-Za-z0-9]+)/?$`)
var feishuDocumentToken = regexp.MustCompile(`^[A-Za-z0-9]+$`)
var notionPageID = regexp.MustCompile(`^(?:[A-Fa-f0-9]{32}|[A-Fa-f0-9]{8}(?:-[A-Fa-f0-9]{4}){3}-[A-Fa-f0-9]{12})$`)

func writerInternalTargetURL(provider, uri, documentID string) string {
	switch provider {
	case "feishu":
		if parts := feishuInternalTarget.FindStringSubmatch(uri); parts != nil {
			kind := map[string]string{"docx": "docx", "doc": "docs", "node": "wiki"}[parts[1]]
			return "https://feishu.cn/" + kind + "/" + parts[2]
		}
		// The provider itself resolves ID-only targets as /~docx/<document_id>.
		if uri == "" && feishuDocumentToken.MatchString(documentID) {
			return "https://feishu.cn/docx/" + documentID
		}
	case "notion":
		page := documentID
		if strings.HasPrefix(uri, "notion:/~page/") {
			page = strings.TrimPrefix(uri, "notion:/~page/")
		} else if uri != "" {
			return ""
		}
		if notionPageID.MatchString(page) {
			return "https://www.notion.so/" + strings.ReplaceAll(page, "-", "")
		}
	}
	return ""
}

func absoluteNotePath(path string) bool {
	absolute := strings.HasPrefix(path, "/") || strings.HasPrefix(path, `\\`) ||
		(len(path) >= 3 && ((path[0] >= 'A' && path[0] <= 'Z') || (path[0] >= 'a' && path[0] <= 'z')) && path[1] == ':' && (path[2] == '/' || path[2] == '\\'))
	return absolute && !strings.ContainsFunc(path, func(r rune) bool { return r < 32 || r == 127 })
}

func obsidianOpenURL(path string) string {
	if !absoluteNotePath(path) {
		return ""
	}
	return "obsidian://open?path=" + strings.ReplaceAll(url.QueryEscape(path), "+", "%20")
}

// Provider locators identify a resource internally; only browser URLs and the
// read-only Obsidian open action can be used by the document footer.
func documentPublicationTargetURL(provider string, target json.RawMessage) string {
	var locator struct {
		URI        string `json:"uri"`
		DocumentID string `json:"doc_id"`
		Meta       struct {
			PullRequestURL string `json:"pull_request_url"`
			BrowserURL     string `json:"browser_url"`
			LocalPath      string `json:"local_path"`
		} `json:"meta"`
	}
	if json.Unmarshal(target, &locator) != nil {
		return ""
	}
	for _, candidate := range []string{locator.Meta.PullRequestURL, locator.Meta.BrowserURL, locator.URI} {
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.User != nil || parsed.Host == "" {
			continue
		}
		if parsed.Scheme == "https" || parsed.Scheme == "http" {
			if provider == "wechat" && !wechatDraftURL(candidate) {
				continue
			}
			return candidate
		}
		query, err := url.ParseQuery(parsed.RawQuery)
		if err == nil && parsed.Scheme == "obsidian" && parsed.Host == "open" && parsed.Path == "" && parsed.Fragment == "" &&
			len(query) == 1 && len(query["path"]) == 1 && absoluteNotePath(query.Get("path")) {
			return candidate
		}
	}
	if provider == "obsidian" {
		return obsidianOpenURL(locator.Meta.LocalPath)
	}
	return writerInternalTargetURL(provider, locator.URI, locator.DocumentID)
}

func wechatDraftURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host == "mp.weixin.qq.com" && parsed.User == nil &&
		(parsed.Path == "/s" || strings.HasPrefix(parsed.Path, "/s/"))
}

// WeChat draft URLs expire. Resolve the selected article on a read, including
// old receipts that only stored the console URL; never repeat the draft write.
func publicationStatusForRead(ctx context.Context, op *DocumentPublicationOperation) DocumentPublicationStatus {
	status := publicationStatus(op, time.Now())
	if op.Provider != "wechat" || len(op.ReceiptJSON) == 0 {
		return status
	}
	status.TargetURL = ""
	var receipt DocumentPublicationReceipt
	if json.Unmarshal(op.ReceiptJSON, &receipt) != nil {
		return status
	}
	var target struct {
		DocumentID string `json:"doc_id"`
		Meta       struct {
			ArticleIndex int `json:"article_index"`
		} `json:"meta"`
	}
	if json.Unmarshal(receipt.TargetDocument, &target) != nil || strings.TrimSpace(target.DocumentID) == "" || target.Meta.ArticleIndex < 0 {
		return status
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	config, err := modelconfig.LoadWriterProviderToolConfig(ctx, "wechat", op.OwnerUserID)
	if err != nil {
		return status
	}
	response, _, err := algo.InvokeDocumentAction(ctx, algo.DocumentActionInvokeRequest{
		Reference: "builtin:document.wechat_draft_url.v1", Phase: "preview", ToolConfig: config,
		Arguments: map[string]any{"media_id": target.DocumentID, "article_index": target.Meta.ArticleIndex},
	})
	if err != nil {
		return status
	}
	var result struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(response.Result, &result) == nil && wechatDraftURL(result.URL) {
		status.TargetURL = result.URL
	}
	return status
}
