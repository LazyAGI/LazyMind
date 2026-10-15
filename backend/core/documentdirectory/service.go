// Package documentdirectory owns user-scoped views of source resources.
package documentdirectory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/asyncjob"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/maintenance"
)

const PlanJob = "document_directory_plan"
const OrganizeJob = "document_directory_organize"

var ErrBusy = errors.New("document directory is organizing")
var ErrCapacity = errors.New("document directory capacity is busy")
var ErrPinLimit = errors.New("document directory pin limit reached")
var ErrInput = errors.New("invalid document directory input")

type Candidate struct {
	Source         string          `json:"source"`
	Namespace      string          `json:"namespace"`
	ResourceID     string          `json:"resource_id"`
	Title          string          `json:"title"`
	Locator        json.RawMessage `json:"locator"`
	Excerpt        string          `json:"excerpt,omitempty"`
	ReadState      string          `json:"read_state"`
	AccessState    string          `json:"access_state"`
	ContentVersion string          `json:"content_version,omitempty"`
	ReadAt         *time.Time      `json:"read_at,omitempty"`
}

type Observation struct {
	InventoryID string          `json:"inventory_id,omitempty"`
	CallID      string          `json:"call_id"`
	Source      string          `json:"source"`
	Operation   string          `json:"operation"`
	Status      string          `json:"status"`
	Scope       json.RawMessage `json:"scope"`
	Candidates  []Candidate     `json:"candidates"`
	Incomplete  bool            `json:"incomplete"`
}

type Input struct {
	ConversationID    string                       `json:"conversation_id,omitempty"`
	Observations      []orm.DocumentObservation    `json:"observations"`
	Entries           []orm.DocumentDirectoryEntry `json:"entries,omitempty"`
	Instruction       string                       `json:"instruction,omitempty"`
	FolderInventoryID string                       `json:"folder_inventory_id,omitempty"`
	FolderOffset      int                          `json:"folder_offset,omitempty"`
	FolderBatch       bool                         `json:"folder_batch,omitempty"`
	Manual            bool                         `json:"manual"`
}

type Detail struct {
	Total       int64                        `json:"total"`
	GroupCounts map[string]int64             `json:"group_counts"`
	Directory   orm.DocumentDirectory        `json:"directory"`
	Groups      []orm.DocumentDirectoryGroup `json:"groups"`
	Entries     []orm.DocumentDirectoryEntry `json:"entries"`
	Folder      *orm.DocumentFolderInventory `json:"folder,omitempty"`
	Organizing  bool                         `json:"organizing"`
}

func encoded(v any) json.RawMessage  { b, _ := json.Marshal(v); return b }
func key(v any) string               { h := sha256.Sum256(encoded(v)); return hex.EncodeToString(h[:]) }
func resourceKey(c Candidate) string { return key([]string{c.Source, c.Namespace, c.ResourceID}) }

func validCandidate(c Candidate) bool {
	switch c.Source {
	case "local", "agent_conversations", "feishu", "notion", "google_drive", "mail":
	default:
		return false
	}
	if strings.TrimSpace(c.Namespace) == "" || strings.TrimSpace(c.ResourceID) == "" || len(c.ResourceID) > 8192 || len(c.Namespace) > 2048 || len(c.Title) > 4096 || len(c.Excerpt) > 12000 || !json.Valid(c.Locator) {
		return false
	}
	switch c.ReadState {
	case "unread", "partial", "read", "unknown":
	default:
		return false
	}
	switch c.AccessState {
	case "unknown", "accessible", "unavailable", "denied", "deleted":
	default:
		return false
	}
	return true
}

func busy(tx *gorm.DB, uid, id string) (bool, error) {
	var n int64
	err := tx.Model(&orm.AsyncJob{}).Where("create_user_id=? AND job_type=? AND resource_id=? AND status=? AND lock_until>?", uid, OrganizeJob, id, asyncjob.StatusRunning, time.Now().UTC()).Count(&n).Error
	return n > 0, err
}

// A directory used by a live Main Chat execution is protected even when a long
// tool/model call outlasts the short browser-view grace period.
func activeUse(tx *gorm.DB, uid, id string) (bool, error) {
	var observations []orm.DocumentObservation
	if err := tx.Where("user_id=? AND call_id=?", uid, "use:"+id).Find(&observations).Error; err != nil {
		return false, err
	}
	for _, o := range observations {
		var n int64
		if err := tx.Model(&orm.ChatHistory{}).Where("id=? AND run_id=? AND run_status=?", o.HistoryID, o.RunID, "generating").Count(&n).Error; err != nil {
			return false, err
		}
		if n > 0 {
			return true, nil
		}
	}
	return false, nil
}

func requireEditable(tx *gorm.DB, uid, id string, version int64) (orm.DocumentDirectory, error) {
	var d orm.DocumentDirectory
	if err := tx.Where("id=? AND user_id=? AND ready=?", id, uid, true).Take(&d).Error; err != nil {
		return d, err
	}
	if b, err := busy(tx, uid, id); err != nil {
		return d, err
	} else if b {
		return d, ErrBusy
	}
	if version != 0 && d.Version != version {
		return d, ErrBusy
	}
	return d, nil
}

func Get(ctx context.Context, db *gorm.DB, uid, id string, page ...EntryPageQuery) (Detail, error) {
	var detail Detail
	err := mutate(ctx, db, uid, func(tx *gorm.DB) error {
		var err error
		detail, err = getSnapshot(ctx, tx, uid, id, page...)
		return err
	})
	return detail, err
}
func getSnapshot(ctx context.Context, db *gorm.DB, uid, id string, page ...EntryPageQuery) (Detail, error) {
	d := Detail{Groups: []orm.DocumentDirectoryGroup{}, Entries: []orm.DocumentDirectoryEntry{}}
	if err := db.WithContext(ctx).Where("id=? AND user_id=? AND deleted_at IS NULL AND (ready=? OR id IN (SELECT directory_id FROM document_folder_inventories WHERE user_id=?))", id, uid, true, uid).Take(&d.Directory).Error; err != nil {
		return d, err
	}
	if err := db.WithContext(ctx).Where("directory_id=?", id).Order("position,id").Find(&d.Groups).Error; err != nil {
		return d, err
	}
	if len(page) > 0 {
		if err := entryPage(db.WithContext(ctx), id, page[0], &d); err != nil {
			return d, err
		}
	} else {
		if err := db.WithContext(ctx).Where("directory_id=?", id).Order("created_at,id").Find(&d.Entries).Error; err != nil {
			return d, err
		}
	}
	var folder orm.DocumentFolderInventory
	if err := db.Where("directory_id=? AND user_id=?", id, uid).Take(&folder).Error; err == nil {
		d.Folder = &folder
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return d, err
	}
	var err error
	d.Organizing, err = busy(db.WithContext(ctx), uid, id)
	return d, err
}

func List(ctx context.Context, db *gorm.DB, uid string) ([]orm.DocumentDirectory, error) {
	rows := []orm.DocumentDirectory{}
	err := db.WithContext(ctx).Where("user_id=? AND deleted_at IS NULL AND (ready=? OR id IN (SELECT directory_id FROM document_folder_inventories WHERE user_id=?))", uid, true, uid).Order("pinned DESC, COALESCE(organized_at,created_at) DESC,id").Find(&rows).Error
	return rows, err
}

// All user mutations serialize only their short transactions, never model calls.
func mutate(ctx context.Context, db *gorm.DB, uid string, fn func(*gorm.DB) error) error {
	if strings.TrimSpace(uid) == "" {
		return ErrInput
	}
	return maintenance.UserTransaction(ctx, db, uid, fn)
}

func removeTx(tx *gorm.DB, uid, id string) error {
	if _, err := asyncjob.CancelResourceJobs(context.Background(), tx, OrganizeJob, OrganizeJob, id, "directory_deleted"); err != nil {
		return err
	}
	if err := tx.Model(&orm.DocumentDirectoryBatch{}).Where("user_id=? AND directory_id=? AND status IN ?", uid, id, []string{"pending", "running"}).Updates(map[string]any{"status": "canceled", "updated_at": time.Now().UTC()}).Error; err != nil {
		return err
	}
	if err := tx.Model(&orm.DocumentDirectoryEntry{}).Where("directory_id=?", id).Update("excluded", true).Error; err != nil {
		return err
	}
	if err := tx.Where("directory_id=?", id).Delete(&orm.DocumentDirectoryGroup{}).Error; err != nil {
		return err
	}
	return tx.Model(&orm.DocumentDirectory{}).Where("id=? AND user_id=?", id, uid).Updates(map[string]any{"deleted_at": time.Now().UTC(), "ready": false, "pinned": false}).Error
}

// Called immediately before committing a new visible directory. A failed model
// call can never evict an existing view.
func makeRoom(tx *gorm.DB, uid, exclude string) (string, error) {
	var n int64
	if err := tx.Model(&orm.DocumentDirectory{}).Where("user_id=? AND ready=? AND id<>?", uid, true, exclude).Count(&n).Error; err != nil {
		return "", err
	}
	if n < 20 {
		return "", nil
	}
	var candidates []orm.DocumentDirectory
	if err := tx.Where("user_id=? AND ready=? AND pinned=? AND id<>? AND (use_until IS NULL OR use_until<=?)", uid, true, false, exclude, time.Now().UTC()).Order("last_used_at,id").Find(&candidates).Error; err != nil {
		return "", err
	}
	for _, d := range candidates {
		b, err := busy(tx, uid, d.ID)
		if err != nil {
			return "", err
		}
		if b {
			continue
		}
		if using, err := activeUse(tx, uid, d.ID); err != nil {
			return "", err
		} else if using {
			continue
		}
		return d.Name, removeTx(tx, uid, d.ID)
	}
	return "", ErrCapacity
}

type Command struct {
	ConversationID string     `json:"-"`
	Operation      string     `json:"operation"`
	DirectoryID    string     `json:"directory_id,omitempty"`
	Version        int64      `json:"version,omitempty"`
	Name           string     `json:"name,omitempty"`
	Intent         string     `json:"intent,omitempty"`
	Organization   string     `json:"organization,omitempty"`
	AutoAdd        *bool      `json:"auto_add,omitempty"`
	Pinned         *bool      `json:"pinned,omitempty"`
	GroupID        string     `json:"group_id,omitempty"`
	ParentID       string     `json:"parent_id,omitempty"`
	EntryID        string     `json:"entry_id,omitempty"`
	Candidate      *Candidate `json:"candidate,omitempty"`
	BatchID        string     `json:"batch_id,omitempty"`
	Instruction    string     `json:"instruction,omitempty"`
}

func Execute(ctx context.Context, db *gorm.DB, uid string, c Command) (map[string]any, error) {
	result := map[string]any{}
	err := mutate(ctx, db, uid, func(tx *gorm.DB) error {
		now := time.Now().UTC()
		if c.Operation == "acknowledge" {
			return tx.Model(&orm.DocumentDirectoryBatch{}).Where("id=? AND user_id=?", c.BatchID, uid).Update("acknowledged", true).Error
		}
		if c.Operation == "retry" || c.Operation == "cancel" {
			return batchAction(ctx, tx, uid, c, result)
		}
		if c.Operation == "create" {
			if strings.TrimSpace(c.Name) == "" || len(c.Name) > 255 || strings.TrimSpace(c.Intent) == "" || len(c.Intent) > 4000 {
				return ErrInput
			}
			if c.Organization == "" {
				c.Organization = "按资料类型组织"
			}
			evicted, err := makeRoom(tx, uid, "")
			if err != nil {
				return err
			}
			d := orm.DocumentDirectory{ID: common.GenerateID(), UserID: uid, Name: c.Name, Intent: c.Intent, Organization: c.Organization, AutoAdd: true, Ready: true, Version: 1, LastUsedAt: now, CreatedAt: now, UpdatedAt: now, CoverageJSON: encoded([]any{})}
			result["directory"], result["evicted"] = d, evicted
			return tx.Create(&d).Error
		}
		var d orm.DocumentDirectory
		if c.Operation == "touch" {
			if err := tx.Where("id=? AND user_id=? AND ready=?", c.DirectoryID, uid, true).Take(&d).Error; err != nil {
				return err
			}
			until := now.Add(2 * time.Minute)
			return tx.Model(&d).Updates(map[string]any{"last_used_at": now, "use_until": until}).Error
		}
		if c.Operation == "delete" {
			pending, e := requireDirectory(tx, uid, c.DirectoryID)
			if e != nil {
				return e
			}
			if !pending.Ready {
				var n int64
				if e = tx.Model(&orm.DocumentFolderInventory{}).Where("directory_id=? AND user_id=?", pending.ID, uid).Count(&n).Error; e != nil {
					return e
				}
				if n == 0 {
					return ErrInput
				}
				return removeTx(tx, uid, pending.ID)
			}
		}
		var err error
		d, err = requireEditable(tx, uid, c.DirectoryID, c.Version)
		if err != nil {
			return err
		}
		switch c.Operation {
		case "delete":
			return removeTx(tx, uid, d.ID)
		case "update":
			if len(c.Name) > 255 || len(c.Intent) > 4000 {
				return ErrInput
			}
			if c.Name != "" {
				d.Name = c.Name
			}
			if c.Intent != "" {
				d.Intent = c.Intent
			}
			if c.AutoAdd != nil {
				d.AutoAdd = *c.AutoAdd
			}
			if c.Pinned != nil && *c.Pinned && !d.Pinned {
				var n int64
				if err := tx.Model(&orm.DocumentDirectory{}).Where("user_id=? AND pinned=?", uid, true).Count(&n).Error; err != nil {
					return err
				}
				if n >= 10 {
					return ErrPinLimit
				}
			}
			if c.Pinned != nil {
				d.Pinned = *c.Pinned
			}
		case "delete_group":
			var count int64
			if err := tx.Model(&orm.DocumentDirectoryEntry{}).Where("directory_id=? AND group_id=? AND excluded=?", d.ID, c.GroupID, false).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return ErrInput
			}
			if err := tx.Model(&orm.DocumentDirectoryGroup{}).Where("directory_id=? AND parent_id=?", d.ID, c.GroupID).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return ErrInput
			}
			if err := tx.Where("id=? AND directory_id=?", c.GroupID, d.ID).Delete(&orm.DocumentDirectoryGroup{}).Error; err != nil {
				return err
			}
		case "group":
			if strings.TrimSpace(c.Name) == "" || len(c.Name) > 255 {
				return ErrInput
			}
			if err := validateParent(tx, d.ID, c.GroupID, c.ParentID); err != nil {
				return err
			}
			g := orm.DocumentDirectoryGroup{ID: c.GroupID, DirectoryID: d.ID, Name: c.Name, ParentID: c.ParentID}
			if g.ID == "" {
				g.ID = common.GenerateID()
				err = tx.Create(&g).Error
			} else {
				var old orm.DocumentDirectoryGroup
				if err = tx.Where("id=? AND directory_id=?", g.ID, d.ID).Take(&old).Error; err == nil {
					g.Position = old.Position
					err = tx.Save(&g).Error
				}
			}
			if err != nil {
				return err
			}
			result["group"] = g
		case "entry", "entry_pin", "exclude", "restore":
			var e orm.DocumentDirectoryEntry
			if c.Candidate != nil && c.EntryID == "" && c.Operation == "entry" {
				if !validCandidate(*c.Candidate) {
					return ErrInput
				}
				err = tx.Where("directory_id=? AND resource_key=?", d.ID, resourceKey(*c.Candidate)).Take(&e).Error
				if errors.Is(err, gorm.ErrRecordNotFound) {
					e = entryFromCandidate(d.ID, *c.Candidate, now)
				} else if err != nil {
					return err
				}
				if e.Excluded {
					return ErrInput
				}
			} else if err := tx.Where("id=? AND directory_id=?", c.EntryID, d.ID).Take(&e).Error; err != nil {
				return err
			}
			if c.Operation == "entry_pin" {
				if c.Pinned == nil {
					return ErrInput
				}
				e.Pinned = *c.Pinned
			} else if c.Operation == "exclude" {
				e.Excluded = true
			} else if c.Operation == "restore" {
				e.Excluded = false
			} else {
				if e.Excluded {
					return ErrInput
				}
				if err := validateParent(tx, d.ID, "", c.GroupID); err != nil {
					return err
				}
				e.GroupID = c.GroupID
				e.Pinned = true
				e.Confirmed = true
				if c.Pinned != nil {
					e.Pinned = *c.Pinned
				}
			}
			e.UpdatedAt = now
			if err := tx.Save(&e).Error; err != nil {
				return err
			}
			result["entry"] = e
		case "organize", "reorganize":
			return enqueueManual(ctx, tx, uid, d, c, result)
		default:
			return ErrInput
		}
		d.Version++
		d.LastUsedAt = now
		d.UpdatedAt = now
		result["directory"] = d
		return tx.Save(&d).Error
	})
	return result, err
}

func validateParent(tx *gorm.DB, dir, id, parent string) error {
	seen := map[string]bool{id: true}
	for parent != "" {
		if seen[parent] {
			return ErrInput
		}
		seen[parent] = true
		var g orm.DocumentDirectoryGroup
		if err := tx.Where("id=? AND directory_id=?", parent, dir).Take(&g).Error; err != nil {
			return err
		}
		parent = g.ParentID
	}
	return nil
}

func entryFromCandidate(dir string, c Candidate, now time.Time) orm.DocumentDirectoryEntry {
	return orm.DocumentDirectoryEntry{ID: common.GenerateID(), DirectoryID: dir, ResourceKey: resourceKey(c), Source: c.Source, Namespace: c.Namespace, ResourceID: c.ResourceID, LocatorJSON: c.Locator, Title: c.Title, ReadState: c.ReadState, AccessState: c.AccessState, ContentVersion: c.ContentVersion, ReadAt: c.ReadAt, CreatedAt: now, UpdatedAt: now}
}
