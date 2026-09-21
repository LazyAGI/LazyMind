package workflow

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPublicationStatusTargetURL(t *testing.T) {
	for _, tt := range []struct{ name, provider, target, want string }{
		{"feishu", "feishu", `{"uri":"https://example.feishu.cn/docx/fixture"}`, "https://example.feishu.cn/docx/fixture"},
		{"feishu-wiki", "feishu", `{"uri":"https://example.feishu.cn/wiki/fixture"}`, "https://example.feishu.cn/wiki/fixture"},
		{"feishu-internal-docx", "feishu", `{"uri":"feishu:/~docx/FixtureDocumentToken","doc_id":"FixtureDocumentToken"}`, "https://feishu.cn/docx/FixtureDocumentToken"},
		{"feishu-internal-doc", "feishu", `{"uri":"feishu:/~doc/FixtureLegacyToken","doc_id":"FixtureLegacyToken"}`, "https://feishu.cn/docs/FixtureLegacyToken"},
		{"feishu-internal-wiki", "feishu", `{"uri":"feishu@FixtureSpace:/~node/FixtureWikiToken","doc_id":"FixtureDocumentToken"}`, "https://feishu.cn/wiki/FixtureWikiToken"},
		{"feishu-id-only", "feishu", `{"doc_id":"FixtureDocumentToken"}`, "https://feishu.cn/docx/FixtureDocumentToken"},
		{"notion-internal-page", "notion", `{"uri":"notion:/~page/12345678-1234-1234-1234-123456789abc","doc_id":"12345678-1234-1234-1234-123456789abc"}`, "https://www.notion.so/12345678123412341234123456789abc"},
		{"notion", "notion", `{"uri":"notion:/~page/fixture","meta":{"browser_url":"https://www.notion.so/fixture-document"}}`, "https://www.notion.so/fixture-document"},
		{"github-wiki", "github", `{"uri":"githubwiki://example/docs/Note","meta":{"browser_url":"https://github.com/example/docs/wiki/Note"}}`, "https://github.com/example/docs/wiki/Note"},
		{"github", "github", `{"uri":"github://repo/note.md","meta":{"browser_url":"https://github.com/example/docs/blob/main/note.md"}}`, "https://github.com/example/docs/blob/main/note.md"},
		{"github-pr", "github", `{"uri":"github://repo/note.md","meta":{"pull_request_url":"https://github.com/example/docs/pull/7","browser_url":"https://github.com/example/docs/blob/main/note.md"}}`, "https://github.com/example/docs/pull/7"},
		{"wechat", "wechat", `{"uri":"wechat://draft/fixture","meta":{"browser_url":"https://mp.weixin.qq.com/"}}`, ""},
		{"obsidian", "obsidian", `{"uri":"obsidian://vlt_fixture/note.md","meta":{"local_path":"/Users/test/My Vault/中文 #1.md"}}`, "obsidian://open?path=%2FUsers%2Ftest%2FMy%20Vault%2F%E4%B8%AD%E6%96%87%20%231.md"},
		{"obsidian-windows", "obsidian", `{"meta":{"local_path":"C:\\Notes\\中文.md"}}`, "obsidian://open?path=C%3A%5CNotes%5C%E4%B8%AD%E6%96%87.md"},
		{"obsidian-internal", "obsidian", `{"uri":"obsidian://vlt_fixture/note.md"}`, ""},
		{"unsafe", "github", `{"uri":"javascript:alert(1)"}`, ""},
		{"credentials", "github", `{"meta":{"browser_url":"https://user:password@github.com/example/docs"}}`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for _, receipt := range []bool{false, true} {
				op := &DocumentPublicationOperation{Provider: tt.provider, TargetDocument: json.RawMessage(tt.target)}
				if receipt {
					op.TargetDocument = json.RawMessage(`{"uri":"https://example.test/stale"}`)
					op.ReceiptJSON, _ = json.Marshal(DocumentPublicationReceipt{TargetDocument: json.RawMessage(tt.target)})
				}
				if got := publicationStatus(op, time.Now()).TargetURL; got != tt.want {
					t.Errorf("receipt=%v: got %q, want %q", receipt, got, tt.want)
				}
			}
		})
	}
}
