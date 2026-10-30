package academic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// The local API omits trashed collections from /collections, but active items
// can still refer to them. Verify each absent collection individually before
// removing only its membership; an unknown or unavailable collection is an
// error, never permission to silently discard organization data.
func (c *zoteroClient) normalizeTrashedCollections(ctx context.Context, preview *zoteroPreview) error {
	known := make(map[string]bool, len(preview.Collections))
	for _, raw := range preview.Collections {
		var collection struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(raw, &collection); err != nil {
			return invalidZoteroCollectionReference()
		}
		known[collection.Key] = true
	}
	deleted := map[string]bool{}
	removed := 0
	for i, raw := range preview.Items {
		var item struct {
			Data struct {
				Collections []string `json:"collections"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &item); err != nil {
			return invalidZoteroCollectionReference()
		}
		retained := make([]string, 0, len(item.Data.Collections))
		for _, key := range item.Data.Collections {
			if !zoteroItemKey.MatchString(key) {
				return invalidZoteroCollectionReference()
			}
			if known[key] {
				retained = append(retained, key)
				continue
			}
			if !deleted[key] {
				if err := c.verifyTrashedCollection(ctx, key); err != nil {
					return err
				}
				deleted[key] = true
			}
			removed++
		}
		if len(retained) == len(item.Data.Collections) {
			continue
		}
		// Keep every other Zotero field, including provider-specific metadata.
		var envelope, data map[string]json.RawMessage
		if json.Unmarshal(raw, &envelope) != nil || json.Unmarshal(envelope["data"], &data) != nil || data == nil {
			return invalidZoteroCollectionReference()
		}
		data["collections"], _ = json.Marshal(retained)
		envelope["data"], _ = json.Marshal(data)
		preview.Items[i], _ = json.Marshal(envelope)
	}
	if removed > 0 {
		preview.Warnings = append(preview.Warnings, fmt.Sprintf("已忽略 Zotero 中 %d 个已删除文件夹的 %d 条关联；文献及其有效文件夹关系均已保留。", len(deleted), removed))
	}
	return nil
}

func (c *zoteroClient) verifyTrashedCollection(ctx context.Context, key string) error {
	u := c.apiURL("collections/" + key)
	query := u.Query()
	query.Set("format", "json")
	u.RawQuery = query.Encode()
	body, headers, err := c.request(ctx, u, 2<<20)
	if err != nil {
		return err
	}
	if err = c.observeVersion(headers); err != nil {
		return err
	}
	c.metadataBytes += len(body)
	if c.metadataBytes > zoteroMaxMetadataBytes {
		return zoteroFailure(413, "ZOTERO_LIBRARY_TOO_LARGE", "Zotero 题录超过 20 MiB 导入上限，请分批导入")
	}
	var collection struct {
		Key  string `json:"key"`
		Data struct {
			Key     string          `json:"key"`
			Deleted json.RawMessage `json:"deleted"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &collection) != nil || collection.Key != key || collection.Data.Key != key {
		return invalidZoteroCollectionReference()
	}
	deleted := bytes.TrimSpace(collection.Data.Deleted)
	if !bytes.Equal(deleted, []byte("true")) && !bytes.Equal(deleted, []byte("1")) {
		return invalidZoteroCollectionReference()
	}
	return nil
}

func invalidZoteroCollectionReference() *zoteroError {
	return zoteroFailure(502, "INVALID_ZOTERO_RESPONSE", "Zotero 文献引用的文件夹未完整返回，且无法确认它已被删除，请检查 Zotero 文献库后重新预览")
}
