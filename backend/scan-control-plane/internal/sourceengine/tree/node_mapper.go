package tree

import (
	"path"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
	store "github.com/lazymind/scan_control_plane/internal/store/source"
)

const (
	parseStateQueued       = "QUEUED"
	parseStatePendingParse = "PENDING_PARSE"

	effectiveParseStatusParsed         = "PARSED"
	effectiveParseStatusParsing        = "PARSING"
	effectiveParseStatusFailed         = "FAILED"
	effectiveParseStatusParseFailed    = "PARSE_FAILED"
	effectiveParseStatusCanceled       = "CANCELED"
	effectiveParseStatusDownloading    = "DOWNLOADING"
	effectiveParseStatusDownloadFailed = "DOWNLOAD_FAILED"
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
		IsDocument:    true,
		IsContainer:   true,
		HasChildren:   true,
		Selectable:    true,
	}
}

func bindingRootNodeWithObject(node TreeNode, item ObjectWithState) TreeNode {
	objectNode := sourceObjectNode(item)
	node.DisplayName = objectNode.DisplayName
	node.SearchName = objectNode.SearchName
	node.SourceState = objectNode.SourceState
	node.SyncState = objectNode.SyncState
	node.PendingAction = objectNode.PendingAction
	node.ParseQueueState = objectNode.ParseQueueState
	node.HasUpdate = objectNode.HasUpdate
	node.UpdateType = objectNode.UpdateType
	node.UpdateDesc = objectNode.UpdateDesc
	node.ProviderMeta = objectNode.ProviderMeta
	return node
}

func sourceObjectNode(item ObjectWithState) TreeNode {
	object := item.Object
	selectableContainer := object.IsContainer || object.HasChildren
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
		IsDocument:   object.IsDocument || selectableContainer,
		IsContainer:  object.IsContainer,
		HasChildren:  object.HasChildren,
		Selectable:   object.IsDocument || selectableContainer,
		ProviderMeta: store.CloneJSON(object.ProviderMeta),
	}
	if item.State != nil {
		updateType := updateTypeForState(item.State.SourceState)
		node.SourceState = item.State.SourceState
		node.SyncState = item.State.SyncState
		node.PendingAction = item.State.PendingAction
		node.ParseQueueState = item.State.ParseQueueState
		node.HasUpdate = updateType != "unchanged"
		node.UpdateType = updateType
		node.UpdateDesc = updateDescForType(updateType)
		node.Selectable = item.State.Selectable || selectableContainer
	}
	return node
}

func liveSourceNode(sourceID string, binding store.Binding, raw connector.RawObject, normalized connector.NormalizedSourceObject) TreeNode {
	selectableContainer := normalized.IsContainer || normalized.HasChildren
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
		IsDocument:    normalized.IsDocument || selectableContainer,
		IsContainer:   normalized.IsContainer,
		HasChildren:   normalized.HasChildren,
		Selectable:    normalized.IsDocument || selectableContainer,
		ProviderMeta:  providerMeta(raw.ProviderMeta),
	}
}

func documentItem(item DocumentWithState, binding store.Binding) SourceDocumentItem {
	displayName := documentSourceDisplayName(item)
	name := documentTypedName(item)
	fileType := documentFileType(item)
	updateType := updateTypeForState(item.State.SourceState)
	parseState := documentPendingParseState(item, updateType)
	out := SourceDocumentItem{
		SourceID:         item.Object.SourceID,
		BindingID:        item.Object.BindingID,
		ObjectKey:        item.Object.ObjectKey,
		DisplayName:      displayName,
		Name:             name,
		Path:             documentPath(item, name),
		Directory:        documentDirectory(item),
		FileType:         fileType,
		SizeBytes:        item.Object.SizeBytes,
		SourceVersion:    item.State.SourceVersion,
		BaselineVersion:  item.State.BaselineVersion,
		SourceState:      item.State.SourceState,
		SyncState:        item.State.SyncState,
		PendingAction:    item.State.PendingAction,
		ParseQueueState:  parseState,
		ParseState:       parseState,
		HasUpdate:        updateType != "unchanged",
		UpdateType:       updateType,
		UpdateDesc:       updateDescForType(updateType),
		SourceModifiedAt: item.Object.ModifiedAt,
		LastSyncedAt:     item.State.LastSyncedAt,
		LastError:        store.CloneJSON(item.State.LastError),
	}
	if item.Document != nil {
		out.DocumentID = item.Document.DocumentID
		out.ParseStatus = item.Document.ParseStatus
		out.ParseState = documentEffectiveParseState(parseState, item.Document.ParseStatus)
		out.CoreDocumentID = item.Document.CoreDocumentID
	}
	out.EffectiveParseStatus = documentEffectiveParseStatus(out, binding)
	return out
}

func documentEffectiveParseState(queueState, documentStatus string) string {
	if activeParseState(queueState) {
		return queueState
	}
	if documentCanceledState(documentStatus) {
		return effectiveParseStatusCanceled
	}
	if strings.ToUpper(strings.TrimSpace(queueState)) == store.ParseTaskStatusFailed {
		return queueState
	}
	if strings.TrimSpace(documentStatus) != "" {
		return documentStatus
	}
	return queueState
}

func activeParseState(value string) bool {
	switch strings.ToUpper(strings.TrimSpace(value)) {
	case parseStateQueued, store.ParseTaskStatusPending, store.ParseTaskStatusRunning, store.ParseTaskStatusSubmitted:
		return true
	default:
		return false
	}
}

func documentEffectiveParseStatus(item SourceDocumentItem, binding store.Binding) string {
	parseState := strings.ToUpper(strings.TrimSpace(firstNonEmptyString(item.ParseState, item.ParseQueueState, item.ParseStatus)))
	if parseState == "" {
		return ""
	}
	if documentCanceledState(parseState) || documentCanceledState(item.ParseStatus) || documentCanceledError(item.LastError) {
		return effectiveParseStatusCanceled
	}
	supportsDownloadStatus := supportsDocumentDownloadStatus(binding)
	if documentFailureState(parseState) {
		if supportsDownloadStatus && documentDownloadFailure(parseState, item.LastError) {
			return effectiveParseStatusDownloadFailed
		}
		if documentParseFailure(parseState, item.LastError) {
			return effectiveParseStatusParseFailed
		}
		return effectiveParseStatusFailed
	}
	if supportsDownloadStatus && documentDownloadInProgressState(parseState) {
		return effectiveParseStatusDownloading
	}
	if documentParsingState(parseState) {
		return effectiveParseStatusParsing
	}
	if parseState == store.ParseTaskStatusSucceeded {
		return effectiveParseStatusParsed
	}
	return parseState
}

func supportsDocumentDownloadStatus(binding store.Binding) bool {
	connectorType := strings.ToLower(strings.TrimSpace(binding.ConnectorType))
	return connectorType != "" && connectorType != "local" && connectorType != "local_fs"
}

func documentDownloadInProgressState(parseState string) bool {
	switch parseState {
	case parseStateQueued, store.ParseTaskStatusPending, store.ParseTaskStatusRunning:
		return true
	default:
		return false
	}
}

func documentParsingState(parseState string) bool {
	return documentDownloadInProgressState(parseState) || parseState == store.ParseTaskStatusSubmitted
}

func documentFailureState(parseState string) bool {
	return strings.Contains(parseState, "FAIL") || strings.Contains(parseState, "ERROR")
}

func documentCanceledState(value string) bool {
	return statusTextHasAny(value, "canceled", "cancelled")
}

func documentCanceledError(lastError map[string]any) bool {
	for _, key := range []string{"code", "reason", "status", "task_state"} {
		if documentCanceledState(documentLastErrorField(lastError, key)) {
			return true
		}
	}
	return false
}

func documentDownloadFailure(parseState string, lastError map[string]any) bool {
	phase := documentLastErrorField(lastError, "phase")
	if statusTextHasAny(phase, "download", "export", "fetch", "source") {
		return true
	}
	text := documentErrorStatusText(parseState, lastError)
	return statusTextHasAny(
		text,
		"download_failed",
		"download failed",
		"export_failed",
		"export failed",
		"fetch_failed",
		"fetch failed",
		"transient_source_error",
		"unsupported_export",
		"auth_connection_invalid",
		"permission_denied",
		"download",
		"export",
	)
}

func documentParseFailure(parseState string, lastError map[string]any) bool {
	phase := documentLastErrorField(lastError, "phase")
	if statusTextHasAny(phase, "parse", "index", "ingest", "core", "knowledge") {
		return true
	}
	text := documentErrorStatusText(parseState, lastError)
	return statusTextHasAny(
		text,
		"parse_failed",
		"parse failed",
		"core_task_failed",
		"core_submit_failed",
		"core_task_not_found",
		"index_failed",
		"index failed",
		"ingest_failed",
		"ingest failed",
	)
}

func documentErrorStatusText(parseState string, lastError map[string]any) string {
	parts := []string{parseState}
	for _, key := range []string{"phase", "stage", "code", "reason", "message", "error"} {
		if value := documentLastErrorField(lastError, key); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " ")
}

func documentLastErrorField(lastError map[string]any, key string) string {
	if lastError == nil {
		return ""
	}
	value, ok := lastError[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func statusTextHasAny(value string, candidates ...string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, candidate := range candidates {
		if strings.Contains(normalized, candidate) {
			return true
		}
	}
	return false
}

func documentPendingParseState(item DocumentWithState, updateType string) string {
	if item.Document == nil && (updateType == "new" || updateType == "changed") {
		return parseStatePendingParse
	}
	return item.State.ParseQueueState
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
