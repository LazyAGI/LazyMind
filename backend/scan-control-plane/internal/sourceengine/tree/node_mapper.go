package tree

import (
	"path"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

func targetNode(connectorType connector.ConnectorType, raw connector.RawObject, normalized connector.NormalizedSourceObject) TreeNode {
	return TreeNode{
		Key:           normalized.ObjectKey,
		NodeRef:       raw.ObjectRef,
		DisplayName:   normalized.DisplayName,
		SearchName:    normalized.SearchName,
		ConnectorType: string(connectorType),
		TargetType:    string(raw.BindingTargetType),
		TargetRef:     raw.BindingTargetRef,
		TreeKey:       treeKeyForTarget(raw, normalized),
		ObjectKey:     normalized.ObjectKey,
		ParentKey:     normalized.ParentKey,
		IsDocument:    normalized.IsDocument,
		IsContainer:   normalized.IsContainer,
		HasChildren:   normalized.HasChildren,
		Selectable:    selectableTarget(raw, normalized),
		ProviderMeta:  providerMeta(raw.ProviderMeta),
	}
}

func treeKeyForTarget(raw connector.RawObject, normalized connector.NormalizedSourceObject) string {
	if raw.TreeKey != "" {
		return raw.TreeKey
	}
	return normalized.ObjectKey
}

func selectableTarget(raw connector.RawObject, normalized connector.NormalizedSourceObject) bool {
	if raw.Bindable {
		return true
	}
	if raw.BindingTargetType == "" && raw.BindingTargetRef == "" && raw.TreeKey == "" {
		return normalized.IsContainer || normalized.IsDocument
	}
	return false
}

func bindingRootNode(binding store.Binding) TreeNode {
	return TreeNode{
		Key:           binding.BindingID,
		NodeRef:       binding.TreeKey,
		DisplayName:   binding.CoreParentDocumentName,
		ConnectorType: binding.ConnectorType,
		TargetType:    binding.TargetType,
		TargetRef:     binding.TargetRef,
		SourceID:      binding.SourceID,
		BindingID:     binding.BindingID,
		TreeKey:       binding.TreeKey,
		ObjectKey:     binding.TreeKey,
		IsContainer:   true,
		HasChildren:   true,
		Selectable:    false,
	}
}

func sourceObjectNode(item ObjectWithState) TreeNode {
	object := item.Object
	node := TreeNode{
		Key:          object.BindingID + ":" + object.ObjectKey,
		NodeRef:      object.ObjectKey,
		DisplayName:  object.DisplayName,
		SearchName:   object.SearchName,
		SourceID:     object.SourceID,
		BindingID:    object.BindingID,
		TreeKey:      object.TreeKey,
		ObjectKey:    object.ObjectKey,
		ParentKey:    object.ParentKey,
		IsDocument:   object.IsDocument,
		IsContainer:  object.IsContainer,
		HasChildren:  object.HasChildren,
		Selectable:   object.IsDocument,
		ProviderMeta: store.CloneJSON(object.ProviderMeta),
	}
	if item.State != nil {
		node.SourceState = item.State.SourceState
		node.SyncState = item.State.SyncState
		node.ParseQueueState = item.State.ParseQueueState
		node.Selectable = item.State.Selectable
	}
	return node
}

func liveSourceNode(sourceID string, binding store.Binding, raw connector.RawObject, normalized connector.NormalizedSourceObject) TreeNode {
	return TreeNode{
		Key:           binding.BindingID + ":" + normalized.ObjectKey,
		NodeRef:       raw.ObjectRef,
		DisplayName:   normalized.DisplayName,
		SearchName:    normalized.SearchName,
		ConnectorType: binding.ConnectorType,
		TargetType:    binding.TargetType,
		TargetRef:     binding.TargetRef,
		SourceID:      sourceID,
		BindingID:     binding.BindingID,
		TreeKey:       binding.TreeKey,
		ObjectKey:     normalized.ObjectKey,
		ParentKey:     normalized.ParentKey,
		IsDocument:    normalized.IsDocument,
		IsContainer:   normalized.IsContainer,
		HasChildren:   normalized.HasChildren,
		Selectable:    normalized.IsDocument,
		ProviderMeta:  providerMeta(raw.ProviderMeta),
	}
}

func documentItem(item DocumentWithState) SourceDocumentItem {
	displayName := documentSourceDisplayName(item)
	name := documentTypedName(item)
	fileType := documentFileType(item)
	updateType := updateTypeForState(item.State.SourceState)
	out := SourceDocumentItem{
		SourceID:        item.Object.SourceID,
		BindingID:       item.Object.BindingID,
		ObjectKey:       item.Object.ObjectKey,
		DisplayName:     displayName,
		Name:            name,
		Path:            documentPath(item, name),
		Directory:       documentDirectory(item),
		FileType:        fileType,
		SizeBytes:       item.Object.SizeBytes,
		SourceVersion:   item.State.SourceVersion,
		BaselineVersion: item.State.BaselineVersion,
		SourceState:     item.State.SourceState,
		SyncState:       item.State.SyncState,
		PendingAction:   item.State.PendingAction,
		ParseQueueState: item.State.ParseQueueState,
		ParseState:      item.State.ParseQueueState,
		HasUpdate:       updateType != "unchanged",
		UpdateType:      updateType,
		UpdateDesc:      updateDescForType(updateType),
		ModifiedAt:      item.Object.ModifiedAt,
		LastSyncedAt:    item.State.LastSyncedAt,
		LastError:       store.CloneJSON(item.State.LastError),
	}
	if item.Document != nil {
		out.DocumentID = item.Document.DocumentID
		out.ParseStatus = item.Document.ParseStatus
		out.ParseState = item.Document.ParseStatus
		out.CoreDocumentID = item.Document.CoreDocumentID
	}
	return out
}

func documentSourceDisplayName(item DocumentWithState) string {
	name := strings.TrimSpace(item.Object.DisplayName)
	if item.Document != nil && strings.TrimSpace(item.Document.DisplayName) != "" {
		name = strings.TrimSpace(item.Document.DisplayName)
	}
	if name == "" {
		return strings.TrimSpace(item.Object.ObjectKey)
	}
	return name
}

func documentTypedName(item DocumentWithState) string {
	name := documentSourceDisplayName(item)
	extension := normalizedExtension(item.Object.FileExtension)
	if extension == "" && item.Document != nil {
		extension = normalizedExtension(item.Document.FileExtension)
	}
	if extension == "" || path.Ext(name) != "" {
		return name
	}
	return name + extension
}

func documentFileType(item DocumentWithState) string {
	extension := normalizedExtension(item.Object.FileExtension)
	if extension == "" && item.Document != nil {
		extension = normalizedExtension(item.Document.FileExtension)
	}
	return strings.TrimPrefix(extension, ".")
}

func documentPath(item DocumentWithState, displayName string) string {
	parent := strings.TrimSpace(item.Object.ParentKey)
	if parent == "" {
		return displayName
	}
	return strings.TrimRight(parent, "/") + "/" + displayName
}

func documentDirectory(item DocumentWithState) string {
	parent := strings.TrimSpace(item.Object.ParentKey)
	if parent != "" {
		return parent
	}
	return item.Object.BindingID
}

func normalizedExtension(extension string) string {
	extension = strings.TrimSpace(strings.ToLower(extension))
	if extension == "" {
		return ""
	}
	if strings.HasPrefix(extension, ".") {
		return extension
	}
	return "." + extension
}

func updateTypeForState(sourceState string) string {
	switch strings.ToUpper(strings.TrimSpace(sourceState)) {
	case "NEW":
		return "new"
	case "MODIFIED":
		return "changed"
	case "DELETED":
		return "deleted"
	default:
		return "unchanged"
	}
}

func updateDescForType(updateType string) string {
	switch updateType {
	case "new":
		return "新文件待入库"
	case "changed":
		return "内容变化待重解析"
	case "deleted":
		return "源端删除待清理"
	default:
		return "当前文件已是最新"
	}
}

func providerMeta(in connector.ProviderMeta) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
