package doc

import (
	"context"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"strings"
)

// DocumentCatalog is the shared, ACL-checked resource discovery service. The
// catalog dependency is also used by the page API, keeping permission semantics
// independent of the caller's transport.
type DocumentCatalog struct {
	db       *gorm.DB
	datasets interface {
		GetDataset(context.Context, DatasetGetRequest) (Dataset, error)
	}
}

func NewDocumentCatalog(db *gorm.DB, datasets interface {
	GetDataset(context.Context, DatasetGetRequest) (Dataset, error)
}) *DocumentCatalog {
	return &DocumentCatalog{db: db, datasets: datasets}
}

type DocumentCatalogQuery struct {
	DatasetIDs        []string
	ResourceIDs       []string
	ResourceType      string
	ParentID          *string
	Recursive         bool
	Match, Name, Path string
	Offset, Limit     int
}
type DocumentCatalogPage struct {
	Datasets     map[string]Dataset
	Rows         []orm.Document
	Total        int64
	LogicalPaths map[string]string
}

func (s *DocumentCatalog) List(ctx context.Context, caller DatasetCatalogCaller, q DocumentCatalogQuery) (DocumentCatalogPage, error) {
	out := DocumentCatalogPage{Datasets: map[string]Dataset{}, Rows: []orm.Document{}, LogicalPaths: map[string]string{}}
	if len(q.DatasetIDs) == 0 || len(q.DatasetIDs) > 20 || q.Limit < 1 || q.Limit > 100 || q.Offset < 0 {
		return out, &ResourceError{Code: "INVALID_ARGUMENT", Message: "invalid query scope or page"}
	}
	if q.ParentID != nil && len(q.DatasetIDs) != 1 {
		return out, &ResourceError{Code: "INVALID_ARGUMENT", Message: "parent_id requires a single library"}
	}
	for _, id := range q.DatasetIDs {
		dataset, err := s.datasets.GetDataset(ctx, DatasetGetRequest{UserID: caller.UserID, DatasetID: id, Caller: caller})
		if err != nil {
			return out, err
		}
		out.Datasets[id] = dataset
	}
	base := s.db.WithContext(ctx).Model(&orm.Document{}).Where("dataset_id IN ? AND deleted_at IS NULL", q.DatasetIDs)
	switch q.ResourceType {
	case "", "document":
		base = base.Where("UPPER(COALESCE(document_type,'')) <> 'FOLDER'")
	case "folder":
		base = base.Where("UPPER(document_type) = 'FOLDER'")
	case "all":
	default:
		return out, &ResourceError{Code: "INVALID_ARGUMENT", Message: "invalid resource_type"}
	}
	if len(q.ResourceIDs) > 0 {
		base = base.Where("id IN ?", q.ResourceIDs)
	}
	if q.ParentID != nil {
		if *q.ParentID != "" {
			var parent orm.Document
			if err := s.db.WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL AND UPPER(document_type) = 'FOLDER'", *q.ParentID, q.DatasetIDs[0]).Take(&parent).Error; err != nil {
				return out, &ResourceError{Code: "NOT_FOUND", Message: "parent folder not found"}
			}
		}
		if q.Recursive {
			// UNION deduplicates IDs even in a corrupt historical cycle; tenant/library
			// predicates apply at both the anchor and every recursive step.
			base = base.Where(`id IN (WITH RECURSIVE descendants(id) AS (
    SELECT id FROM documents WHERE dataset_id = ? AND p_id = ? AND deleted_at IS NULL
    UNION SELECT d.id FROM documents d JOIN descendants p ON d.p_id=p.id WHERE d.dataset_id = ? AND d.deleted_at IS NULL
   ) SELECT id FROM descendants)`, q.DatasetIDs[0], *q.ParentID, q.DatasetIDs[0])
		} else {
			base = base.Where("p_id = ?", *q.ParentID)
		}
	}
	if q.Match != "" && q.Match != "contains" && q.Match != "exact" {
		return out, &ResourceError{Code: "INVALID_ARGUMENT", Message: "invalid match"}
	}
	escape := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")
	if q.Name != "" {
		if q.Match == "exact" {
			base = base.Where("display_name = ?", q.Name)
		} else {
			base = base.Where("LOWER(display_name) LIKE LOWER(?) ESCAPE '!'", "%"+escape.Replace(q.Name)+"%")
		}
	}
	if q.Path != "" {
		expr := "json_extract(ext, '$.relative_path')"
		if s.db.Dialector.Name() == "postgres" {
			expr = "ext ->> 'relative_path'"
		}
		if q.Match == "exact" {
			base = base.Where(expr+" = ?", q.Path)
		} else {
			base = base.Where("LOWER("+expr+") LIKE LOWER(?) ESCAPE '!'", "%"+escape.Replace(q.Path)+"%")
		}
	}
	if err := base.Count(&out.Total).Error; err != nil {
		return out, err
	}
	if err := base.Order("updated_at DESC, id ASC").Offset(q.Offset).Limit(q.Limit).Find(&out.Rows).Error; err != nil {
		return out, err
	}
	for _, row := range out.Rows {
		path, err := s.logicalPath(ctx, row)
		if err != nil {
			return out, err
		}
		out.LogicalPaths[row.ID] = path
	}
	return out, nil
}
func (s *DocumentCatalog) logicalPath(ctx context.Context, row orm.Document) (string, error) {
	parts := []string{row.DisplayName}
	seen := map[string]bool{row.ID: true}
	for row.PID != "" {
		if seen[row.PID] || len(parts) > 128 {
			return "", &ResourceError{Code: "CONFLICT", Message: "invalid folder ancestry"}
		}
		seen[row.PID] = true
		var parent orm.Document
		if err := s.db.WithContext(ctx).Where("id = ? AND dataset_id = ? AND deleted_at IS NULL AND UPPER(document_type) = 'FOLDER'", row.PID, row.DatasetID).Take(&parent).Error; err != nil {
			return "", &ResourceError{Code: "CONFLICT", Message: "folder ancestry is unavailable"}
		}
		parts = append(parts, parent.DisplayName)
		row = parent
	}
	for a, b := 0, len(parts)-1; a < b; a, b = a+1, b-1 {
		parts[a], parts[b] = parts[b], parts[a]
	}
	return strings.Join(parts, "/"), nil
}
