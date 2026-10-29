package academic

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestZoteroPreviewOmitsOnlyVerifiedTrashedCollectionMemberships(t *testing.T) {
	const collections = `[{"key":"ACTIVE01","data":{"key":"ACTIVE01","name":"Active","parentCollection":false}}]`
	const items = `[{"key":"PAPER001","data":{"key":"PAPER001","itemType":"journalArticle","title":"Keep metadata","collections":["ACTIVE01","TRASH001","TRASH002"],"tags":[{"tag":"keep"}]}},{"key":"ATTACH01","data":{"key":"ATTACH01","itemType":"attachment","parentItem":"PAPER001","collections":["TRASH001"]}},{"key":"PAPER002","data":{"key":"PAPER002","itemType":"book","title":"Unchanged","collections":["ACTIVE01"]}}]`
	lookups := map[string]int{}
	client := zoteroTestClient(t, zoteroConfig{Source: "local"}, func(r *http.Request) (*http.Response, error) {
		switch r.URL.Path {
		case "/api/users/0/collections":
			return zoteroResponse(r, 200, collections), nil
		case "/api/users/0/items":
			return zoteroResponse(r, 200, items), nil
		case "/api/users/0/collections/TRASH001", "/api/users/0/collections/TRASH002":
			key := strings.TrimPrefix(r.URL.Path, "/api/users/0/collections/")
			lookups[key]++
			deleted := "true"
			if key == "TRASH002" {
				deleted = "1"
			}
			return zoteroResponse(r, 200, `{"key":"`+key+`","data":{"key":"`+key+`","name":"Trashed","deleted":`+deleted+`}}`), nil
		default:
			t.Fatalf("unexpected endpoint %s", r.URL)
			return nil, nil
		}
	})
	preview, err := client.preview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var paper, attachment map[string]any
	_ = json.Unmarshal(preview.Items[0], &paper)
	_ = json.Unmarshal(preview.Items[1], &attachment)
	paperData := paper["data"].(map[string]any)
	attachmentData := attachment["data"].(map[string]any)
	if members := paperData["collections"].([]any); len(members) != 1 || members[0] != "ACTIVE01" {
		t.Fatalf("trashed memberships retained or active membership lost: %v", members)
	}
	if len(attachmentData["collections"].([]any)) != 0 || attachmentData["parentItem"] != "PAPER001" || paperData["title"] != "Keep metadata" || len(paperData["tags"].([]any)) != 1 {
		t.Fatal("normalization changed document metadata or retained deleted memberships")
	}
	var originalItems, originalCollections []json.RawMessage
	_ = json.Unmarshal([]byte(items), &originalItems)
	_ = json.Unmarshal([]byte(collections), &originalCollections)
	if string(preview.Items[2]) != string(originalItems[2]) || string(preview.Collections[0]) != string(originalCollections[0]) || len(preview.Items) != 3 || len(preview.Collections) != 1 {
		t.Fatal("unrelated items or active collections changed")
	}
	if lookups["TRASH001"] != 1 || lookups["TRASH002"] != 1 || !strings.Contains(strings.Join(preview.Warnings, "\n"), "2 个已删除文件夹的 3 条关联") {
		t.Fatalf("missing verified deletion warning or repeated lookup: %v %v", lookups, preview.Warnings)
	}
}

func TestZoteroPreviewRejectsUnverifiedMissingCollections(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		err     error
		version string
		code    string
	}{
		{"active collection", 200, `{"key":"MISSING1","data":{"key":"MISSING1","name":"Active","deleted":false}}`, nil, "9", "INVALID_ZOTERO_RESPONSE"},
		{"zero deleted", 200, `{"key":"MISSING1","data":{"key":"MISSING1","name":"Active","deleted":0}}`, nil, "9", "INVALID_ZOTERO_RESPONSE"},
		{"string deleted", 200, `{"key":"MISSING1","data":{"key":"MISSING1","name":"Unknown","deleted":"true"}}`, nil, "9", "INVALID_ZOTERO_RESPONSE"},
		{"wrong collection", 200, `{"key":"OTHER001","data":{"key":"OTHER001","deleted":true}}`, nil, "9", "INVALID_ZOTERO_RESPONSE"},
		{"invalid JSON", 200, `{`, nil, "9", "INVALID_ZOTERO_RESPONSE"},
		{"no record", 404, `not found`, nil, "9", "ZOTERO_NOT_FOUND"},
		{"provider failure", 503, `unavailable`, nil, "9", "ZOTERO_PROVIDER_FAILED"},
		{"connection failure", 0, "", errors.New("connection failed"), "9", "ZOTERO_CONNECTION_FAILED"},
		{"changed library", 200, `{"key":"MISSING1","data":{"key":"MISSING1","deleted":true}}`, nil, "10", "ZOTERO_SOURCE_CHANGED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := zoteroTestClient(t, zoteroConfig{Source: "local"}, func(r *http.Request) (*http.Response, error) {
				switch r.URL.Path {
				case "/api/users/0/collections":
					return zoteroResponse(r, 200, `[]`), nil
				case "/api/users/0/items":
					return zoteroResponse(r, 200, `[{"key":"PAPER001","data":{"itemType":"journalArticle","collections":["MISSING1"]}}]`), nil
				default:
					if tc.err != nil {
						return nil, tc.err
					}
					response := zoteroResponse(r, tc.status, tc.body)
					response.Header.Set("Last-Modified-Version", tc.version)
					return response, nil
				}
			})
			_, err := client.preview(context.Background())
			failure, ok := err.(*zoteroError)
			if !ok || failure.Code != tc.code {
				t.Fatalf("missing collection error hidden or misclassified: %#v", err)
			}
		})
	}
}
