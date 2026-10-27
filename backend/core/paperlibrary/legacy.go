package paperlibrary

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common/orm"
)

const maxSnapshotBytes = 20 << 20

type snapshotGraph map[string]json.RawMessage

// Complete versioned backups may contain children left behind by the old
// browser's remove-paper action. Partial import requests must still resolve
// their parents rather than silently creating historical records.
func historicalSnapshot(graph snapshotGraph) bool {
	var version int
	if json.Unmarshal(graph["schemaVersion"], &version) != nil || version < 3 || version > 4 {
		return false
	}
	for _, key := range []string{"folders", "papers", "attachments", "notes"} {
		value := bytes.TrimSpace(graph[key])
		if len(value) == 0 || value[0] != '[' {
			return false
		}
	}
	return true
}

// EnsureLegacy converts a saved graph exactly once. The archive is never changed
// or deleted, and the marker is committed with every normalized row. A failed
// conversion rolls back completely and remains retryable after a restart.
func EnsureLegacy(ctx context.Context, db *gorm.DB, dataset string) error {
	if !db.Migrator().HasTable(&orm.PaperLibrary{}) {
		return nil
	}
	// Steady-state statistics never reload the archived bibliography.
	if db.Migrator().HasTable(&orm.PaperLibraryLegacyState{}) {
		var marker orm.PaperLibraryLegacyState
		err := db.WithContext(ctx).Select("migrated_at").Where("dataset_id=?", dataset).Take(&marker).Error
		if err == nil && marker.MigratedAt != nil {
			return nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	var archive orm.PaperLibrary
	err := db.WithContext(ctx).Where("dataset_id=?", dataset).Take(&archive).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ds orm.Dataset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=?", dataset).Take(&ds).Error; err != nil {
			return err
		}
		var state orm.PaperLibraryLegacyState
		err := tx.Where("dataset_id=?", dataset).Take(&state).Error
		if err == nil && state.MigratedAt != nil {
			return nil
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := convertSnapshot(ctx, tx, ds, ds.CreateUserID, archive.DataJSON, true); err != nil {
			return fmt.Errorf("preserve legacy library %s: %w", dataset, err)
		}
		var ext map[string]any
		if err := json.Unmarshal(ds.Ext, &ext); err != nil {
			return err
		}
		if ext["library_kind"] == "paper" && (ds.Scene == "" || ds.Scene == "general") {
			if err := tx.Model(&orm.Dataset{}).Where("id=?", dataset).Update("scene", "academic_papers").Error; err != nil {
				return err
			}
		}
		now := time.Now().UTC()
		return tx.Model(&orm.PaperLibraryLegacyState{}).Where("dataset_id=?", dataset).Update("migrated_at", now).Error
	})
}

// ImportSnapshot atomically adds confirmed metadata and auxiliary entities.
// Stable IDs and scholarly identity reuse make retries idempotent. Existing
// reading state, corrected metadata, folders and notes always keep precedence.
func ImportSnapshot(ctx context.Context, db *gorm.DB, dataset, user string, raw json.RawMessage) error {
	if len(raw) > 48<<20 {
		return errors.New("library snapshot envelope too large")
	}
	if err := EnsureLegacy(ctx, db, dataset); err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ds orm.Dataset
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND deleted_at IS NULL", dataset).Take(&ds).Error; err != nil {
			return err
		}
		if ds.Scene != "academic_papers" {
			return errors.New("target is not a paper library")
		}
		return convertSnapshot(ctx, tx, ds, user, raw, false)
	})
}

func graphItems(graph snapshotGraph, key string) ([]map[string]json.RawMessage, error) {
	var items []map[string]json.RawMessage
	if raw := graph[key]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("invalid %s: %w", key, err)
		}
	}
	if len(items) > 50000 {
		return nil, fmt.Errorf("too many %s", key)
	}
	seen := map[string]bool{}
	for _, item := range items {
		id := textField(item, "id")
		if !ValidID(id) || seen[id] {
			return nil, fmt.Errorf("invalid or duplicate %s identity", key)
		}
		seen[id] = true
	}
	return items, nil
}
func textField(item map[string]json.RawMessage, key string) string {
	var value string
	_ = json.Unmarshal(item[key], &value)
	return value
}
func boolField(item map[string]json.RawMessage, key string) bool {
	var value bool
	_ = json.Unmarshal(item[key], &value)
	return value
}
func timeField(item map[string]json.RawMessage, key string, fallback time.Time) time.Time {
	var value time.Time
	if json.Unmarshal(item[key], &value) == nil && !value.IsZero() {
		return value
	}
	return fallback
}
func rawJSON(value any) json.RawMessage { raw, _ := json.Marshal(value); return raw }
func defaultText(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func decodeSnapshotPaper(item map[string]json.RawMessage) (Paper, error) {
	p := Paper{ID: textField(item, "id"), AcademicWorkID: textField(item, "academicWorkId"), Status: defaultText(textField(item, "status"), "unread"), Source: defaultText(textField(item, "source"), "unknown"), ImportMethod: textField(item, "importMethod"), SourceURL: textField(item, "sourceUrl"), ArxivVersion: textField(item, "arxivVersion"), Tags: []string{}}
	if raw := item["metadata"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &p.Metadata); err != nil {
			return p, err
		}
	}
	if p.Metadata.Title == "" {
		p.Metadata.Title = textField(item, "title")
	}
	if p.Metadata.Venue == "" {
		p.Metadata.Venue = textField(item, "venue")
	}
	if p.Metadata.Abstract == "" {
		p.Metadata.Abstract = textField(item, "abstract")
	}
	if p.Metadata.Year == 0 {
		_ = json.Unmarshal(item["year"], &p.Metadata.Year)
	}
	if p.Metadata.CitationCount == nil {
		_ = json.Unmarshal(item["citationCount"], &p.Metadata.CitationCount)
	}
	if len(p.Metadata.Authors) == 0 {
		if json.Unmarshal(item["authors"], &p.Metadata.Authors) != nil {
			p.Metadata.Authors = strings.FieldsFunc(textField(item, "authors"), func(r rune) bool { return r == ',' || r == ';' || r == '；' || r == '、' })
		}
	}
	p.Metadata.Authors = TrimAuthors(p.Metadata.Authors)
	if p.Metadata.Identifiers.DOI == "" {
		p.Metadata.Identifiers.DOI = textField(item, "doi")
	}
	if p.Metadata.Identifiers.Arxiv == "" {
		p.Metadata.Identifiers.Arxiv = defaultText(textField(item, "arxivId"), textField(item, "arxiv"))
	}
	p.Metadata.Identifiers.DOI = DOI(p.Metadata.Identifiers.DOI)
	p.Metadata.Identifiers.Arxiv = ArxivBase(p.Metadata.Identifiers.Arxiv)
	if raw := item["tags"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &p.Tags); err != nil {
			return p, err
		}
	}
	if p.Tags == nil {
		p.Tags = []string{}
	}
	p.FolderID = Pointer(textField(item, "folderId"))
	p.Important = boolField(item, "important")
	if len(p.Metadata.Title) > 4096 || len(p.Metadata.Authors) > 200 || len(p.Metadata.Abstract) > 2<<20 {
		return p, errors.New("paper metadata too large")
	}
	if p.Status != "unread" && p.Status != "reading" && p.Status != "read" {
		return p, errors.New("invalid reading status")
	}
	return p, nil
}

// ValidateFolderTree also validates references to existing folders supplied by
// the caller. A root is depth one; a maximum of 32 levels matches legacy data.
func ValidateFolderTree(folders []Folder) error {
	parents := map[string]string{}
	for _, f := range folders {
		if !ValidID(f.ID) {
			return errors.New("invalid folder identity")
		}
		if _, exists := parents[f.ID]; exists {
			return errors.New("duplicate folder identity")
		}
		parent := ""
		if f.ParentID != nil {
			parent = *f.ParentID
		}
		parents[f.ID] = parent
	}
	for id := range parents {
		seen := map[string]bool{}
		for cursor := id; cursor != ""; cursor = parents[cursor] {
			if _, ok := parents[cursor]; !ok {
				return errors.New("folder parent does not exist")
			}
			if seen[cursor] {
				return errors.New("folder cycle")
			}
			seen[cursor] = true
			if len(seen) > 32 {
				return errors.New("folder depth exceeds 32")
			}
		}
	}
	return nil
}

func convertSnapshot(ctx context.Context, tx *gorm.DB, ds orm.Dataset, user string, raw json.RawMessage, legacy bool) error {
	limit := maxSnapshotBytes
	if !legacy {
		limit = 48 << 20
	}
	if len(raw) == 0 || len(raw) > limit {
		return errors.New("invalid library snapshot size")
	}
	var graph snapshotGraph
	if err := json.Unmarshal(raw, &graph); err != nil {
		return err
	}
	if graph == nil {
		return errors.New("library snapshot must be an object")
	}
	historical := legacy || historicalSnapshot(graph)
	folders, err := graphItems(graph, "folders")
	if err != nil {
		return err
	}
	if legacy {
		// The old graph had an independent collection hierarchy as well as
		// folders. Keep its original tree validation during conversion.
		var collections []Folder
		if encoded := graph["collections"]; len(encoded) > 0 {
			if err := json.Unmarshal(encoded, &collections); err != nil {
				return err
			}
			if err := ValidateFolderTree(collections); err != nil {
				return err
			}
		}
	}
	papers, err := graphItems(graph, "papers")
	if err != nil {
		return err
	}
	if len(papers) > 10000 {
		return errors.New("too many papers")
	}
	notes, err := graphItems(graph, "notes")
	if err != nil {
		return err
	}
	attachments, err := graphItems(graph, "attachments")
	if err != nil {
		return err
	}
	hiddenIDs, orphanIDs := map[string]bool{}, map[string]bool{}
	knownPapers := map[string]bool{}
	for _, p := range papers {
		knownPapers[textField(p, "id")] = true
	}
	hidden, err := graphItems(graph, "hiddenArxivPapers")
	if err != nil {
		return err
	}
	for _, p := range hidden {
		id := textField(p, "id")
		if !knownPapers[id] {
			papers = append(papers, p)
			hiddenIDs[id] = true
			knownPapers[id] = true
		}
	}
	if historical {
		// Older removal kept child materials even when it omitted the paper
		// metadata itself. Tombstones retain those children without inventing
		// a visible paper or restoring removed membership.
		for _, items := range [][]map[string]json.RawMessage{notes, attachments} {
			for _, item := range items {
				id := textField(item, "paperId")
				if !knownPapers[id] {
					if !ValidID(id) {
						return errors.New("invalid historical paper identity")
					}
					papers = append(papers, map[string]json.RawMessage{"id": rawJSON(id), "metadata": json.RawMessage(`{"title":"","authors":[],"identifiers":{}}`), "source": rawJSON("unknown")})
					hiddenIDs[id], orphanIDs[id], knownPapers[id] = true, true, true
				}
			}
		}
	}
	if !legacy {
		// Hidden children are deliberately absent from the active projection;
		// retain their complete source snapshot in exported backups as well.
		if len(hiddenIDs) > 0 && (len(graph["legacy"]) == 0 || string(bytes.TrimSpace(graph["legacy"])) == "null") {
			graph["legacy"] = append(json.RawMessage(nil), raw...)
		}
		if err := retainRestoredArchive(tx, ds.ID, graph); err != nil {
			return err
		}
		raw = rawJSON(graph)
	}
	now := time.Now().UTC()
	var existingFolders []orm.PaperLibraryFolder
	if err = tx.Where("dataset_id=?", ds.ID).Find(&existingFolders).Error; err != nil {
		return err
	}
	allFolders := []Folder{}
	knownFolders := map[string]bool{}
	for _, f := range existingFolders {
		knownFolders[f.ID] = true
		allFolders = append(allFolders, Folder{ID: f.ID, Name: f.Name, ParentID: Pointer(f.ParentID)})
	}
	for _, f := range folders {
		if !knownFolders[textField(f, "id")] {
			allFolders = append(allFolders, Folder{ID: textField(f, "id"), Name: textField(f, "name"), ParentID: Pointer(textField(f, "parentId"))})
		}
	}
	if err = ValidateFolderTree(allFolders); err != nil {
		return err
	}
	for _, f := range folders {
		id, name, parent := textField(f, "id"), strings.TrimSpace(textField(f, "name")), textField(f, "parentId")
		if knownFolders[id] {
			continue
		}
		if name == "" || len(name) > 255 {
			return errors.New("invalid folder name")
		}
		key := strings.ToLower(name)
		var count int64
		if err = tx.Model(&orm.PaperLibraryFolder{}).Where("dataset_id=? AND parent_id=? AND name_key=?", ds.ID, parent, key).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			if !legacy {
				return errors.New("folder name already exists")
			}
			key = id
		}
		if err = tx.Create(&orm.PaperLibraryFolder{DatasetID: ds.ID, ID: id, Name: name, NameKey: key, ParentID: parent, Revision: 1, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
			return err
		}
		knownFolders[id] = true
	}
	aliases := map[string]string{}
	workIDs := map[string]string{}
	for _, item := range papers {
		p, e := decodeSnapshotPaper(item)
		if e != nil {
			return e
		}
		var row orm.PaperLibraryEntry
		e = tx.Where("dataset_id=? AND id=?", ds.ID, p.ID).Take(&row).Error
		if e == nil {
			aliases[p.ID] = row.ID
			workIDs[row.ID] = row.AcademicWorkID
			continue
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		work, e := snapshotWork(tx, ds.ID, p, legacy)
		if e != nil {
			return e
		}
		if !legacy {
			e = tx.Where("dataset_id=? AND academic_work_id=?", ds.ID, work.ID).Take(&row).Error
			if e == nil {
				aliases[p.ID] = row.ID
				workIDs[row.ID] = row.AcademicWorkID
				continue
			}
			if !errors.Is(e, gorm.ErrRecordNotFound) {
				return e
			}
		}
		folder := ""
		if p.FolderID != nil {
			folder = *p.FolderID
		}
		if folder != "" && !knownFolders[folder] {
			if hiddenIDs[p.ID] {
				folder = ""
			} else {
				return errors.New("paper folder does not exist")
			}
		}
		row = orm.PaperLibraryEntry{DatasetID: ds.ID, ID: p.ID, AcademicWorkID: work.ID, FolderID: folder, Status: p.Status, TagsJSON: rawJSON(p.Tags), MetadataJSON: rawJSON(p.Metadata), Source: p.Source, ImportMethod: defaultText(p.ImportMethod, "zotero"), SourceURL: p.SourceURL, ArxivVersion: p.ArxivVersion, Revision: 1, CreatedAt: now, UpdatedAt: now}
		if hiddenIDs[p.ID] {
			row.RemovedAt = &now
		}
		if err = tx.Create(&row).Error; err != nil {
			return err
		}
		aliases[p.ID] = p.ID
		workIDs[p.ID] = work.ID
		if p.Important && user != "" {
			mark := orm.AcademicUserPaperState{UserID: user, DatasetID: ds.ID, PaperID: work.ID, IsImportant: true, UpdatedAt: now}
			if err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&mark).Error; err != nil {
				return err
			}
		}
	}
	resolvePaper := func(id string) (string, error) {
		if value, ok := aliases[id]; ok {
			return value, nil
		}
		var row orm.PaperLibraryEntry
		if e := tx.Where("dataset_id=? AND id=?", ds.ID, id).Take(&row).Error; e != nil {
			return "", e
		}
		workIDs[id] = row.AcademicWorkID
		return id, nil
	}
	for _, n := range notes {
		id := textField(n, "id")
		paper, e := resolvePaper(textField(n, "paperId"))
		if e != nil {
			return e
		}
		var existing orm.PaperLibraryNote
		e = tx.Where("dataset_id=? AND id=?", ds.ID, id).Take(&existing).Error
		if e == nil {
			if existing.PaperID != paper {
				return errors.New("note identity belongs to another paper")
			}
			continue
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		row := orm.PaperLibraryNote{DatasetID: ds.ID, ID: id, PaperID: paper, Title: textField(n, "title"), Content: textField(n, "content"), Kind: defaultText(textField(n, "kind"), "note"), CreatedAt: timeField(n, "createdAt", now), UpdatedAt: timeField(n, "updatedAt", now)}
		if err = tx.Create(&row).Error; err != nil {
			return err
		}
	}
	for _, a := range attachments {
		id := textField(a, "id")
		paper, e := resolvePaper(textField(a, "paperId"))
		if e != nil {
			return e
		}
		var existing orm.PaperLibraryAttachment
		e = tx.Where("dataset_id=? AND id=?", ds.ID, id).Take(&existing).Error
		if e == nil {
			if existing.PaperID != paper {
				return errors.New("attachment identity belongs to another paper")
			}
			continue
		}
		if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		document := textField(a, "contentRef")
		documentExists := false
		if len(document) > 128 {
			return errors.New("invalid document identity")
		}
		if document != "" {
			var d orm.Document
			e = tx.Where("id=?", document).Take(&d).Error
			documentExists = e == nil && d.DeletedAt == nil
			if e == nil && d.DatasetID != ds.ID {
				return errors.New("attachment belongs to another dataset")
			}
			if !historical && (errors.Is(e, gorm.ErrRecordNotFound) || e == nil && d.DeletedAt != nil) {
				return errors.New("new attachment must reference an existing document")
			}
			if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
				return e
			}
			var count int64
			if err = tx.Model(&orm.PaperLibraryAttachment{}).Where("dataset_id=? AND document_id=? AND detached_at IS NULL", ds.ID, document).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 && !hiddenIDs[paper] {
				return errors.New("legacy document has multiple memberships; original archive retained for explicit resolution")
			}
		}
		row := orm.PaperLibraryAttachment{DatasetID: ds.ID, ID: id, PaperID: paper, DocumentID: document, Name: textField(a, "name"), Kind: defaultText(textField(a, "kind"), "pdf"), SourceURL: textField(a, "sourceUrl"), IsPrimary: boolField(a, "isPrimary"), CreatedAt: timeField(a, "addedAt", now), UpdatedAt: now}
		if hiddenIDs[paper] {
			row.DetachedAt = &now
			row.IsPrimary = false
		}
		if err = tx.Create(&row).Error; err != nil {
			return err
		}
		if documentExists && !orphanIDs[paper] {
			link := orm.AcademicWorkDocument{AcademicWorkID: workIDs[paper], DatasetID: ds.ID, DocumentID: document, SourceProvider: defaultText(textField(a, "sourceProvider"), textField(a, "source")), SourceLocator: textField(a, "sourceUrl"), SourceVersion: textField(a, "sourceVersion"), ContentSHA256: textField(a, "contentHash"), MatchMethod: "legacy_import", CreatedAt: now, UpdatedAt: now}
			if err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&link).Error; err != nil {
				return err
			}
		}
	}
	// Rebind extension references when a new source matched an existing identity.
	remapped, err := remapSnapshot(raw, aliases)
	if err != nil {
		return err
	}
	state := orm.PaperLibraryLegacyState{DatasetID: ds.ID, ExtrasJSON: json.RawMessage(`{}`), UpdatedAt: now}
	if err = tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&state).Error; err != nil {
		return err
	}
	if err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("dataset_id=?", ds.ID).Take(&state).Error; err != nil {
		return err
	}
	state.ExtrasJSON, err = mergeAuxiliary(state.ExtrasJSON, remapped)
	if err != nil {
		return err
	}
	return tx.Model(&state).Updates(map[string]any{"extras_json": state.ExtrasJSON, "updated_at": now}).Error
}

func snapshotWork(tx *gorm.DB, dataset string, p Paper, legacy bool) (orm.AcademicWork, error) {
	ids := p.Metadata.Identifiers
	if tx.Dialector.Name() == "postgres" {
		keys := []string{}
		if ids.DOI != "" {
			keys = append(keys, "doi:"+ids.DOI)
		}
		if ids.Arxiv != "" {
			keys = append(keys, "arxiv:"+ids.Arxiv)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "academic-library-identity:"+key).Error; err != nil {
				return orm.AcademicWork{}, err
			}
		}
	}
	var candidates []orm.AcademicWork
	if legacy && p.AcademicWorkID != "" {
		var prior orm.AcademicWork
		err := tx.Where("id=?", p.AcademicWorkID).Take(&prior).Error
		if err == nil {
			candidates = append(candidates, prior)
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return prior, err
		}
	}
	if len(candidates) == 0 && (ids.DOI != "" || ids.Arxiv != "") {
		q := tx.Where("doi_normalized=? AND doi_normalized<>''", ids.DOI)
		if ids.Arxiv != "" {
			q = q.Or("arxiv_id_base=?", ids.Arxiv)
		}
		if err := q.Limit(2).Find(&candidates).Error; err != nil {
			return orm.AcademicWork{}, err
		}
	}
	private := false
	if len(candidates) > 1 {
		if !legacy {
			return orm.AcademicWork{}, errors.New("paper identifiers conflict")
		}
		private = true
	}
	if len(candidates) == 1 {
		row := candidates[0]
		if IdentityConflict(WorkMetadata(row).Identifiers, ids) {
			if !legacy {
				return row, errors.New("paper identifiers conflict")
			}
			private = true
		}
		if !private {
			var count int64
			if legacy {
				if err := tx.Model(&orm.PaperLibraryEntry{}).Where("dataset_id=? AND academic_work_id=?", dataset, row.ID).Count(&count).Error; err != nil {
					return row, err
				}
			}
			if count == 0 {
				return row, nil
			}
			private = true
		}
	}
	now := time.Now().UTC()
	workID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("library-paper:"+dataset+":"+p.ID)).String()
	row := orm.AcademicWork{ID: workID, CanonicalTitle: p.Metadata.Title, NormalizedTitle: strings.ToLower(strings.TrimSpace(p.Metadata.Title)), AuthorsJSON: rawJSON(p.Metadata.Authors), PublicationYear: p.Metadata.Year, Venue: p.Metadata.Venue, Abstract: p.Metadata.Abstract, DOINormalized: ids.DOI, ArxivIDBase: ids.Arxiv, ExternalIDsJSON: rawJSON(map[string]string{}), ProvenanceJSON: rawJSON(map[string]any{"source": "library", "legacy_private_identity": private}), CreatedAt: now, UpdatedAt: now}
	if len(p.Metadata.Authors) > 0 {
		row.FirstAuthorNormalized = strings.ToLower(p.Metadata.Authors[0])
	}
	if private {
		row.DOINormalized = ""
		row.ArxivIDBase = ""
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error; err != nil {
		return row, err
	}
	return row, tx.Where("id=?", workID).Take(&row).Error
}
func remapSnapshot(raw json.RawMessage, aliases map[string]string) (json.RawMessage, error) {
	var value any
	if err := decodeAuxiliaryJSON(raw, &value); err != nil {
		return nil, err
	}
	var walk func(any, string)
	walk = func(v any, parent string) {
		switch v := v.(type) {
		case map[string]any:
			for k, child := range v {
				// Historical snapshots are records of their original graph,
				// not active references to rewrite during identity reuse.
				if k == "legacy" || k == "migrationSnapshots" || k == "mergeSnapshots" || k == "restoredArchives" {
					continue
				}
				if text, ok := child.(string); ok && (k == "paperId" || k == "fromPaperId" || k == "toPaperId" || k == "primaryId" || k == "duplicateId" || k == "id" && parent == "papers") {
					if mapped, ok := aliases[text]; ok {
						v[k] = mapped
					}
				}
				if k == "relatedPaperIds" {
					if list, ok := child.([]any); ok {
						for i, item := range list {
							if text, ok := item.(string); ok {
								if mapped, ok := aliases[text]; ok {
									list[i] = mapped
								}
							}
						}
					}
				}
				walk(child, k)
			}
		case []any:
			for _, child := range v {
				walk(child, parent)
			}
		}
	}
	walk(value, "")
	return json.Marshal(value)
}

// A backup restored into a different library may carry an older archive which
// is not this dataset's own migration source. Retain it as auxiliary history,
// deduplicated by content, rather than treating it as a writable source graph.
func retainRestoredArchive(tx *gorm.DB, dataset string, graph snapshotGraph) error {
	legacy := graph["legacy"]
	delete(graph, "legacy")
	if len(legacy) == 0 || string(legacy) == "null" {
		return nil
	}
	canonical := func(raw json.RawMessage) ([]byte, error) {
		var value any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}
	encoded, err := canonical(legacy)
	if err != nil {
		return err
	}
	var own orm.PaperLibrary
	err = tx.Where("dataset_id=?", dataset).Take(&own).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if err == nil {
		existing, e := canonical(own.DataJSON)
		if e != nil {
			return e
		}
		if bytes.Equal(existing, encoded) {
			return nil
		}
	}
	var archives []map[string]json.RawMessage
	if prior := graph["restoredArchives"]; len(prior) > 0 {
		if err = json.Unmarshal(prior, &archives); err != nil {
			return err
		}
	}
	digest := sha256.Sum256(encoded)
	id := "legacy-" + hex.EncodeToString(digest[:])
	exists := false
	for _, archive := range archives {
		if textField(archive, "id") == id {
			exists = true
			break
		}
	}
	if !exists {
		archives = append(archives, map[string]json.RawMessage{"id": rawJSON(id), "source": rawJSON("normalized-backup"), "data": legacy})
	}
	graph["restoredArchives"] = rawJSON(archives)
	return nil
}
