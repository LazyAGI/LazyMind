package doc

import (
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"lazymind/core/acl"
	"lazymind/core/common/orm"
	"lazymind/core/learning"
	"strings"
	"time"
)

func (s *ResourceService) prepareKnowledgeCreate(ctx context.Context, op ResourceOperationContext, req CreateKnowledgeRequest) (orm.Dataset, []learning.CapabilityRef, error) {
	if err := resourceAccount(op.Caller); err != nil {
		return orm.Dataset{}, nil, err
	}
	if err := validateDatasetDisplayName(req.Name); err != nil {
		return orm.Dataset{}, nil, resourceErr("INVALID_ARGUMENT", err.Error())
	}
	if hasReservedDatasetDisplayNamePrefix(req.Name) {
		return orm.Dataset{}, nil, resourceErr("INVALID_ARGUMENT", "reserved name")
	}
	scene, err := resourceScene(req.Scene)
	if err != nil {
		return orm.Dataset{}, nil, err
	}
	level, err := knowledgeProcessingLevel(ctx, s.db, op.Caller.UserID, req.ProcessingLevel)
	if err != nil {
		return orm.Dataset{}, nil, err
	}
	now := time.Now().UTC()
	id := newDatasetID()
	if req.PreferredID != "" {
		id = req.PreferredID
	}
	if op.OperationKey != "" {
		id = "ds_" + resourceHash([]string{resourceTenant(op.Caller.TenantID), op.Caller.UserID, op.OperationKey})[:32]
	}
	ds := orm.Dataset{ID: id, KbID: id, DisplayName: req.Name, Desc: req.Description, Scene: scene, ResourceVersion: 1, ResourceUID: id, DatasetInfo: json.RawMessage(`{}`), ProcessingLevel: level, ProcessingRevision: 1, TransitionStatus: TransitionIdle, ProcessingConfig: json.RawMessage(`{}`), EmbeddingModel: "default", EmbeddingModelProvider: "default", TenantID: resourceTenant(op.Caller.TenantID), Type: 1, Ext: json.RawMessage(`{}`), BaseModel: orm.BaseModel{CreateUserID: op.Caller.UserID, CreateUserName: op.Caller.UserName, CreatedAt: now, UpdatedAt: now}}
	options := Dataset{}
	if req.PageOptions != nil {
		options = *req.PageOptions
	}
	if req.Tags != nil {
		options.Tags = req.Tags
	}
	tags, err := normalizeAndValidateDatasetTags(options.Tags)
	if err != nil {
		return orm.Dataset{}, nil, err
	}
	if len(tags) == 0 {
		return orm.Dataset{}, nil, resourceErr("INVALID_ARGUMENT", "at least one knowledge base tag is required")
	}
	if err := validateKnowledgeDescription(req.Description); err != nil {
		return orm.Dataset{}, nil, err
	}
	algoID := req.AlgoID
	if algoID == "" {
		algoID = options.Algo.AlgoID
	}
	options.Algo, err = knowledgeAlgorithm(ctx, algoID)
	if err != nil {
		return orm.Dataset{}, nil, err
	}
	refs, err := knowledgeCapabilities(ctx, s.db, op.Caller.UserID, scene, req.ProfileID, req.Capabilities)
	if err != nil {
		return orm.Dataset{}, nil, err
	}
	if err := s.knowledgeNameConflict(ctx, s.db, op, req.Name, ""); err != nil {
		return orm.Dataset{}, nil, err
	}
	parsers := fetchParsersByAlgoID(ctx, options.Algo.AlgoID)
	ds.CoverImage = strings.TrimSpace(options.CoverImage)
	ds.ReaderFallbackAccepted = options.ReaderFallbackAccepted
	ds.Ext, _ = json.Marshal(map[string]any{"tags": tags, "algo_id": options.Algo.AlgoID, "algo_name": options.Algo.DisplayName, "parsers": parsers, "profile_id": req.ProfileID})

	return ds, refs, nil
}

func (s *ResourceService) CreateKnowledge(ctx context.Context, op ResourceOperationContext, req CreateKnowledgeRequest) (ResourceOperationResult, error) {
	if err := resourceAccount(op.Caller); err != nil {
		return ResourceOperationResult{}, err
	}
	if err := validateDatasetDisplayName(req.Name); err != nil {
		return ResourceOperationResult{}, resourceErr("INVALID_ARGUMENT", err.Error())
	}
	if hasReservedDatasetDisplayNamePrefix(req.Name) {
		return ResourceOperationResult{}, resourceErr("INVALID_ARGUMENT", "reserved name")
	}
	// Resolve replay before external provisioning. The transaction checks again below.
	if out, found, err := s.replay(ctx, op, "knowledge.create", req); found || err != nil {
		return out, err
	}
	ds, refs, err := s.prepareKnowledgeCreate(ctx, op, req)
	if err != nil {
		return ResourceOperationResult{}, err
	}
	if op.ExpectedSnapshot != "" && op.ExpectedSnapshot != knowledgeCreateDigest(ds, refs) {
		return ResourceOperationResult{}, resourceErr("CONFLICT", "creation configuration changed; confirm again")
	}

	if err := s.provision(ctx, op.Caller, &ds); err != nil {
		return ResourceOperationResult{}, err
	}
	return s.mutate(ctx, op, "knowledge.create", req, func(tx *gorm.DB) (ResourceOperationResult, error) {
		if err := s.knowledgeNameConflict(ctx, tx, op, req.Name, ""); err != nil {
			return ResourceOperationResult{}, err
		}
		if err := tx.Create(&ds).Error; err != nil {
			return ResourceOperationResult{}, err
		}
		if err := tx.Create(&orm.KBModel{ID: ds.KbID, Name: ds.DisplayName, OwnerID: op.Caller.UserID, Visibility: acl.VisibilityPrivate}).Error; err != nil {
			return ResourceOperationResult{}, err
		}
		if err := learning.SaveKnowledgeBaseCapabilities(tx, op.Caller.UserID, ds.ID, refs); err != nil {
			return ResourceOperationResult{}, err
		}
		for _, permission := range roleToPermissions("dataset_maintainer") {
			grant := orm.ACLModel{ResourceType: acl.ResourceTypeDB, ResourceID: ds.ID, GranteeType: acl.GranteeUser, TargetID: op.Caller.UserID, Permission: permission, CreatedBy: op.Caller.UserID, CreatedAt: ds.CreatedAt}
			if err := tx.Create(&grant).Error; err != nil {
				return ResourceOperationResult{}, err
			}
		}
		return ResourceOperationResult{Items: []ResourceOperationItem{{ResourceRef: ResourceRef{Kind: "knowledge_base", KnowledgeBaseID: ds.ID}, Name: ds.DisplayName, ResourceStatus: "created", EntryURL: "/lib/knowledge/detail/" + ds.ID, After: map[string]any{"description": ds.Desc, "scene": ds.Scene, "processing_level": ds.ProcessingLevel, "tags": parseDatasetTags(ds.Ext), "algo_id": parseDatasetAlgo(ds.Ext).AlgoID, "profile_id": req.ProfileID, "capabilities": refs}}}}, nil
	})
}
func (s *ResourceService) knowledgeNameConflict(ctx context.Context, tx *gorm.DB, op ResourceOperationContext, name, exclude string) error {
	var rows []orm.Dataset
	if err := tx.Where("display_name = ? AND id <> ? AND deleted_at IS NULL", name, exclude).Limit(s.budget.MaxResources + 1).Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) > s.budget.MaxResources {
		return resourceErr("OPERATION_TOO_LARGE", "too many name candidates")
	}
	for _, ds := range rows {
		if resourceTenant(ds.TenantID) != resourceTenant(op.Caller.TenantID) {
			continue
		}
		perms := []string{}
		if ds.CreateUserID == op.Caller.UserID {
			return resourceErr("CONFLICT", "dataset name already exists")
		} else {
			perms, _ = acl.PermissionsForWithDB(tx, acl.ResourceTypeDB, ds.ID, op.Caller.UserID, resourceTransactionGroups(tx))
		}
		if len(perms) > 0 {
			if err := s.checkSource(ctx, tx, op.Caller, ds.ID, acl.PermissionDatasetRead); err != nil {
				return err
			}
			return resourceErr("CONFLICT", "dataset name already exists")
		}
	}
	return nil
}
func (s *ResourceService) CreateFolders(ctx context.Context, op ResourceOperationContext, req CreateFoldersRequest) (ResourceOperationResult, error) {
	if _, err := expandedFolderPaths(req, s.budget.MaxResources); err != nil {
		return ResourceOperationResult{}, err
	}
	return s.mutate(ctx, op, "knowledge.folder.create", req, func(tx *gorm.DB) (ResourceOperationResult, error) {
		var out ResourceOperationResult
		ds, err := s.dataset(ctx, tx, op, req.KnowledgeBaseID, acl.PermissionDatasetUpload)
		if err != nil {
			return out, err
		}
		if op.ExpectedSnapshot != "" && folderCreateDigest(ds, req) != op.ExpectedSnapshot {
			return out, resourceErr("CONFLICT", "creation destination changed; confirm again")
		}
		if err = lockResourceDataset(tx, ds); err != nil {
			return out, err
		}
		if err = resourceParent(tx, ds.ID, req.ParentID); err != nil {
			return out, err
		}
		created := map[string]string{}
		for _, path := range req.Folders {
			if strings.HasPrefix(path, "/") || strings.Contains(path, "\\") || strings.Contains(path, ":") {
				return out, resourceErr("INVALID_ARGUMENT", "relative folder path required")
			}
			parts := strings.Split(path, "/")
			pid := req.ParentID
			for _, name := range parts {
				if name == "" || name == "." || name == ".." || strings.TrimSpace(name) != name || len(name) > 512 {
					return out, resourceErr("INVALID_ARGUMENT", "invalid folder path")
				}
				key := pid + "/" + name
				if existing := created[key]; existing != "" {
					pid = existing
					continue
				}
				if len(created) >= s.budget.MaxResources {
					return out, resourceErr("OPERATION_TOO_LARGE", "too many expanded folders")
				}
				if err = resourceNameConflict(tx, ds.ID, pid, name, ""); err != nil {
					return out, err
				}
				now := time.Now().UTC()
				folderID := newDocID()
				if req.PreferredRootID != "" {
					if len(req.Folders) != 1 || len(parts) != 1 {
						return out, resourceErr("INVALID_ARGUMENT", "explicit folder ID requires one folder")
					}
					folderID = req.PreferredRootID
				}
				row := orm.Document{ID: folderID, DatasetID: ds.ID, DisplayName: name, DocumentType: "FOLDER", PID: pid, ResourceVersion: 1, Tags: json.RawMessage(`[]`), Ext: json.RawMessage(`{}`), BaseModel: orm.BaseModel{CreateUserID: op.Caller.UserID, CreateUserName: op.Caller.UserName, CreatedAt: now, UpdatedAt: now}}
				if err = tx.Create(&row).Error; err != nil {
					return out, err
				}
				out.Items = append(out.Items, resourceItem(row, "created"))
				created[key] = row.ID
				pid = row.ID
			}
		}
		parents := []string{req.ParentID}
		for _, item := range out.Items {
			parents = append(parents, item.ResourceRef.ID())
		}
		if err = refreshFolderStats(tx, map[string][]string{ds.ID: parents}); err != nil {
			return out, err
		}
		return out, nil
	})
}
func (s *ResourceService) UpdateMetadata(ctx context.Context, op ResourceOperationContext, req UpdateMetadataRequest) (ResourceOperationResult, error) {
	if len(req.Items) == 0 {
		return ResourceOperationResult{}, resourceErr("INVALID_ARGUMENT", "items required")
	}
	if len(req.Items) > s.budget.MaxResources {
		return ResourceOperationResult{}, resourceErr("OPERATION_TOO_LARGE", "too many metadata items")
	}
	if err := resourceAccount(op.Caller); err != nil {
		return ResourceOperationResult{}, err
	}
	if saved, found, err := s.replay(ctx, op, "knowledge.resource.update_metadata", req); found || err != nil {
		return saved, err
	}
	// Preflight external readiness before opening a database transaction.
	prepared := make([]ResourceMetadataChanges, len(req.Items))
	for i, item := range req.Items {
		c := item.Changes
		if _, err := s.dataset(ctx, s.db, op, item.Target.KnowledgeBaseID, acl.PermissionDatasetWrite); err != nil {
			return ResourceOperationResult{}, err
		}
		if c.ProcessingLevel != nil {
			if *c.ProcessingLevel == "" {
				return ResourceOperationResult{}, resourceErr("INVALID_ARGUMENT", "processing_level cannot be empty")
			}
			level, err := knowledgeProcessingLevel(ctx, s.db, op.Caller.UserID, *c.ProcessingLevel)
			if err != nil {
				return ResourceOperationResult{}, err
			}
			c.ProcessingLevel = &level
		}
		if c.AlgoID != nil {
			algo, err := knowledgeAlgorithm(ctx, *c.AlgoID)
			if err != nil {
				return ResourceOperationResult{}, err
			}
			c.AlgoID = &algo.AlgoID
			c.algorithm = &algo
			c.parsers = fetchParsersByAlgoID(ctx, algo.AlgoID)
		}
		if c.Description != nil {
			if err := validateKnowledgeDescription(*c.Description); err != nil {
				return ResourceOperationResult{}, err
			}
		}
		prepared[i] = c
	}
	committed := false
	result, err := s.mutate(ctx, op, "knowledge.resource.update_metadata", req, func(tx *gorm.DB) (ResourceOperationResult, error) {
		var out ResourceOperationResult
		for index, item := range req.Items {
			c := prepared[index]
			if c.Name == nil && c.Description == nil && c.Scene == nil && c.Tags == nil && c.ProfileID == nil && c.AlgoID == nil && c.ProcessingLevel == nil && c.Capabilities == nil && c.ReaderFallbackAccepted == nil && c.CoverImage == nil && c.PageParsers == nil {
				return out, resourceErr("INVALID_ARGUMENT", "changes cannot be empty")
			}
			if c.Name != nil && strings.TrimSpace(*c.Name) == "" {
				return out, resourceErr("INVALID_ARGUMENT", "name cannot be empty")
			}
			ds, err := s.dataset(ctx, tx, op, item.Target.KnowledgeBaseID, acl.PermissionDatasetWrite)
			if err != nil {
				return out, err
			}
			if err = lockResourceDataset(tx, ds); err != nil {
				return out, err
			}
			updates := map[string]any{"updated_at": time.Now().UTC()}
			before := map[string]any{}
			after := map[string]any{}
			var result ResourceOperationItem
			if item.Target.Kind == "knowledge_base" {
				if err := updateKnowledgeConfiguration(ctx, tx, op.Caller.UserID, &ds, c, updates, before, after); err != nil {
					return out, err
				}
				if c.Name != nil {
					n := strings.TrimSpace(*c.Name)
					if err = validateDatasetDisplayName(n); err != nil {
						return out, resourceErr("INVALID_ARGUMENT", err.Error())
					}
					if err = s.knowledgeNameConflict(ctx, tx, op, n, ds.ID); err != nil {
						return out, err
					}
					updates["display_name"] = n
					before["name"] = ds.DisplayName
					after["name"] = n
					ds.DisplayName = n
				}
				if c.Description != nil {
					updates["desc"] = *c.Description
					before["description"] = ds.Desc
					after["description"] = *c.Description
				}
				if c.Scene != nil {
					if *c.Scene == "" {
						return out, resourceErr("INVALID_ARGUMENT", "scene cannot be empty")
					}
					v, e := resourceScene(*c.Scene)
					if e != nil {
						return out, e
					}
					updates["scene"] = v
					before["scene"] = ds.Scene
					after["scene"] = v
				}
				if err = tx.Model(&orm.Dataset{}).Where("id = ?", ds.ID).Updates(updates).Error; err != nil {
					return out, err
				}
				result = ResourceOperationItem{ResourceRef: item.Target, Name: ds.DisplayName, ResourceStatus: "updated", EntryURL: "/lib/knowledge/detail/" + ds.ID}
			} else {
				row, e := resourceDocument(tx, item.Target)
				if e != nil {
					return out, e
				}
				if isFolderLikeDocument(row) && c.Tags != nil {
					if !c.Recursive || c.Name != nil || c.Scene != nil || c.Description != nil || c.Capabilities != nil || c.ProfileID != nil || c.AlgoID != nil || c.ProcessingLevel != nil {
						return out, resourceErr("INVALID_ARGUMENT", "folder tags require recursive=true and only tag changes")
					}
					tree, e := s.tree(ctx, tx, op, item.Target)
					if e != nil {
						return out, e
					}
					for _, child := range tree.rows {
						if isFolderLikeDocument(child) {
							continue
						}
						changes, prior, next, e := resourceDocumentChanges(tx, child, ResourceMetadataChanges{Tags: c.Tags, TagMode: c.TagMode}, child.PID)
						if e != nil {
							return out, e
						}
						changes["resource_version"] = gorm.Expr("resource_version + 1")
						changes["updated_at"] = time.Now().UTC()
						changed := tx.Model(&orm.Document{}).Where("id = ? AND resource_version = ? AND deleted_at IS NULL", child.ID, child.ResourceVersion).Updates(changes)
						if changed.Error != nil {
							return out, changed.Error
						}
						if changed.RowsAffected != 1 {
							return out, resourceErr("CONFLICT", "resource changed")
						}
						if len(out.Items) >= s.budget.MaxResources {
							return out, resourceErr("OPERATION_TOO_LARGE", "too many metadata resources")
						}
						value := resourceItem(child, "updated")
						value.Before = prior
						value.After = next
						out.Items = append(out.Items, value)
					}
					continue
				}
				if c.Recursive {
					return out, resourceErr("INVALID_ARGUMENT", "recursive applies to folder tags only")
				}
				changes, prior, next, e := resourceDocumentChanges(tx, row, c, row.PID)
				if e != nil {
					return out, e
				}
				for key, value := range changes {
					updates[key] = value
				}
				before, after = prior, next
				if name, ok := changes["display_name"].(string); ok {
					row.DisplayName = name
				}
				updates["resource_version"] = gorm.Expr("resource_version + 1")
				r := tx.Model(&orm.Document{}).Where("id = ? AND resource_version = ? AND deleted_at IS NULL", row.ID, row.ResourceVersion).Updates(updates)
				if r.Error != nil {
					return out, r.Error
				}
				if r.RowsAffected != 1 {
					return out, resourceErr("CONFLICT", "resource changed")
				}
				result = resourceItem(row, "updated")
			}
			result.Before = before
			result.After = after
			out.Items = append(out.Items, result)
		}
		committed = true
		return out, nil
	})
	if err != nil || !committed {
		return result, err
	}
	for i, item := range result.Items {
		if item.ResourceRef.Kind == "knowledge_base" {
			_, algoChanged := item.After["algo_id"]
			_, nameChanged := item.After["name"]
			_, descChanged := item.After["description"]
			_, tagsChanged := item.After["tags"]
			if !algoChanged && !nameChanged && !descChanged && !tagsChanged {
				continue
			}
			if syncErr := s.syncKnowledgeMetadata(ctx, op.Caller, item.ResourceRef.KnowledgeBaseID, algoChanged); syncErr != nil {
				result.Items[i].Error = resourceErr("METADATA_SYNC_FAILED", syncErr.Error())
			}
		}
	}
	s.updateReceiptResult(ctx, op, result)
	return result, nil
}

// resourceDocumentChanges is shared by metadata edits and combined legacy moves.
func resourceDocumentChanges(tx *gorm.DB, row orm.Document, c ResourceMetadataChanges, parent string) (map[string]any, map[string]any, map[string]any, error) {
	updates, before, after := map[string]any{}, map[string]any{}, map[string]any{}
	if c.Description != nil || c.Scene != nil || c.ProfileID != nil || c.AlgoID != nil || c.ProcessingLevel != nil || c.Capabilities != nil || c.ReaderFallbackAccepted != nil || (isFolderLikeDocument(row) && c.Tags != nil) {
		return nil, nil, nil, resourceErr("INVALID_ARGUMENT", "field is not allowed for resource kind")
	}
	if c.Name != nil {
		name := strings.TrimSpace(*c.Name)
		if name == "" || strings.ContainsAny(name, "/\\") || len(name) > 512 {
			return nil, nil, nil, resourceErr("INVALID_ARGUMENT", "invalid resource name")
		}
		if err := resourceNameConflict(tx, row.DatasetID, parent, name, row.ID); err != nil {
			return nil, nil, nil, err
		}
		updates["display_name"], before["name"], after["name"] = name, row.DisplayName, name
	}
	if c.Tags != nil {
		tags := normalizeBatchDocumentTags(*c.Tags)
		if c.TagMode == "append" {
			var existing []string
			_ = json.Unmarshal(row.Tags, &existing)
			tags = mergeAppendDocumentTags(existing, tags, 10)
		} else if c.TagMode != "" && c.TagMode != "replace" {
			return nil, nil, nil, resourceErr("INVALID_ARGUMENT", "tag_mode must be append or replace")
		}
		if len(tags) > 10 {
			tags = tags[:10]
		}
		raw, _ := json.Marshal(tags)
		updates["tags"], before["tags"], after["tags"] = raw, row.Tags, tags
	}
	return updates, before, after, nil
}
