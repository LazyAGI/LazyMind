package tree

import (
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

func documentItem(item DocumentWithState) SourceDocumentItem {
	out := SourceDocumentItem{
		SourceID:        item.Object.SourceID,
		BindingID:       item.Object.BindingID,
		ObjectKey:       item.Object.ObjectKey,
		DisplayName:     item.Object.DisplayName,
		SourceState:     item.State.SourceState,
		SyncState:       item.State.SyncState,
		ParseQueueState: item.State.ParseQueueState,
		ModifiedAt:      item.Object.ModifiedAt,
		LastError:       store.CloneJSON(item.State.LastError),
	}
	if item.Document != nil {
		out.DocumentID = item.Document.DocumentID
		out.ParseStatus = item.Document.ParseStatus
		out.CoreDocumentID = item.Document.CoreDocumentID
	}
	return out
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

