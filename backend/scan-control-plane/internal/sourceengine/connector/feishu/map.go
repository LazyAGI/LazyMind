package feishu

import (
	"fmt"
	"strings"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

func (c *FeishuConnector) rawObject(authConnectionID string, object Object) connector.RawObject {
	mimeType := object.MimeType
	fileExtension := object.FileExtension
	if isMarkdownExportObject(object) {
		mimeType = firstNonEmpty(mimeType, "text/markdown")
		fileExtension = ".md"
	}
	raw := connector.RawObject{
		ObjectRef:         targetRefFor(object),
		ObjectKey:         objectKeyFor(object),
		ParentRef:         parentRefFor(object),
		ParentKey:         parentKeyFor(object),
		DisplayName:       displayName(object.Name, object.Token),
		SearchName:        strings.ToLower(displayName(object.Name, object.Token)),
		ObjectType:        objectType(object),
		IsDocument:        object.IsDocument,
		IsContainer:       object.IsContainer,
		HasChildren:       object.HasChildren,
		Bindable:          isBindableObject(object),
		BindingTargetType: bindingTargetType(object),
		BindingTargetRef:  bindingTargetRef(object),
		SourceVersion:     versionFor(object),
		SizeBytes:         object.SizeBytes,
		MimeType:          mimeType,
		FileExtension:     fileExtension,
		ProviderMeta:      providerMeta(authConnectionID, object),
	}
	raw.TreeKey = raw.ObjectKey
	if object.ModifiedUnixSec > 0 {
		modifiedAt := time.Unix(object.ModifiedUnixSec, 0)
		raw.ModifiedAt = &modifiedAt
	}
	return raw
}

func isMarkdownExportObject(object Object) bool {
	if object.Kind == ObjectKindWikiNode && object.IsDocument {
		return true
	}
	if object.Kind != ObjectKindDriveFile || !object.IsDocument {
		return false
	}
	driveType := strings.ToLower(strings.TrimSpace(object.DriveType))
	return driveType == "doc" || driveType == "docx"
}

func isBindableObject(object Object) bool {
	if object.Kind == ObjectKindVirtualRoot {
		return object.Token == VirtualDriveRootRef
	}
	return object.Kind == ObjectKindDriveFolder || object.Kind == ObjectKindWikiNode
}

func bindingTargetType(object Object) connector.TargetType {
	switch object.Kind {
	case ObjectKindVirtualRoot:
		if object.Token == VirtualDriveRootRef {
			return TargetTypeDriveFolder
		}
	case ObjectKindDriveFolder:
		return TargetTypeDriveFolder
	case ObjectKindWikiNode:
		return TargetTypeWikiNode
	default:
		return ""
	}
	return ""
}

func bindingTargetRef(object Object) string {
	if !isBindableObject(object) {
		return ""
	}
	if object.Kind == ObjectKindVirtualRoot && object.Token == VirtualDriveRootRef {
		return "drive:root"
	}
	return targetRefFor(object)
}

func (c *FeishuConnector) buildObjectKey(raw connector.RawObject) (string, error) {
	if raw.ObjectKey != "" {
		return raw.ObjectKey, nil
	}
	kind := raw.ProviderMeta["kind"]
	switch ObjectKind(kind) {
	case ObjectKindDriveFolder, ObjectKindDriveFile:
		token := firstNonEmpty(raw.ProviderMeta["stable_id"], raw.ProviderMeta["token"], raw.ObjectRef)
		if token == "" {
			return "", connector.NewError(connector.ErrorCodeInvalidArgument, "drive token is required")
		}
		return driveObjectKey(token), nil
	case ObjectKindWikiNode:
		spaceID := raw.ProviderMeta["space_id"]
		nodeToken := firstNonEmpty(raw.ProviderMeta["token"], raw.ObjectRef)
		if spaceID == "" || nodeToken == "" {
			return "", connector.NewError(connector.ErrorCodeInvalidArgument, "wiki space_id and node token are required")
		}
		return wikiObjectKey(spaceID, nodeToken), nil
	default:
		return "", connector.NewError(connector.ErrorCodeInvalidArgument, "provider object kind is required")
	}
}

func (c *FeishuConnector) buildParentKey(raw connector.RawObject) string {
	if raw.ParentKey != "" {
		return raw.ParentKey
	}
	kind := raw.ProviderMeta["kind"]
	switch ObjectKind(kind) {
	case ObjectKindDriveFolder, ObjectKindDriveFile:
		if parent := raw.ProviderMeta["parent_token"]; parent != "" {
			return driveObjectKey(parent)
		}
	case ObjectKindWikiNode:
		if parent := raw.ProviderMeta["parent_token"]; parent != "" {
			return wikiObjectKey(raw.ProviderMeta["space_id"], parent)
		}
	}
	return ""
}

func objectType(object Object) connector.ObjectType {
	if object.Kind == ObjectKindVirtualRoot || object.Kind == ObjectKindWikiSpace {
		return connector.ObjectTypeFolder
	}
	if object.Kind == ObjectKindWikiNode {
		return connector.ObjectTypePage
	}
	if object.IsContainer {
		return connector.ObjectTypeFolder
	}
	return connector.ObjectTypeFile
}

func versionFor(object Object) string {
	if object.Revision != "" {
		return object.Revision
	}
	if object.ModifiedUnixSec > 0 {
		return fmt.Sprintf("%d", object.ModifiedUnixSec)
	}
	switch object.Kind {
	case ObjectKindWikiNode:
		if object.SpaceID != "" && object.Token != "" {
			return fmt.Sprintf("%s:%s", object.SpaceID, object.Token)
		}
	case ObjectKindDriveFolder, ObjectKindDriveFile, ObjectKindVirtualRoot, ObjectKindWikiSpace:
		if token := firstNonEmpty(object.StableID, object.Token); token != "" {
			return token
		}
	}
	return objectKeyFor(object)
}

func providerMeta(authConnectionID string, object Object) connector.ProviderMeta {
	meta := connector.ProviderMeta{
		"auth_connection_id": authConnectionID,
		"kind":               string(object.Kind),
		"token":              object.Token,
	}
	if object.ParentToken != "" {
		meta["parent_token"] = object.ParentToken
	}
	if object.SpaceID != "" {
		meta["space_id"] = object.SpaceID
	}
	if object.StableID != "" {
		meta["stable_id"] = object.StableID
	}
	if object.DriveType != "" {
		meta["file_type"] = object.DriveType
	}
	return meta
}
