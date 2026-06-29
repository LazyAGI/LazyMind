package feishu

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/lazymind/scan_control_plane/internal/sourceengine/connector"
)

const (
	VirtualDriveRootRef  = "feishu:drive:root"
	VirtualWikiSpacesRef = "feishu:wiki:spaces"
)

func validateTarget(targetType connector.TargetType, targetRef string) error {
	switch targetType {
	case TargetTypeDriveFolder:
		return nil
	case TargetTypeWikiNode:
		if strings.TrimSpace(targetRef) == VirtualWikiSpacesRef {
			return nil
		}
		_, _, _, err := parseWikiTarget(targetRef)
		return err
	default:
		return connector.NewError(connector.ErrorCodeInvalidTarget, "target_type is not supported")
	}
}

func isInitialRootRequest(req connector.ListChildrenRequest) bool {
	return req.TargetRef == "" && req.NodeRef == ""
}

func isSupportedTargetType(targetType connector.TargetType) bool {
	return targetType == TargetTypeDriveFolder || targetType == TargetTypeWikiNode
}

func isVirtualBranchRequest(nodeRef string) bool {
	return nodeRef == VirtualDriveRootRef || nodeRef == VirtualWikiSpacesRef || strings.HasPrefix(nodeRef, "feishu:wiki:space:")
}

func validatePageSize(pageSize, maxPageSize int) error {
	if pageSize <= 0 {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "page_size must be positive")
	}
	if pageSize > maxPageSize {
		return connector.NewError(connector.ErrorCodeInvalidArgument, "page_size exceeds connector max_page_size")
	}
	return nil
}

func parseCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		return 0, connector.NewError(connector.ErrorCodeInvalidArgument, "cursor is invalid")
	}
	return offset, nil
}

func driveFolderToken(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == VirtualDriveRootRef {
		return "root"
	}
	if token := lastURLPathSegment(ref); token != "" {
		ref = token
	}
	ref = strings.TrimPrefix(ref, "feishu:")
	ref = strings.TrimPrefix(ref, "drive:")
	ref = strings.TrimPrefix(ref, "drive_folder:")
	if ref == "" {
		return "root"
	}
	return ref
}

func parseWikiTarget(ref string) (string, string, bool, error) {
	if spaceID, ok := wikiSpaceID(ref); ok {
		return spaceID, "", true, nil
	}
	spaceID, nodeToken, err := wikiNode(ref)
	if err != nil {
		return "", "", false, err
	}
	if spaceID == "" || nodeToken == "" {
		return "", "", false, connector.NewError(connector.ErrorCodeInvalidArgument, "wiki target_ref must include space_id and node_token")
	}
	return spaceID, nodeToken, false, nil
}

func wikiNode(ref string) (string, string, error) {
	ref = strings.TrimSpace(strings.TrimPrefix(ref, "wiki:"))
	parts := strings.Split(ref, ":")
	if len(parts) != 2 {
		return "", "", connector.NewError(connector.ErrorCodeInvalidArgument, "wiki node ref is invalid")
	}
	return parts[0], parts[1], nil
}

func looseWikiNodeToken(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || ref == VirtualWikiSpacesRef {
		return ""
	}
	if _, ok := wikiSpaceID(ref); ok {
		return ""
	}
	if token := lastURLPathSegment(ref); token != "" {
		ref = token
	}
	ref = strings.TrimSpace(strings.TrimPrefix(ref, "feishu:"))
	ref = strings.TrimSpace(strings.TrimPrefix(ref, "wiki:"))
	parts := strings.Split(ref, ":")
	if len(parts) != 1 {
		return ""
	}
	return strings.TrimSpace(parts[0])
}

func lastURLPathSegment(ref string) string {
	ref = strings.TrimSpace(ref)
	if !strings.Contains(ref, "://") {
		return ""
	}
	parsed, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	for i := len(parts) - 1; i >= 0; i-- {
		segment := strings.TrimSpace(parts[i])
		if segment == "" {
			continue
		}
		unescaped, err := url.PathUnescape(segment)
		if err == nil {
			segment = strings.TrimSpace(unescaped)
		}
		if segment != "" {
			return segment
		}
	}
	return ""
}

func wikiSpaceID(ref string) (string, bool) {
	spaceID := wikiSpaceIDFromRef(ref)
	return spaceID, spaceID != ""
}

func wikiSpaceIDFromRef(ref string) string {
	const prefix = "feishu:wiki:space:"
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, prefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(ref, prefix))
}

func virtualRootPage(cursor string, pageSize int) (ObjectPage, error) {
	items := []Object{
		{Kind: ObjectKindVirtualRoot, Token: VirtualDriveRootRef, Name: "Drive", IsContainer: true, HasChildren: true, Revision: "virtual-drive"},
		{Kind: ObjectKindVirtualRoot, Token: VirtualWikiSpacesRef, Name: "Wiki", IsContainer: true, HasChildren: true, Revision: "virtual-wiki"},
	}
	return objectPageFromItems(items, cursor, pageSize)
}

func objectPageFromItems(items []Object, cursor string, pageSize int) (ObjectPage, error) {
	offset, err := parseCursor(cursor)
	if err != nil {
		return ObjectPage{}, err
	}
	if offset >= len(items) {
		return ObjectPage{}, nil
	}
	end := offset + pageSize
	if end > len(items) {
		end = len(items)
	}
	page := ObjectPage{Items: append([]Object(nil), items[offset:end]...)}
	if end < len(items) {
		page.HasMore = true
		page.NextCursor = strconv.Itoa(end)
	}
	return page, nil
}

func targetRefFor(object Object) string {
	if object.Kind == ObjectKindVirtualRoot || object.Kind == ObjectKindWikiSpace {
		return object.Token
	}
	if object.Kind == ObjectKindWikiNode {
		return fmt.Sprintf("wiki:%s:%s", object.SpaceID, object.Token)
	}
	return "drive:" + object.Token
}

func parentRefFor(object Object) string {
	if object.ParentToken == "" {
		if object.Kind == ObjectKindWikiSpace {
			return VirtualWikiSpacesRef
		}
		return ""
	}
	if object.Kind == ObjectKindWikiNode {
		return fmt.Sprintf("wiki:%s:%s", object.SpaceID, object.ParentToken)
	}
	return "drive:" + object.ParentToken
}

func targetFingerprint(object Object) string {
	return objectKeyFor(object)
}

func objectKeyFor(object Object) string {
	if object.Kind == ObjectKindVirtualRoot {
		return string(ConnectorType) + ":" + object.Token
	}
	if object.Kind == ObjectKindWikiSpace {
		return wikiSpaceObjectKey(object.SpaceID)
	}
	if object.Kind == ObjectKindWikiNode {
		return wikiObjectKey(object.SpaceID, object.Token)
	}
	return driveObjectKey(firstNonEmpty(object.StableID, object.Token))
}

func parentKeyFor(object Object) string {
	if object.ParentToken == "" {
		if object.Kind == ObjectKindWikiSpace {
			return string(ConnectorType) + ":" + VirtualWikiSpacesRef
		}
		if object.Kind == ObjectKindWikiNode && object.SpaceID != "" {
			return wikiSpaceObjectKey(object.SpaceID)
		}
		return ""
	}
	if object.Kind == ObjectKindWikiSpace {
		return string(ConnectorType) + ":" + VirtualWikiSpacesRef
	}
	if object.Kind == ObjectKindWikiNode {
		return wikiObjectKey(object.SpaceID, object.ParentToken)
	}
	return driveObjectKey(object.ParentToken)
}

func driveObjectKey(token string) string {
	return fmt.Sprintf("%s:drive:%s", ConnectorType, token)
}

func wikiObjectKey(spaceID, nodeToken string) string {
	return fmt.Sprintf("%s:wiki:%s:%s", ConnectorType, spaceID, nodeToken)
}

func wikiSpaceObjectKey(spaceID string) string {
	return fmt.Sprintf("%s:wiki:space:%s", ConnectorType, spaceID)
}

func scopeNodeRef(scopeRef connector.ScopeRef) string {
	for _, key := range []string{"node_ref", "object_ref", "target_ref"} {
		if value := strings.TrimSpace(scopeRef[key]); value != "" {
			return value
		}
	}
	if objectKey := strings.TrimSpace(scopeRef["object_key"]); objectKey != "" {
		if strings.HasPrefix(objectKey, string(ConnectorType)+":wiki:") {
			return strings.TrimPrefix(objectKey, string(ConnectorType)+":wiki:")
		}
		if strings.HasPrefix(objectKey, string(ConnectorType)+":drive:") {
			return strings.TrimPrefix(objectKey, string(ConnectorType)+":drive:")
		}
	}
	return ""
}

func displayName(name, fallback string) string {
	if strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	return strings.TrimSpace(fallback)
}

func searchName(search, display string) string {
	if strings.TrimSpace(search) != "" {
		return strings.ToLower(strings.TrimSpace(search))
	}
	return strings.ToLower(strings.TrimSpace(display))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func uniqueNonEmptyStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		normalized := strings.TrimSpace(value)
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

func normalizedFeishuObjectType(value string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(value)), ".")
}

func isFeishuDocType(value string) bool {
	switch normalizedFeishuObjectType(value) {
	case "doc", "docx":
		return true
	default:
		return false
	}
}

func cloneProviderMeta(meta connector.ProviderMeta) connector.ProviderMeta {
	if meta == nil {
		return nil
	}
	cloned := make(connector.ProviderMeta, len(meta))
	for key, value := range meta {
		cloned[key] = value
	}
	return cloned
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
