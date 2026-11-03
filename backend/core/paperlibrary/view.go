package paperlibrary

import (
	"context"
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"os"
	"strings"
	"sync"
)

type Counts struct {
	PaperCount         int64 `json:"paper_count"`
	PDFAttachmentCount int64 `json:"pdf_attachment_count"`
}
type View struct {
	Data     Data `json:"data"`
	CanWrite bool `json:"can_write"`
	Active   bool `json:"active"`
	Counts
}

// Statistics never loads bibliographic text, notes, identities, or import history.
func Statistics(ctx context.Context, db *gorm.DB, datasets []string) (map[string]Counts, error) {
	result := map[string]Counts{}
	for _, dataset := range datasets {
		if err := EnsureLegacy(ctx, db, dataset); err != nil {
			return result, err
		}
		result[dataset] = Counts{}
	}
	if len(datasets) == 0 {
		return result, nil
	}
	var rows []struct {
		DatasetID          string
		PaperCount         int64
		PDFAttachmentCount int64
	}
	err := db.WithContext(ctx).Table("paper_library_entries e").Select("e.dataset_id, COUNT(DISTINCT e.id) AS paper_count, COUNT(DISTINCT CASE WHEN a.kind='pdf' AND d.id IS NOT NULL THEN d.id END) AS pdf_attachment_count").
		Joins("LEFT JOIN paper_library_attachments a ON a.dataset_id=e.dataset_id AND a.paper_id=e.id AND a.detached_at IS NULL").
		Joins("LEFT JOIN documents d ON d.id=a.document_id AND d.dataset_id=e.dataset_id AND d.deleted_at IS NULL").Where("e.dataset_id IN ? AND e.removed_at IS NULL", datasets).Group("e.dataset_id").Scan(&rows).Error
	for _, row := range rows {
		result[row.DatasetID] = Counts{PaperCount: row.PaperCount, PDFAttachmentCount: row.PDFAttachmentCount}
	}
	return result, err
}

// Load reads only the requested entries and their children. Nil ids is an initial library read.
func Load(ctx context.Context, db *gorm.DB, dataset, user string, ids []string, includeFolders bool) (View, error) {
	view := View{Data: EmptyData()}
	if err := EnsureLegacy(ctx, db, dataset); err != nil {
		return view, err
	}
	if err := loadAuxiliary(ctx, db, dataset, &view.Data); err != nil {
		return view, err
	}
	// An attachment can come from a different provider than its confirmed
	// bibliography. Preserve that provenance before projecting typed fields.
	var auxiliary struct {
		Attachments []struct {
			ID      string `json:"id"`
			PaperID string `json:"paperId"`
			Source  string `json:"source"`
		} `json:"attachments"`
	}
	if len(view.Data.Extra) > 0 {
		if err := json.Unmarshal(view.Data.Extra, &auxiliary); err != nil {
			return view, err
		}
	}
	attachmentSources := map[string]map[string]string{}
	for _, attachment := range auxiliary.Attachments {
		if attachmentSources[attachment.PaperID] == nil {
			attachmentSources[attachment.PaperID] = map[string]string{}
		}
		attachmentSources[attachment.PaperID][attachment.ID] = attachment.Source
	}
	db = db.WithContext(ctx)
	query := db.Where("dataset_id=? AND removed_at IS NULL", dataset)
	if ids != nil {
		if len(ids) == 0 {
			return view, nil
		}
		query = query.Where("id IN ?", ids)
	}
	var entries []orm.PaperLibraryEntry
	if err := query.Order("created_at,id").Find(&entries).Error; err != nil {
		return view, err
	}
	if includeFolders {
		var folders []orm.PaperLibraryFolder
		if err := db.Where("dataset_id=?", dataset).Order("created_at,id").Find(&folders).Error; err != nil {
			return view, err
		}
		for _, f := range folders {
			view.Data.Folders = append(view.Data.Folders, Folder{ID: f.ID, Name: f.Name, ParentID: Pointer(f.ParentID), Revision: f.Revision})
		}
	}
	if len(entries) == 0 {
		return view, nil
	}
	entryIDs, workIDs := []string{}, []string{}
	for _, e := range entries {
		entryIDs = append(entryIDs, e.ID)
		workIDs = append(workIDs, e.AcademicWorkID)
	}
	var works []orm.AcademicWork
	var members []orm.PaperLibraryAttachment
	var notes []orm.PaperLibraryNote
	for _, q := range []*gorm.DB{
		db.Where("id IN ?", workIDs).Find(&works),
		db.Where("dataset_id=? AND paper_id IN ? AND detached_at IS NULL", dataset, entryIDs).Order("created_at,id").Find(&members),
		db.Where("dataset_id=? AND paper_id IN ?", dataset, entryIDs).Order("created_at,id").Find(&notes),
	} {
		if q.Error != nil {
			return view, q.Error
		}
	}
	workByID := map[string]orm.AcademicWork{}
	for _, w := range works {
		workByID[w.ID] = w
	}
	marks := map[string]bool{}
	if user != "" {
		var rows []orm.AcademicUserPaperState
		if err := db.Where("user_id=? AND dataset_id=? AND paper_id IN ?", user, dataset, workIDs).Find(&rows).Error; err != nil {
			return view, err
		}
		for _, m := range rows {
			marks[m.PaperID] = m.IsImportant
		}
	}
	documents, itemIDs := []string{}, []string{}
	for _, a := range members {
		if a.DocumentID != "" {
			documents = append(documents, a.DocumentID)
		}
		if a.ImportItemID != "" {
			itemIDs = append(itemIDs, a.ImportItemID)
		}
	}
	docByID := map[string]orm.Document{}
	linkByID := map[string]orm.AcademicWorkDocument{}
	stateByID := map[string]orm.AcademicDocumentState{}
	if len(documents) > 0 {
		var docs []orm.Document
		var links []orm.AcademicWorkDocument
		var states []orm.AcademicDocumentState
		for _, q := range []*gorm.DB{
			db.Where("dataset_id=? AND id IN ? AND deleted_at IS NULL", dataset, documents).Find(&docs),
			db.Where("dataset_id=? AND document_id IN ?", dataset, documents).Find(&links),
			db.Where("dataset_id=? AND document_id IN ?", dataset, documents).Find(&states),
		} {
			if q.Error != nil {
				return view, q.Error
			}
		}
		for _, d := range docs {
			docByID[d.ID] = d
		}
		for _, l := range links {
			linkByID[l.DocumentID] = l
		}
		for _, s := range states {
			stateByID[s.DocumentID] = s
		}
	}
	availability, err := originalAvailability(ctx, docByID)
	if err != nil {
		return view, err
	}
	origins := map[string]orm.PaperImportBatch{}
	if len(itemIDs) > 0 {
		var rows []struct {
			ItemID string
			orm.PaperImportBatch
		}
		if err := db.Table("paper_import_items i").Select("i.id AS item_id,b.*").Joins("JOIN paper_import_batches b ON b.id=i.batch_id").Where("i.id IN ? AND b.target_dataset_id=?", itemIDs, dataset).Scan(&rows).Error; err != nil {
			return view, err
		}
		for _, r := range rows {
			origins[r.ItemID] = r.PaperImportBatch
		}
	}
	paperByID := map[string]*Paper{}
	primaryByPaper := map[string]int{}
	for _, e := range entries {
		work, exists := workByID[e.AcademicWorkID]
		if !exists {
			return view, fmt.Errorf("paper %s has no scholarly identity", e.ID)
		}
		metadata, err := EffectiveMetadata(e, work)
		if err != nil {
			return view, err
		}
		p := Paper{ID: e.ID, AcademicWorkID: e.AcademicWorkID, Revision: e.Revision, Metadata: metadata, FolderID: Pointer(e.FolderID), Status: e.Status, Tags: []string{}, Important: marks[e.AcademicWorkID], Source: e.Source, ImportMethod: e.ImportMethod, SourceURL: e.SourceURL, ArxivVersion: e.ArxivVersion, Parse: "ready", AttachmentIDs: []string{}, NoteIDs: []string{}, Incomplete: metadata.Title == "" || len(metadata.Authors) == 0}
		var origin struct {
			Private bool `json:"legacy_private_identity"`
		}
		if err = json.Unmarshal(work.ProvenanceJSON, &origin); err != nil {
			return view, err
		}
		if origin.Private {
			p.AssociationConflict = "既有题录标识符与其他条目重合，原始题录已保留，请核对"
		}
		if err = json.Unmarshal(e.TagsJSON, &p.Tags); err != nil {
			return view, err
		}
		if p.Tags == nil {
			p.Tags = []string{}
		}
		view.Data.Papers = append(view.Data.Papers, p)
	}
	for i := range view.Data.Papers {
		p := &view.Data.Papers[i]
		paperByID[p.ID] = p
	}
	for _, a := range members {
		p := paperByID[a.PaperID]
		if p == nil {
			continue
		}
		link := linkByID[a.DocumentID]
		document, available := docByID[a.DocumentID]
		available = available && availability[document.ID]
		source := defaultText(attachmentSources[a.PaperID][a.ID], defaultText(link.SourceProvider, p.Source))
		attachment := Attachment{ID: a.ID, PaperID: a.PaperID, Name: a.Name, Kind: a.Kind, ContentRef: a.DocumentID, Source: source, ImportMethod: p.ImportMethod, SourceProvider: link.SourceProvider, SourceVersion: link.SourceVersion, SourceURL: link.SourceLocator, ContentHash: link.ContentSHA256, IsPrimary: a.IsPrimary, Available: a.DocumentID == "" || available, AddedAt: a.CreatedAt}
		if attachment.SourceURL == "" {
			attachment.SourceURL = a.SourceURL
		}
		if origin, ok := origins[a.ImportItemID]; ok {
			attachment.ImportMethod = ImportMethod(origin.EntryType)
			var policy struct {
				ExpansionID      string `json:"expansion_id"`
				ExpansionVersion int64  `json:"expansion_version"`
			}
			if err := json.Unmarshal(origin.PolicySnapshotJSON, &policy); err != nil {
				return view, err
			}
			attachment.ExpansionID, attachment.ExpansionVersion = policy.ExpansionID, policy.ExpansionVersion
		}
		p.AttachmentIDs = append(p.AttachmentIDs, a.ID)
		if state, ok := stateByID[a.DocumentID]; ok {
			attachment.AcademicState = &AcademicState{DocumentID: state.DocumentID, Kind: state.Kind, Status: state.Status, PaperID: state.PaperID, Revision: state.Revision, Manual: state.Manual, Error: state.Error}
			if state.Status == "pending" || state.Status == "processing" {
				view.Active = true
			}
			if state.PaperID != "" && state.PaperID != p.AcademicWorkID {
				p.AssociationConflict = "附件识别结果与已确认题录不同，请核对"
			}
		}
		if attachment.Kind == "pdf" && attachment.ContentRef != "" && attachment.Available {
			if _, exists := primaryByPaper[p.ID]; !exists || attachment.IsPrimary {
				primaryByPaper[p.ID] = len(view.Data.Attachments)
			}
		}
		view.Data.Attachments = append(view.Data.Attachments, attachment)
	}
	for _, n := range notes {
		if p := paperByID[n.PaperID]; p != nil {
			p.NoteIDs = append(p.NoteIDs, n.ID)
			view.Data.Notes = append(view.Data.Notes, Note{ID: n.ID, PaperID: n.PaperID, Title: n.Title, Content: n.Content, Kind: n.Kind, CreatedAt: n.CreatedAt, UpdatedAt: n.UpdatedAt})
		}
	}
	for i := range view.Data.Papers {
		p := &view.Data.Papers[i]
		index, exists := primaryByPaper[p.ID]
		if !exists {
			continue
		}
		primary := &view.Data.Attachments[index]
		primary.IsPrimary = true
	}
	return view, nil
}

// OriginalAvailable distinguishes retained bibliography/document identities from
// a usable original. Missing, malformed, non-regular and symlink files cannot
// satisfy import presence or enable a reader action.
func OriginalAvailable(document orm.Document) bool {
	var original struct {
		StoredPath string `json:"stored_path"`
	}
	if document.DeletedAt != nil || json.Unmarshal(document.Ext, &original) != nil || strings.TrimSpace(original.StoredPath) == "" {
		return false
	}
	info, err := os.Lstat(original.StoredPath)
	return err == nil && info.Mode().IsRegular()
}

// Bound filesystem probes while keeping availability request-scoped: deleted or
// replaced originals must never remain readable through a stale process cache.
func originalAvailability(ctx context.Context, documents map[string]orm.Document) (map[string]bool, error) {
	result := make(map[string]bool, len(documents))
	jobs := make(chan orm.Document)
	var workers sync.WaitGroup
	var mu sync.Mutex
	for i := 0; i < min(4, len(documents)); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for document := range jobs {
				if ctx.Err() != nil {
					continue
				}
				available := OriginalAvailable(document)
				mu.Lock()
				result[document.ID] = available
				mu.Unlock()
			}
		}()
	}
	for _, document := range documents {
		if ctx.Err() != nil {
			break
		}
		select {
		case jobs <- document:
		case <-ctx.Done():
		}
	}
	close(jobs)
	workers.Wait()
	return result, ctx.Err()
}
