package localfs

import (
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"time"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

func (c *LocalFSConnector) rawObject(agentID string, info PathInfo, parentPath string) connector.RawObject {
	path := canonicalPath(info)
	extension := fileExtension(info)
	mimeType := info.MimeType
	if mimeType == "" && !info.IsDir {
		mimeType = mime.TypeByExtension(extension)
	}
	raw := connector.RawObject{
		ObjectRef:     path,
		ObjectKey:     objectKeyFor(agentID, info),
		ParentRef:     parentRef(info, parentPath),
		ParentKey:     parentKeyFor(agentID, info, parentPath),
		DisplayName:   displayName(info.DisplayName, path),
		SearchName:    strings.ToLower(displayName(info.DisplayName, path)),
		ObjectType:    objectType(info),
		IsDocument:    !info.IsDir,
		IsContainer:   info.IsDir,
		HasChildren:   info.IsDir,
		SourceVersion: versionFor(info),
		SizeBytes:     info.SizeBytes,
		MimeType:      mimeType,
		FileExtension: extension,
		ProviderMeta:  providerMeta(agentID, info, parentPath),
	}
	raw.TreeKey = raw.ObjectKey
	if info.IsDir {
		raw.Bindable = true
		raw.BindingTargetType = TargetTypeLocalPath
		raw.BindingTargetRef = path
	}
	if info.MTimeUnixNano > 0 {
		modifiedAt := time.Unix(0, info.MTimeUnixNano)
		raw.ModifiedAt = &modifiedAt
	}
	return raw
}

func (c *LocalFSConnector) buildObjectKey(raw connector.RawObject) (string, error) {
	if raw.ObjectKey != "" {
		return raw.ObjectKey, nil
	}
	agentID := raw.ProviderMeta["agent_id"]
	if stableID := raw.ProviderMeta["stable_id"]; stableID != "" && agentID != "" {
		return stableObjectKey(agentID, stableID), nil
	}
	path := raw.ProviderMeta["path"]
	if path == "" {
		path = raw.ObjectRef
	}
	if agentID == "" || path == "" {
		return "", connector.NewError(connector.ErrorCodeInvalidArgument, "agent_id and path are required to build object_key")
	}
	return pathObjectKey(agentID, path), nil
}

func (c *LocalFSConnector) buildParentKey(raw connector.RawObject) string {
	if raw.ParentKey != "" {
		return raw.ParentKey
	}
	agentID := raw.ProviderMeta["agent_id"]
	if parentStableID := raw.ProviderMeta["parent_stable_id"]; parentStableID != "" && agentID != "" {
		return stableObjectKey(agentID, parentStableID)
	}
	parentPath := raw.ProviderMeta["parent_path"]
	if parentPath == "" {
		parentPath = raw.ParentRef
	}
	if agentID == "" || parentPath == "" {
		return ""
	}
	return pathObjectKey(agentID, parentPath)
}

func objectType(info PathInfo) connector.ObjectType {
	if info.IsDir {
		return connector.ObjectTypeFolder
	}
	return connector.ObjectTypeFile
}

func versionFor(info PathInfo) string {
	return fmt.Sprintf("%d:%d", info.MTimeUnixNano, info.SizeBytes)
}

func fileExtension(info PathInfo) string {
	if info.FileExtension != "" {
		return info.FileExtension
	}
	if info.IsDir {
		return ""
	}
	return filepath.Ext(displayName(info.DisplayName, canonicalPath(info)))
}

func providerMeta(agentID string, info PathInfo, parentPath string) connector.ProviderMeta {
	meta := connector.ProviderMeta{
		"agent_id": agentID,
		"path":     canonicalPath(info),
	}
	if info.StableID != "" {
		meta["stable_id"] = info.StableID
	}
	if parent := parentRef(info, parentPath); parent != "" {
		meta["parent_path"] = parent
	}
	if info.ParentStableID != "" {
		meta["parent_stable_id"] = info.ParentStableID
	}
	return meta
}
