package notion

import (
	"fmt"
	"strings"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

func (c *NotionConnector) rawObject(authConnectionID string, object Object) connector.RawObject {
	raw := connector.RawObject{
		ObjectRef:         object.ID,
		ObjectKey:         objectKeyFor(object),
		ParentRef:         object.ParentID,
		ParentKey:         parentKeyFor(object),
		DisplayName:       displayName(object.Name, object.ID),
		SearchName:        strings.ToLower(displayName(object.Name, object.ID)),
		ObjectType:        objectType(object),
		IsDocument:        true,
		IsContainer:       object.HasChildren || object.Kind == ObjectKindDatabase,
		HasChildren:       object.HasChildren || object.Kind == ObjectKindDatabase,
		Bindable:          true,
		BindingTargetType: bindingTargetType(object),
		BindingTargetRef:  object.ID,
		SourceVersion:     versionFor(object),
		MimeType:          "text/markdown",
		FileExtension:     ".md",
		ProviderMeta:      providerMeta(authConnectionID, object),
	}
	raw.TreeKey = raw.ObjectKey
	if object.ModifiedUnixSec > 0 {
		modifiedAt := time.Unix(object.ModifiedUnixSec, 0)
		raw.ModifiedAt = &modifiedAt
	}
	return raw
}

func (c *NotionConnector) rawObjectPage(authConnectionID string, page ObjectPage, complete bool) connector.RawObjectPage {
	items := make([]connector.RawObject, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, c.rawObject(authConnectionID, item))
	}
	return connector.RawObjectPage{
		Items:        dedupeRawObjects(items),
		HasMore:      page.HasMore,
		NextCursor:   page.NextCursor,
		Watermark:    page.Watermark,
		ListComplete: complete,
	}
}

func (c *NotionConnector) buildObjectKey(raw connector.RawObject) (string, error) {
	if raw.ObjectKey != "" {
		return raw.ObjectKey, nil
	}
	kind := ObjectKind(raw.ProviderMeta["kind"])
	objectID := firstNonEmpty(raw.ProviderMeta["id"], raw.ProviderMeta["page_id"], raw.ProviderMeta["database_id"], raw.ObjectRef)
	objectID = normalizeNotionID(objectID)
	if objectID == "" {
		return "", connector.NewError(connector.ErrorCodeInvalidArgument, "notion object id is required")
	}
	switch kind {
	case ObjectKindDatabase:
		return databaseObjectKey(objectID), nil
	case ObjectKindPage, "":
		return pageObjectKey(objectID), nil
	default:
		return "", connector.NewError(connector.ErrorCodeInvalidArgument, "provider object kind is unsupported")
	}
}

func (c *NotionConnector) buildParentKey(raw connector.RawObject) string {
	if raw.ParentKey != "" {
		return raw.ParentKey
	}
	parentID := normalizeNotionID(firstNonEmpty(raw.ProviderMeta["parent_id"], raw.ParentRef))
	if parentID == "" {
		return ""
	}
	if ObjectKind(raw.ProviderMeta["parent_kind"]) == ObjectKindDatabase {
		return databaseObjectKey(parentID)
	}
	return pageObjectKey(parentID)
}

func objectType(object Object) connector.ObjectType {
	if object.Kind == ObjectKindDatabase {
		return connector.ObjectTypeFolder
	}
	return connector.ObjectTypePage
}

func bindingTargetType(object Object) connector.TargetType {
	if object.Kind == ObjectKindDatabase {
		return TargetTypeDatabase
	}
	return TargetTypePage
}

func versionFor(object Object) string {
	if strings.TrimSpace(object.LastEditedTime) != "" {
		return strings.TrimSpace(object.LastEditedTime)
	}
	if object.ModifiedUnixSec > 0 {
		return fmt.Sprintf("%d", object.ModifiedUnixSec)
	}
	return objectKeyFor(object)
}

func providerMeta(authConnectionID string, object Object) connector.ProviderMeta {
	meta := connector.ProviderMeta{
		"auth_connection_id": authConnectionID,
		"kind":               string(object.Kind),
		"id":                 object.ID,
	}
	if object.Kind == ObjectKindDatabase {
		meta["database_id"] = object.ID
	} else {
		meta["page_id"] = object.ID
	}
	if object.ParentID != "" {
		meta["parent_id"] = object.ParentID
	}
	if object.ParentKind != "" {
		meta["parent_kind"] = string(object.ParentKind)
	}
	if object.URL != "" {
		meta["url"] = object.URL
	}
	if object.LastEditedTime != "" {
		meta["last_edited_time"] = object.LastEditedTime
	}
	return meta
}

func objectKeyFor(object Object) string {
	if object.Kind == ObjectKindDatabase {
		return databaseObjectKey(object.ID)
	}
	return pageObjectKey(object.ID)
}

func parentKeyFor(object Object) string {
	parentID := normalizeNotionID(object.ParentID)
	if parentID == "" {
		return ""
	}
	if object.ParentKind == ObjectKindDatabase {
		return databaseObjectKey(parentID)
	}
	return pageObjectKey(parentID)
}

func pageObjectKey(pageID string) string {
	return fmt.Sprintf("%s:page:%s", ConnectorType, normalizeNotionID(pageID))
}

func databaseObjectKey(databaseID string) string {
	return fmt.Sprintf("%s:database:%s", ConnectorType, normalizeNotionID(databaseID))
}

func dedupeRawObjects(items []connector.RawObject) []connector.RawObject {
	seen := make(map[string]struct{}, len(items))
	out := make([]connector.RawObject, 0, len(items))
	for _, item := range items {
		key := item.ObjectKey
		if key == "" {
			key = item.ObjectRef
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, item)
	}
	return out
}
