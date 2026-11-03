package acl

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/log"
)

// Store text ORM text ACL text。
type Store struct {
	db *orm.DB
}

var defaultStore *Store

// GetStore text ACL text。text InitStore。
func GetStore() *Store { return defaultStore }

// InitStore textInitialize ACL text。text main text DB text migrate.RunUp() text。
func InitStore(db *orm.DB) {
	if db == nil {
		panic("acl: InitStore requires non-nil db")
	}
	defaultStore = &Store{db: db}
}

// EnsureKB textKnowledge basetextCreate，text kb_id。
func (s *Store) EnsureKB(kbID string, name string, ownerID string) string {
	if kbID != "" {
		var m orm.KBModel
		if err := s.db.First(&m, "id = ?", kbID).Error; err == nil {
			return kbID
		}
	}
	if kbID == "" {
		kbID = fmt.Sprintf("kb_%d", time.Now().UnixNano())
	}
	s.db.Create(&orm.KBModel{ID: kbID, Name: name, OwnerID: ownerID, Visibility: VisibilityPrivate})
	return kbID
}

// GetKB textKnowledge basetext（text）。
func (s *Store) GetKB(kbID string) *KBInfo {
	var m orm.KBModel
	if err := s.db.First(&m, "id = ?", kbID).Error; err != nil {
		return nil
	}
	return &KBInfo{ID: m.ID, Name: m.Name, OwnerID: m.OwnerID, Visibility: m.Visibility}
}

// SetKBVisibility SetKnowledge basetext，textUpdate acl_visibility text acl_kbs。
func (s *Store) SetKBVisibility(kbID string, level string) {
	_ = s.db.Transaction(func(tx *gorm.DB) error {
		var v orm.VisibilityModel
		if err := tx.Where("resource_id = ?", kbID).First(&v).Error; err != nil {
			tx.Create(&orm.VisibilityModel{ResourceID: kbID, Level: level})
		} else {
			tx.Model(&v).Update("level", level)
		}
		var k orm.KBModel
		if tx.First(&k, "id = ?", kbID).Error == nil {
			tx.Model(&k).Update("visibility", level)
		}
		return nil
	})
}

// GetVisibility textKnowledge basetext，text private。
func (s *Store) GetVisibility(kbID string) string {
	var v orm.VisibilityModel
	if err := s.db.Where("resource_id = ?", kbID).First(&v).Error; err != nil {
		return VisibilityPrivate
	}
	return v.Level
}

// AddACL text ACL text，text acl_id。
func canonicalGranteeType(granteeType string) string {
	switch granteeType {
	case GranteeTenant:
		return GranteeGroup
	default:
		return granteeType
	}
}

func (s *Store) AddACL(resourceType, resourceID string, granteeType string, targetID string, permission string, createdBy string, expiresAt *time.Time) int64 {
	if s == nil || s.db == nil {
		log.Logger.Error().
			Str("resource_type", resourceType).
			Str("resource_id", resourceID).
			Str("grantee_type", granteeType).
			Str("target_id", targetID).
			Str("permission", permission).
			Str("created_by", createdBy).
			Msg("add acl failed: store is not initialized")
		return 0
	}
	permission = normalizePermission(resourceType, permission)
	granteeType = canonicalGranteeType(granteeType)
	if permission == "" || permission == PermNone {
		log.Logger.Warn().
			Str("resource_type", resourceType).
			Str("resource_id", resourceID).
			Str("grantee_type", granteeType).
			Str("target_id", targetID).
			Str("permission", permission).
			Str("created_by", createdBy).
			Msg("add acl skipped: invalid normalized permission")
		return 0
	}
	if strings.TrimSpace(targetID) == "" {
		log.Logger.Warn().
			Str("resource_type", resourceType).
			Str("resource_id", resourceID).
			Str("grantee_type", granteeType).
			Str("permission", permission).
			Str("created_by", createdBy).
			Msg("add acl skipped: empty target id")
		return 0
	}
	var existing orm.ACLModel
	if err := s.db.Where("resource_type = ? AND resource_id = ? AND grantee_type = ? AND target_id = ? AND permission = ?", resourceType, resourceID, granteeType, targetID, permission).First(&existing).Error; err == nil {
		updates := map[string]any{}
		if expiresAt != nil || existing.ExpiresAt != nil {
			updates["expires_at"] = expiresAt
		}
		if len(updates) > 0 {
			if err := s.db.Model(&existing).Updates(updates).Error; err != nil {
				log.Logger.Error().
					Err(err).
					Int64("acl_id", existing.ID).
					Str("resource_type", resourceType).
					Str("resource_id", resourceID).
					Str("grantee_type", granteeType).
					Str("target_id", targetID).
					Str("permission", permission).
					Msg("add acl found existing row but failed to update expires_at")
				return 0
			}
		}
		log.Logger.Info().
			Int64("acl_id", existing.ID).
			Str("resource_type", resourceType).
			Str("resource_id", resourceID).
			Str("grantee_type", granteeType).
			Str("target_id", targetID).
			Str("permission", permission).
			Str("created_by", createdBy).
			Msg("add acl reused existing row")
		return existing.ID
	}
	m := &orm.ACLModel{
		ResourceType: resourceType,
		ResourceID:   resourceID,
		GranteeType:  granteeType,
		TargetID:     targetID,
		Permission:   permission,
		CreatedBy:    createdBy,
		CreatedAt:    time.Now(),
		ExpiresAt:    expiresAt,
	}
	if err := s.db.Create(m).Error; err != nil {
		log.Logger.Error().
			Err(err).
			Str("resource_type", resourceType).
			Str("resource_id", resourceID).
			Str("grantee_type", granteeType).
			Str("target_id", targetID).
			Str("permission", permission).
			Str("created_by", createdBy).
			Msg("add acl insert failed")
		return 0
	}
	log.Logger.Info().
		Int64("acl_id", m.ID).
		Str("resource_type", resourceType).
		Str("resource_id", resourceID).
		Str("grantee_type", granteeType).
		Str("target_id", targetID).
		Str("permission", permission).
		Str("created_by", createdBy).
		Msg("add acl inserted row")
	return m.ID
}

// UpdateACL UpdatePermissiontext。
func (s *Store) UpdateACL(aclID int64, permission string, expiresAt *time.Time) bool {
	var row orm.ACLModel
	if err := s.db.First(&row, "id = ?", aclID).Error; err != nil {
		return false
	}
	permission = normalizePermission(row.ResourceType, permission)
	res := s.db.Model(&orm.ACLModel{}).Where("id = ?", aclID).Updates(map[string]any{
		"permission": permission,
		"expires_at": expiresAt,
	})
	return res.RowsAffected > 0
}

// DeleteACL text id Deletetext ACL。
func (s *Store) DeleteACL(aclID int64) bool {
	res := s.db.Delete(&orm.ACLModel{}, "id = ?", aclID)
	return res.RowsAffected > 0
}

// ListACL text ACL list，text grantee_type text，text。
func (s *Store) ListACL(resourceType, resourceID string, granteeType string) []ACLListItem {
	q := s.db.Model(&orm.ACLModel{}).
		Where("resource_type = ? AND resource_id = ?", resourceType, resourceID).
		Where("expires_at IS NULL OR expires_at > ?", time.Now())
	if granteeType != "" {
		q = q.Where("grantee_type = ?", canonicalGranteeType(granteeType))
	}
	var rows []orm.ACLModel
	q.Find(&rows)
	out := make([]ACLListItem, 0, len(rows))
	for _, r := range rows {
		out = append(out, ACLListItem{
			ID:          r.ID,
			GranteeType: r.GranteeType,
			GranteeID:   r.TargetID,
			Permission:  r.Permission,
			CreatedAt:   r.CreatedAt,
		})
	}
	return out
}

// GetACLByID text id text ACL text，text。
func (s *Store) GetACLByID(resourceType, resourceID string, aclID int64) (*ACLRow, bool) {
	var m orm.ACLModel
	if err := s.db.First(&m, "id = ? AND resource_type = ? AND resource_id = ?", aclID, resourceType, resourceID).Error; err != nil {
		return nil, false
	}
	return &ACLRow{
		ID:           m.ID,
		ResourceType: m.ResourceType,
		ResourceID:   m.ResourceID,
		GranteeType:  m.GranteeType,
		TargetID:     m.TargetID,
		Permission:   m.Permission,
		CreatedBy:    m.CreatedBy,
		CreatedAt:    m.CreatedAt,
		ExpiresAt:    m.ExpiresAt,
	}, true
}

// ACLsForUser textUsertext ACL text（textUsertextTenant/text）。
func (s *Store) ACLsForUser(resourceType, resourceID string, userID string) []*ACLRow {
	return s.ACLsForUserWithGroups(resourceType, resourceID, userID, nil)
}

// ACLsForUserWithGroups textuser groups text，groupIDs text nil text。
func (s *Store) ACLsForUserWithGroups(resourceType, resourceID string, userID string, groupIDs []string) []*ACLRow {
	now := time.Now()
	q := s.db.Model(&orm.ACLModel{}).
		Where("resource_type = ? AND resource_id = ?", resourceType, resourceID).
		Where("expires_at IS NULL OR expires_at > ?", now)

	var rows []orm.ACLModel
	q.Find(&rows)
	if len(rows) == 0 {
		return nil
	}

	if groupIDs == nil {
		groupIDs = s.loadUserGroupIDs(userID)
	}
	groupSet := make(map[string]bool, len(groupIDs))
	for _, g := range groupIDs {
		gg := strings.TrimSpace(g)
		if gg == "" {
			continue
		}
		groupSet[gg] = true
	}

	var out []*ACLRow
	for _, r := range rows {
		if r.GranteeType == GranteeUser && r.TargetID == userID {
			out = append(out, toACLRow(&r))
			continue
		}
		if (r.GranteeType == GranteeGroup || r.GranteeType == GranteeTenant) && groupSet[r.TargetID] {
			out = append(out, toACLRow(&r))
		}
	}
	return out
}

func (s *Store) loadUserGroupIDs(userID string) []string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil
	}
	seen := map[string]struct{}{}
	var localGroupIDs []string
	_ = s.db.Model(&orm.UserGroupModel{}).Where("user_id = ?", userID).Pluck("group_id", &localGroupIDs).Error
	for _, groupID := range localGroupIDs {
		groupID = strings.TrimSpace(groupID)
		if groupID != "" {
			seen[groupID] = struct{}{}
		}
	}
	remoteGroupIDs := fetchUserGroupIDsFromAuthService(context.Background(), userID)
	for _, groupID := range remoteGroupIDs {
		groupID = strings.TrimSpace(groupID)
		if groupID == "" {
			continue
		}
		seen[groupID] = struct{}{}
		s.EnsureGroup(groupID, "")
		s.db.FirstOrCreate(&orm.UserGroupModel{}, &orm.UserGroupModel{UserID: userID, GroupID: groupID})
	}
	out := make([]string, 0, len(seen))
	for groupID := range seen {
		out = append(out, groupID)
	}
	sort.Strings(out)
	log.Logger.Debug().
		Str("user_id", userID).
		Strs("local_group_ids", localGroupIDs).
		Strs("remote_group_ids", remoteGroupIDs).
		Strs("merged_group_ids", out).
		Msg("resolved user groups for acl")
	return out
}

func fetchUserGroupIDsFromAuthService(ctx context.Context, userID string) []string {
	base := strings.TrimSpace(authServiceBaseURL())
	if base == "" || strings.TrimSpace(userID) == "" {
		return nil
	}
	endpoint := base + "/user/" + url.PathEscape(userID) + "/groups/internal"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		log.Logger.Warn().Err(err).Str("user_id", userID).Str("url", endpoint).Msg("build auth-service request failed")
		return nil
	}
	if tok := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN")); tok != "" {
		req.Header.Set("X-LazyMind-Internal-Token", tok)
	}
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		log.Logger.Warn().Err(err).Str("user_id", userID).Str("url", endpoint).Msg("fetch user groups from auth-service failed")
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Logger.Warn().Int("status", resp.StatusCode).Str("user_id", userID).Str("url", endpoint).Msg("auth-service returned non-2xx for user groups")
		return nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Logger.Warn().Err(err).Str("user_id", userID).Str("url", endpoint).Msg("read auth-service user groups response failed")
		return nil
	}
	var payload struct {
		Groups []struct {
			GroupID string `json:"group_id"`
		} `json:"groups"`
		Data struct {
			Groups []struct {
				GroupID string `json:"group_id"`
			} `json:"groups"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		log.Logger.Warn().Err(err).Str("user_id", userID).Str("url", endpoint).Str("body", strings.TrimSpace(string(body))).Msg("decode auth-service user groups response failed")
		return nil
	}
	groups := payload.Groups
	if len(groups) == 0 {
		groups = payload.Data.Groups
	}
	log.Logger.Info().
		Str("user_id", userID).
		Str("url", endpoint).
		Str("body", strings.TrimSpace(string(body))).
		Int("group_count", len(groups)).
		Msg("fetched user groups from auth-service")
	out := make([]string, 0, len(groups))
	for _, item := range groups {
		if groupID := strings.TrimSpace(item.GroupID); groupID != "" {
			out = append(out, groupID)
		}
	}
	return out
}

func authServiceBaseURL() string {
	if u := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_URL")); u != "" {
		base := strings.TrimRight(u, "/")
		if strings.HasSuffix(base, "/api/authservice") {
			return base
		}
		return base + "/api/authservice"
	}
	return "http://auth-service:8000/api/authservice"
}

func toACLRow(m *orm.ACLModel) *ACLRow {
	return &ACLRow{
		ID:           m.ID,
		ResourceType: m.ResourceType,
		ResourceID:   m.ResourceID,
		GranteeType:  m.GranteeType,
		TargetID:     m.TargetID,
		Permission:   m.Permission,
		CreatedBy:    m.CreatedBy,
		CreatedAt:    m.CreatedAt,
		ExpiresAt:    m.ExpiresAt,
	}
}

// EnsureGroup textCreate；name text。
func (s *Store) EnsureGroup(groupID string, name string) string {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		groupID = fmt.Sprintf("group_%d", time.Now().UnixNano())
	}
	var g orm.ACLGroupModel
	if err := s.db.First(&g, "id = ?", groupID).Error; err == nil {
		if name != "" && g.Name != name {
			s.db.Model(&g).Update("name", name)
		}
		return groupID
	}
	s.db.Create(&orm.ACLGroupModel{ID: groupID, Name: name})
	return groupID
}

// DeleteGroup Deletetext、Membertext ACL text。
func (s *Store) DeleteGroup(groupID string) {
	if strings.TrimSpace(groupID) == "" {
		return
	}
	_ = s.db.Transaction(func(tx *gorm.DB) error {
		tx.Delete(&orm.UserGroupModel{}, "group_id = ?", groupID)
		tx.Delete(&orm.ACLModel{}, "grantee_type IN ? AND target_id = ?", []string{GranteeGroup, GranteeTenant}, groupID)
		tx.Delete(&orm.ACLGroupModel{}, "id = ?", groupID)
		return nil
	})
}

// AddUserToGroup textUsertextMembertext。
func (s *Store) AddUserToGroup(userID, groupID string) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(groupID) == "" {
		return
	}
	s.EnsureGroup(groupID, "")
	s.db.FirstOrCreate(&orm.UserGroupModel{}, &orm.UserGroupModel{UserID: userID, GroupID: groupID})
}

// RemoveUserFromGroup textUsertextMembertext，textUsertext ACL。
func (s *Store) RemoveUserFromGroup(userID, groupID string) {
	if strings.TrimSpace(userID) == "" || strings.TrimSpace(groupID) == "" {
		return
	}
	s.db.Delete(&orm.UserGroupModel{}, "user_id = ? AND group_id = ?", userID, groupID)
}

// SetUserGroups SetUsertext id text；text。
func (s *Store) SetUserGroups(userID string, groupIDs []string) {
	s.db.Where("user_id = ?", userID).Delete(&orm.UserGroupModel{})
	for _, gid := range groupIDs {
		s.AddUserToGroup(userID, gid)
	}
}

// ListGroups textMembertext。
func (s *Store) ListGroups() []GroupInfo {
	var groups []orm.ACLGroupModel
	s.db.Order("id asc").Find(&groups)
	out := make([]GroupInfo, 0, len(groups))
	for _, g := range groups {
		var n int64
		_ = s.db.Model(&orm.UserGroupModel{}).Where("group_id = ?", g.ID).Count(&n).Error
		out = append(out, GroupInfo{ID: g.ID, Name: g.Name, UserCount: n})
	}
	return out
}

// ListGroupUsers textMember list。
func (s *Store) ListGroupUsers(groupID string) []GroupMember {
	var rows []orm.UserGroupModel
	s.db.Where("group_id = ?", groupID).Order("user_id asc").Find(&rows)
	out := make([]GroupMember, 0, len(rows))
	for _, row := range rows {
		out = append(out, GroupMember{UserID: row.UserID})
	}
	return out
}

// ListUserGroups textUsertext。
func (s *Store) ListUserGroups(userID string) []GroupInfo {
	var memberships []orm.UserGroupModel
	s.db.Where("user_id = ?", userID).Order("group_id asc").Find(&memberships)
	out := make([]GroupInfo, 0, len(memberships))
	for _, membership := range memberships {
		var group orm.ACLGroupModel
		if err := s.db.First(&group, "id = ?", membership.GroupID).Error; err != nil {
			continue
		}
		var n int64
		_ = s.db.Model(&orm.UserGroupModel{}).Where("group_id = ?", group.ID).Count(&n).Error
		out = append(out, GroupInfo{ID: group.ID, Name: group.Name, UserCount: n})
	}
	return out
}

// ReplaceACLForKB replaces all ACL rows for the kb with submitted grants.
// It is used by authorization page "save" behavior.
func (s *Store) ReplaceACLForKB(kbID string, grants []AuthorizationSubjectGrant, createdBy string) (int64, error) {
	log.Logger.Info().
		Str("kb_id", kbID).
		Str("created_by", createdBy).
		Any("grants", grants).
		Msg("replace kb acl start")
	var inserted int64
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("resource_type = ? AND resource_id = ?", ResourceTypeKB, kbID).Delete(&orm.ACLModel{}).Error; err != nil {
			return err
		}
		now := time.Now()
		for _, g := range grants {
			gt := canonicalGranteeType(g.GranteeType)
			if gt != GranteeUser && gt != GranteeGroup {
				continue
			}
			for _, p := range g.Permissions {
				np := normalizePermission(ResourceTypeKB, p)
				if np == "" || np == PermNone {
					continue
				}
				row := orm.ACLModel{
					ResourceType: ResourceTypeKB,
					ResourceID:   kbID,
					GranteeType:  gt,
					TargetID:     g.GranteeID,
					Permission:   np,
					CreatedBy:    createdBy,
					CreatedAt:    now,
				}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
				inserted++
			}
		}
		return nil
	})
	if err != nil {
		log.Logger.Error().Err(err).Str("kb_id", kbID).Str("created_by", createdBy).Msg("replace kb acl failed")
	} else {
		log.Logger.Info().Str("kb_id", kbID).Str("created_by", createdBy).Int64("inserted_acl_rows", inserted).Msg("replace kb acl done")
	}
	return inserted, err
}

// ListKBAuthorization returns ACL rows grouped by (grantee_type, grantee_id).
func (s *Store) ListKBAuthorization(kbID string) []AuthorizationSubjectGrant {
	rows := s.ListACL(ResourceTypeKB, kbID, "")
	log.Logger.Info().Str("kb_id", kbID).Any("acl_rows", rows).Msg("list kb authorization acl rows")
	type key struct {
		t string
		i string
	}
	m := map[key]map[string]struct{}{}
	for _, r := range rows {
		k := key{t: canonicalGranteeType(r.GranteeType), i: r.GranteeID}
		if _, ok := m[k]; !ok {
			m[k] = map[string]struct{}{}
		}
		np := normalizePermission(ResourceTypeKB, r.Permission)
		if np == "" || np == PermNone {
			continue
		}
		m[k][np] = struct{}{}
	}
	out := make([]AuthorizationSubjectGrant, 0, len(m))
	for k, perms := range m {
		items := make([]string, 0, len(perms))
		for p := range perms {
			items = append(items, p)
		}
		sort.Strings(items)
		out = append(out, AuthorizationSubjectGrant{
			GranteeType: k.t,
			GranteeID:   k.i,
			Permissions: items,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GranteeType != out[j].GranteeType {
			return out[i].GranteeType < out[j].GranteeType
		}
		return out[i].GranteeID < out[j].GranteeID
	})
	return out
}

// ListKnownUserIDs returns user IDs that are known to ACL store.
// Since Core has no user profile table, this is assembled from ACL rows and group memberships.
func (s *Store) ListKnownUserIDs() []string {
	seen := map[string]struct{}{}
	var ids []string
	_ = s.db.Model(&orm.ACLModel{}).Where("grantee_type = ?", GranteeUser).Distinct("target_id").Pluck("target_id", &ids).Error
	for _, id := range ids {
		if strings.TrimSpace(id) != "" {
			seen[id] = struct{}{}
		}
	}
	ids = ids[:0]
	_ = s.db.Model(&orm.UserGroupModel{}).Distinct("user_id").Pluck("user_id", &ids).Error
	for _, id := range ids {
		if strings.TrimSpace(id) != "" {
			seen[id] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// AllKBIDs textKnowledge base id（text acl_kbs text acl_visibility，text）。
func (s *Store) AllKBIDs() []string {
	seen := make(map[string]bool)
	var ids []string
	s.db.Model(&orm.KBModel{}).Pluck("id", &ids)
	for _, id := range ids {
		seen[id] = true
	}
	s.db.Model(&orm.VisibilityModel{}).Distinct("resource_id").Pluck("resource_id", &ids)
	for _, id := range ids {
		seen[id] = true
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	return out
}
