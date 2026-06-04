package acl

import (
	"sort"
	"strings"
)

// normalizePermission textPermissiontextPermissiontext，text。
func normalizePermission(resourceType, permission string) string {
	p := strings.ToUpper(strings.TrimSpace(permission))
	switch p {
	case "", strings.ToUpper(PermNone):
		return PermNone
	case strings.ToUpper(PermRead):
		if resourceType == ResourceTypeKB {
			return PermissionKBRead
		}
		if resourceType == ResourceTypeDB {
			return PermissionDatasetRead
		}
		if resourceType == ResourceTypeEvalSet {
			return PermissionEvalSetRead
		}
	case strings.ToUpper(PermWrite):
		if resourceType == ResourceTypeKB {
			return PermissionKBWrite
		}
		if resourceType == ResourceTypeDB {
			return PermissionDatasetWrite
		}
		if resourceType == ResourceTypeEvalSet {
			return PermissionEvalSetWrite
		}
	case strings.ToUpper(PermUpload):
		if resourceType == ResourceTypeDB {
			return PermissionDatasetUpload
		}
	case PermissionKBRead, PermissionKBWrite, PermissionKBCreateDoc, PermissionKBDeleteDoc, PermissionKBDelete:
		if resourceType == ResourceTypeKB {
			return p
		}
	case PermissionDatasetRead, PermissionDatasetWrite, PermissionDatasetUpload:
		if resourceType == ResourceTypeDB {
			return p
		}
	case PermissionEvalSetRead, PermissionEvalSetWrite:
		if resourceType == ResourceTypeEvalSet {
			return p
		}
	}
	return ""
}

func ownerPermissions(resourceType string) []string {
	switch resourceType {
	case ResourceTypeKB:
		return []string{PermissionKBRead, PermissionKBWrite, PermissionKBCreateDoc, PermissionKBDeleteDoc, PermissionKBDelete}
	case ResourceTypeDB:
		return []string{PermissionDatasetRead, PermissionDatasetWrite, PermissionDatasetUpload}
	case ResourceTypeEvalSet:
		return []string{PermissionEvalSetRead, PermissionEvalSetWrite}
	default:
		return nil
	}
}

func publicPermissions(resourceType string) []string {
	if resourceType == ResourceTypeKB {
		return []string{PermissionKBRead}
	}
	return nil
}

func effectivePermissions(resourceType, resourceID string, userID string) (permissions []string, source string) {
	return effectivePermissionsWithGroups(resourceType, resourceID, userID, nil)
}

func effectivePermissionsWithGroups(resourceType, resourceID string, userID string, groupIDs []string) (permissions []string, source string) {
	st := GetStore()
	if st == nil || strings.TrimSpace(userID) == "" || strings.TrimSpace(resourceID) == "" {
		return nil, "private"
	}
	permSet := map[string]struct{}{}
	add := func(items []string) {
		for _, item := range items {
			if item == "" || item == PermNone {
				continue
			}
			permSet[item] = struct{}{}
		}
	}

	if resourceType == ResourceTypeKB {
		kb := st.GetKB(resourceID)
		if kb != nil && kb.OwnerID == userID {
			perms := ownerPermissions(resourceType)
			return perms, SourceOwner
		}
		vis := st.GetVisibility(resourceID)
		if vis == VisibilityPublic {
			add(publicPermissions(resourceType))
			source = SourcePublic
		}
		if vis == VisibilityProtected && source == "" {
			source = SourceProtected
		}
	}

	for _, row := range st.ACLsForUserWithGroups(resourceType, resourceID, userID, groupIDs) {
		if perm := normalizePermission(resourceType, row.Permission); perm != "" && perm != PermNone {
			permSet[perm] = struct{}{}
			source = SourceACL
		}
	}

	if len(permSet) == 0 {
		if source == "" {
			source = "private"
		}
		return nil, source
	}
	permissions = make([]string, 0, len(permSet))
	for perm := range permSet {
		permissions = append(permissions, perm)
	}
	sort.Strings(permissions)
	return permissions, source
}

// PermissionFor textUsertextPermissiontext。
// text，textPermissiontext：none / read / write。
func PermissionFor(resourceType, resourceID string, userID string) (permission string, source string) {
	permissions, source := effectivePermissions(resourceType, resourceID, userID)
	if len(permissions) == 0 {
		return PermNone, source
	}
	for _, perm := range permissions {
		switch perm {
		case PermissionKBWrite, PermissionKBCreateDoc, PermissionKBDeleteDoc, PermissionKBDelete, PermissionDatasetWrite, PermissionDatasetUpload, PermissionEvalSetWrite:
			return PermWrite, source
		}
	}
	return PermRead, source
}

// PermissionsFor textUsertextPermissiontext。
func PermissionsFor(resourceType, resourceID string, userID string) (permissions []string, source string) {
	return effectivePermissions(resourceType, resourceID, userID)
}

// PermissionsForWithGroups text preloaded group IDs textPermissiontext。
func PermissionsForWithGroups(resourceType, resourceID string, userID string, groupIDs []string) (permissions []string, source string) {
	return effectivePermissionsWithGroups(resourceType, resourceID, userID, groupIDs)
}

// ResolveUserGroupIDs text one-shot load user groups（text auth-service）。
func ResolveUserGroupIDs(userID string) []string {
	st := GetStore()
	if st == nil {
		return nil
	}
	return st.loadUserGroupIDs(userID)
}

func hasPermission(permissions []string, want string) bool {
	want = strings.TrimSpace(want)
	if want == "" || want == PermNone {
		return false
	}
	for _, perm := range permissions {
		if perm == want {
			return true
		}
	}
	return false
}

func actionToPermission(resourceType, action string) string {
	a := strings.TrimSpace(action)
	switch resourceType {
	case ResourceTypeKB:
		switch a {
		case PermRead:
			return PermissionKBRead
		case PermWrite:
			return PermissionKBWrite
		case "create_doc":
			return PermissionKBCreateDoc
		case "delete_doc":
			return PermissionKBDeleteDoc
		case "delete_kb":
			return PermissionKBDelete
		default:
			return normalizePermission(resourceType, a)
		}
	case ResourceTypeDB:
		switch a {
		case PermRead:
			return PermissionDatasetRead
		case PermWrite:
			return PermissionDatasetWrite
		case PermUpload, "create_doc":
			return PermissionDatasetUpload
		default:
			return normalizePermission(resourceType, a)
		}
	case ResourceTypeEvalSet:
		switch a {
		case PermRead:
			return PermissionEvalSetRead
		case PermWrite:
			return PermissionEvalSetWrite
		default:
			return normalizePermission(resourceType, a)
		}
	default:
		return ""
	}
}

// Can textAuthorization：textUsertextPermissiontext。
func Can(userID string, resourceType, resourceID string, action string) bool {
	if strings.TrimSpace(userID) == "" || resourceID == "" {
		return false
	}
	permissions, _ := PermissionsFor(resourceType, resourceID, userID)
	want := actionToPermission(resourceType, action)
	if want == "" {
		return false
	}
	if hasPermission(permissions, want) {
		return true
	}
	if resourceType == ResourceTypeDB {
		if want == PermissionDatasetRead {
			return hasPermission(permissions, PermissionDatasetWrite) || hasPermission(permissions, PermissionDatasetUpload)
		}
		if want == PermissionDatasetUpload {
			return hasPermission(permissions, PermissionDatasetWrite)
		}
	}
	if resourceType == ResourceTypeKB {
		if want == PermissionKBRead {
			return hasPermission(permissions, PermissionKBWrite)
		}
		if want == PermissionKBCreateDoc || want == PermissionKBDeleteDoc {
			return hasPermission(permissions, PermissionKBWrite)
		}
	}
	if resourceType == ResourceTypeEvalSet && want == PermissionEvalSetRead {
		return hasPermission(permissions, PermissionEvalSetWrite)
	}
	return false
}
