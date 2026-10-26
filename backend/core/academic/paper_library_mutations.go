package academic

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/paperlibrary"
	"lazymind/core/store"
	"net/http"
	"sort"
	"strings"
	"time"
)

func validateLibraryMetadata(m *paperlibrary.Metadata) error {
	m.Title = strings.TrimSpace(m.Title)
	m.Authors = paperlibrary.TrimAuthors(m.Authors)
	m.Identifiers.DOI = NormalizeDOI(m.Identifiers.DOI)
	rawArxiv := strings.TrimSpace(m.Identifiers.Arxiv)
	m.Identifiers.Arxiv, _ = NormalizeArxivID(rawArxiv)
	if (m.Identifiers.DOI != "" && doiPattern.FindString(m.Identifiers.DOI) != m.Identifiers.DOI) || (rawArxiv != "" && m.Identifiers.Arxiv == "") {
		return invalidLibraryMutation("INVALID_PAPER_LIBRARY", "DOI 或 arXiv 标识符无效")
	}
	if m.Title == "" || len(m.Title) > 4096 || len(m.Abstract) > 2<<20 || len(m.Authors) > 500 || len(m.Venue) > 4096 || m.Year < 0 || m.Year > 10000 || len(m.Identifiers.DOI) > 512 || len(m.Identifiers.Arxiv) > 64 || m.CitationCount != nil && *m.CitationCount < 0 {
		return invalidLibraryMutation("INVALID_PAPER_LIBRARY", "论文题录字段无效或过长")
	}
	for _, a := range m.Authors {
		if len(a) > 2048 {
			return invalidLibraryMutation("INVALID_PAPER_LIBRARY", "作者姓名过长")
		}
	}
	return nil
}
func resolveLibraryWork(ctx context.Context, tx *gorm.DB, dataset, id string, m paperlibrary.Metadata) (orm.AcademicWork, error) {
	input := WorkInput{Title: m.Title, Authors: m.Authors, Year: m.Year, Venue: m.Venue, Abstract: m.Abstract, DOI: m.Identifiers.DOI, ArxivID: m.Identifiers.Arxiv}
	if input.DOI != "" || input.ArxivID != "" {
		if tx.Dialector.Name() == "postgres" {
			keys := []string{}
			if input.DOI != "" {
				keys = append(keys, "doi:"+input.DOI)
			}
			if input.ArxivID != "" {
				keys = append(keys, "arxiv:"+input.ArxivID)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if e := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?,0))", "academic-library-identity:"+key).Error; e != nil {
					return orm.AcademicWork{}, e
				}
			}
		}
		// Existing public bibliography is not enriched from a local correction.
		var candidates []orm.AcademicWork
		q := tx.Where("doi_normalized=? AND doi_normalized<>''", input.DOI)
		if input.ArxivID != "" {
			q = q.Or("arxiv_id_base=?", input.ArxivID)
		}
		if e := q.Limit(2).Find(&candidates).Error; e != nil {
			return orm.AcademicWork{}, e
		}
		if len(candidates) > 1 {
			return orm.AcademicWork{}, invalidLibraryMutation("PAPER_IDENTITY_CONFLICT", "标识符对应多个既有论文身份，请核对 DOI 与 arXiv")
		}
		if len(candidates) == 1 {
			existing := candidates[0]
			input.Title, input.Year, input.Venue, input.Abstract = existing.CanonicalTitle, existing.PublicationYear, existing.Venue, existing.Abstract
			if e := json.Unmarshal(existing.AuthorsJSON, &input.Authors); e != nil {
				return existing, e
			}
		}
		work, _, err := findOrCreateWork(ctx, tx, input)
		if err != nil {
			return work, invalidLibraryMutation("PAPER_IDENTITY_CONFLICT", err.Error())
		}
		return work, nil
	}
	workID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("library-paper:"+dataset+":"+id)).String()
	work := orm.AcademicWork{ID: workID, CanonicalTitle: input.Title, NormalizedTitle: NormalizeTitle(input.Title), AuthorsJSON: marshal(input.Authors), PublicationYear: input.Year, Venue: input.Venue, Abstract: input.Abstract, ExternalIDsJSON: marshal(map[string]string{}), ProvenanceJSON: marshal(map[string]string{"source": "library"}), CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	if len(input.Authors) > 0 {
		work.FirstAuthorNormalized = normalizeAuthor(input.Authors[0])
	}
	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&work).Error; err != nil {
		return work, err
	}
	return work, tx.Where("id=?", workID).Take(&work).Error
}
func confirmedMetadata(m paperlibrary.Metadata) json.RawMessage {
	return marshal(map[string]any{"title": m.Title, "authors": m.Authors, "venue": m.Venue, "year": m.Year, "abstract": m.Abstract, "citationCount": m.CitationCount})
}
func validateAttachedWork(tx *gorm.DB, dataset, paperID string, work orm.AcademicWork) error {
	var states []orm.AcademicDocumentState
	err := tx.Table("academic_document_states s").Select("s.*").Joins("JOIN paper_library_attachments a ON a.document_id=s.document_id AND a.dataset_id=s.dataset_id AND a.detached_at IS NULL").Where("a.dataset_id=? AND a.paper_id=? AND s.kind='paper'", dataset, paperID).Find(&states).Error
	if err != nil {
		return err
	}
	workIDs := []string{}
	for _, s := range states {
		if s.PaperID != "" {
			workIDs = append(workIDs, s.PaperID)
		}
	}
	var links []orm.AcademicWorkDocument
	if err = tx.Table("academic_work_documents l").Select("l.*").Joins("JOIN paper_library_attachments a ON a.document_id=l.document_id AND a.dataset_id=l.dataset_id AND a.detached_at IS NULL").Where("a.dataset_id=? AND a.paper_id=?", dataset, paperID).Find(&links).Error; err != nil {
		return err
	}
	for _, link := range links {
		workIDs = append(workIDs, link.AcademicWorkID)
	}
	if len(workIDs) == 0 {
		return nil
	}
	var works []orm.AcademicWork
	if err = tx.Where("id IN ?", workIDs).Find(&works).Error; err != nil {
		return err
	}
	for _, identified := range works {
		if paperlibrary.IdentityConflict(paperlibrary.WorkMetadata(work).Identifiers, paperlibrary.WorkMetadata(identified).Identifiers) {
			return invalidLibraryMutation("PAPER_IDENTITY_CONFLICT", "论文身份与已有附件不一致，请核对后再修改")
		}
	}
	return nil
}
func applyLibraryMetadata(ctx context.Context, tx *gorm.DB, row *orm.PaperLibraryEntry, metadata paperlibrary.Metadata) error {
	work, e := resolveLibraryWork(ctx, tx, row.DatasetID, row.ID, metadata)
	if e != nil {
		return e
	}
	if e = validateAttachedWork(tx, row.DatasetID, row.ID, work); e != nil {
		return e
	}
	var duplicates int64
	if e = tx.Model(&orm.PaperLibraryEntry{}).Where("dataset_id=? AND academic_work_id=? AND id<>?", row.DatasetID, work.ID, row.ID).Count(&duplicates).Error; e != nil {
		return e
	}
	if duplicates > 0 {
		return invalidLibraryMutation("PAPER_IDENTITY_CONFLICT", "论文集已有该论文，请核对后编辑已有条目")
	}

	if row.AcademicWorkID != work.ID {
		var members []orm.PaperLibraryAttachment
		if e = tx.Where("dataset_id=? AND paper_id=? AND document_id<>'' AND detached_at IS NULL", row.DatasetID, row.ID).Find(&members).Error; e != nil {
			return e
		}
		for _, member := range members {
			var link orm.AcademicWorkDocument
			if e = tx.Where("dataset_id=? AND document_id=?", row.DatasetID, member.DocumentID).Take(&link).Error; errors.Is(e, gorm.ErrRecordNotFound) {
				continue
			} else if e != nil {
				return e
			}
			link.AcademicWorkID = work.ID
			link.MatchMethod = "manual"
			link.UpdatedAt = time.Now().UTC()
			if e = saveDocumentWorkLink(tx, link); e != nil {
				return e
			}
			if e = tx.Model(&orm.AcademicDocumentState{}).Where("dataset_id=? AND document_id=?", row.DatasetID, member.DocumentID).Updates(map[string]any{"paper_id": work.ID, "manual": true, "revision": gorm.Expr("revision+1"), "updated_at": time.Now().UTC()}).Error; e != nil {
				return e
			}
		}
	}
	row.AcademicWorkID = work.ID
	row.MetadataJSON = confirmedMetadata(metadata)
	return nil
}
func PatchLibraryPaper(w http.ResponseWriter, r *http.Request) {
	dataset, _, ok := libraryAccess(w, r, true)
	if !ok {
		return
	}
	var input struct {
		Revision int64                  `json:"revision"`
		Metadata *paperlibrary.Metadata `json:"metadata,omitempty"`
		Status   *string                `json:"status,omitempty"`
		Tags     *[]string              `json:"tags,omitempty"`
	}
	if !parseJSON(r, &input) || input.Revision < 1 || input.Metadata == nil && input.Status == nil && input.Tags == nil {
		replyError(w, 400, "INVALID_REQUEST", "revision 和修改字段不能为空")
		return
	}
	if input.Metadata != nil {
		if err := validateLibraryMetadata(input.Metadata); err != nil {
			replyLibraryError(w, err)
			return
		}
	}
	if input.Status != nil && *input.Status != "unread" && *input.Status != "reading" && *input.Status != "read" {
		replyError(w, 400, "INVALID_REQUEST", "阅读状态无效")
		return
	}
	if input.Tags != nil {
		if len(*input.Tags) > 100 {
			replyError(w, 400, "INVALID_REQUEST", "标签数量过多")
			return
		}
		for _, tag := range *input.Tags {
			if len(tag) > 255 {
				replyError(w, 400, "INVALID_REQUEST", "标签过长")
				return
			}
		}
	}
	id := mux.Vars(r)["library_paper_id"]
	err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		row, err := paperlibrary.Entry(tx, dataset, id, input.Revision)
		if err != nil {
			return err
		}
		if input.Metadata != nil {
			if err := applyLibraryMetadata(r.Context(), tx, &row, *input.Metadata); err != nil {
				return err
			}
		}
		if input.Status != nil {
			row.Status = *input.Status
		}
		if input.Tags != nil {
			row.TagsJSON = marshal(*input.Tags)
		}
		return paperlibrary.SaveEntry(tx, &row)
	})
	replyLibraryChanges(w, r, dataset, []string{id}, err)
}

type libraryTarget struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
}

func libraryTargets(input []libraryTarget) bool {
	if len(input) == 0 || len(input) > 500 {
		return false
	}
	seen := map[string]bool{}
	for _, v := range input {
		if !paperlibrary.ValidID(v.ID) || v.Revision < 1 || seen[v.ID] {
			return false
		}
		seen[v.ID] = true
	}
	sort.Slice(input, func(i, j int) bool { return input[i].ID < input[j].ID })
	return true
}
func MoveLibraryPapers(w http.ResponseWriter, r *http.Request)   { mutateLibraryPapers(w, r, false) }
func RemoveLibraryPapers(w http.ResponseWriter, r *http.Request) { mutateLibraryPapers(w, r, true) }
func mutateLibraryPapers(w http.ResponseWriter, r *http.Request, remove bool) {
	dataset, _, ok := libraryAccess(w, r, true)
	if !ok {
		return
	}
	var input struct {
		Items    []libraryTarget `json:"items"`
		FolderID string          `json:"folder_id"`
	}
	if !parseJSON(r, &input) || !libraryTargets(input.Items) {
		replyError(w, 400, "INVALID_REQUEST", "请选择 1 至 500 篇论文并提供版本")
		return
	}
	ids := []string{}
	err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if !remove {
			if e := paperlibrary.CheckFolder(tx, dataset, input.FolderID); e != nil {
				return e
			}
		}
		for _, item := range input.Items {
			row, e := paperlibrary.Entry(tx, dataset, item.ID, item.Revision)
			if e != nil {
				return e
			}
			if remove {
				now := time.Now().UTC()
				row.RemovedAt = &now
			} else {
				row.FolderID = input.FolderID
			}
			if e = paperlibrary.SaveEntry(tx, &row); e != nil {
				return e
			}
			ids = append(ids, row.ID)
		}
		return nil
	})
	if err != nil {
		replyLibraryError(w, err)
		return
	}
	if remove {
		common.ReplyJSON(w, libraryChanges{Data: paperlibrary.EmptyData(), RemovedPaperIDs: ids})
		return
	}
	replyLibraryChanges(w, r, dataset, ids, nil)
}
func DetachLibraryAttachment(w http.ResponseWriter, r *http.Request) {
	dataset, _, ok := libraryAccess(w, r, true)
	if !ok {
		return
	}
	var input struct {
		Revision int64 `json:"revision"`
	}
	if !parseJSON(r, &input) || input.Revision < 1 {
		replyError(w, 400, "INVALID_REQUEST", "revision 无效")
		return
	}
	id, attachmentID := mux.Vars(r)["library_paper_id"], mux.Vars(r)["attachment_id"]
	err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var member orm.PaperLibraryAttachment
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("dataset_id=? AND paper_id=? AND id=?", dataset, id, attachmentID).Take(&member).Error; e != nil {
			return e
		}
		if member.DetachedAt != nil {
			return nil
		}
		row, e := paperlibrary.Entry(tx, dataset, id, input.Revision)
		if e != nil {
			return e
		}
		now := time.Now().UTC()
		if e = tx.Model(&member).Updates(map[string]any{"detached_at": now, "is_primary": false, "updated_at": now}).Error; e != nil {
			return e
		}
		return paperlibrary.SaveEntry(tx, &row)
	})
	replyLibraryChanges(w, r, dataset, []string{id}, err)
}
func AddLibraryNote(w http.ResponseWriter, r *http.Request) {
	dataset, _, ok := libraryAccess(w, r, true)
	if !ok {
		return
	}
	id := mux.Vars(r)["library_paper_id"]
	var input struct {
		ID      string `json:"id"`
		Content string `json:"content"`
	}
	if !parseJSON(r, &input) || !paperlibrary.ValidID(input.ID) || strings.TrimSpace(input.Content) == "" || len(input.Content) > 2<<20 {
		replyError(w, 400, "INVALID_REQUEST", "笔记内容为空或过长")
		return
	}
	err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var entry orm.PaperLibraryEntry
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("dataset_id=? AND id=? AND removed_at IS NULL", dataset, id).Take(&entry).Error; e != nil {
			return e
		}
		var prior orm.PaperLibraryNote
		if e := tx.Where("dataset_id=? AND id=?", dataset, input.ID).Take(&prior).Error; e == nil {
			if prior.PaperID != id || prior.Content != input.Content {
				return invalidLibraryMutation("INVALID_PAPER_LIBRARY", "笔记请求与原内容不同")
			}
			return nil
		} else if !errors.Is(e, gorm.ErrRecordNotFound) {
			return e
		}
		now := time.Now().UTC()
		return tx.Create(&orm.PaperLibraryNote{DatasetID: dataset, ID: input.ID, PaperID: id, Title: "新笔记", Content: input.Content, Kind: "note", CreatedAt: now, UpdatedAt: now}).Error
	})
	replyLibraryChanges(w, r, dataset, []string{id}, err)
}
func CreateLibraryFolder(w http.ResponseWriter, r *http.Request) { saveLibraryFolder(w, r, false) }
func RenameLibraryFolder(w http.ResponseWriter, r *http.Request) { saveLibraryFolder(w, r, true) }
func saveLibraryFolder(w http.ResponseWriter, r *http.Request, rename bool) {
	dataset, _, ok := libraryAccess(w, r, true)
	if !ok {
		return
	}
	var input struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		ParentID string `json:"parent_id"`
		Revision int64  `json:"revision"`
	}
	if !parseJSON(r, &input) {
		replyError(w, 400, "INVALID_REQUEST", "目录请求无效")
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 255 {
		replyError(w, 400, "INVALID_REQUEST", "目录名称为空或过长")
		return
	}
	id := input.ID
	if rename {
		id = mux.Vars(r)["folder_id"]
	}
	if !paperlibrary.ValidID(id) {
		replyError(w, 400, "INVALID_REQUEST", "目录 ID 无效")
		return
	}
	var row orm.PaperLibraryFolder
	err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if rename {
			if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("dataset_id=? AND id=?", dataset, id).Take(&row).Error; e != nil {
				return e
			}
			if row.Revision != input.Revision {
				return paperlibrary.ErrConflict
			}
		} else {
			if e := paperlibrary.CheckFolder(tx, dataset, input.ParentID); e != nil {
				return e
			}
			row = orm.PaperLibraryFolder{DatasetID: dataset, ID: id, ParentID: input.ParentID, Revision: 1, CreatedAt: time.Now().UTC()}
		}
		var count int64
		if e := tx.Model(&orm.PaperLibraryFolder{}).Where("dataset_id=? AND parent_id=? AND name_key=? AND id<>?", dataset, row.ParentID, strings.ToLower(input.Name), id).Count(&count).Error; e != nil {
			return e
		}
		if count > 0 {
			return invalidLibraryMutation("INVALID_PAPER_LIBRARY", "同一目录下不能存在同名目录")
		}
		row.Name = input.Name
		row.NameKey = strings.ToLower(input.Name)
		row.UpdatedAt = time.Now().UTC()
		if !rename {
			return tx.Create(&row).Error
		}
		previous := row.Revision
		row.Revision++
		result := tx.Model(&orm.PaperLibraryFolder{}).Where("dataset_id=? AND id=? AND revision=?", dataset, id, previous).Select("name", "name_key", "revision", "updated_at").Updates(&row)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return paperlibrary.ErrConflict
		}
		return nil
	})
	if err != nil {
		replyLibraryError(w, err)
		return
	}
	data := paperlibrary.EmptyData()
	data.Folders = []paperlibrary.Folder{{ID: row.ID, Name: row.Name, ParentID: paperlibrary.Pointer(row.ParentID), Revision: row.Revision}}
	common.ReplyJSON(w, libraryChanges{Data: data})
}
func DeleteLibraryFolder(w http.ResponseWriter, r *http.Request) {
	dataset, _, ok := libraryAccess(w, r, true)
	if !ok {
		return
	}
	var input struct {
		Revision int64 `json:"revision"`
	}
	if !parseJSON(r, &input) || input.Revision < 1 {
		replyError(w, 400, "INVALID_REQUEST", "revision 无效")
		return
	}
	id := mux.Vars(r)["folder_id"]
	folderIDs, affected := []string{}, []string{}
	err := store.DB().WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var root orm.PaperLibraryFolder
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("dataset_id=? AND id=?", dataset, id).Take(&root).Error; e != nil {
			return e
		}
		if root.Revision != input.Revision {
			return paperlibrary.ErrConflict
		}
		if e := paperlibrary.CheckFolder(tx, dataset, root.ParentID); e != nil {
			return e
		}
		folderIDs = []string{id}
		level := []string{id}
		for len(level) > 0 {
			var children []orm.PaperLibraryFolder
			if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("dataset_id=? AND parent_id IN ?", dataset, level).Find(&children).Error; e != nil {
				return e
			}
			level = nil
			for _, child := range children {
				folderIDs = append(folderIDs, child.ID)
				level = append(level, child.ID)
			}
		}
		var entries []orm.PaperLibraryEntry
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("dataset_id=? AND folder_id IN ? AND removed_at IS NULL", dataset, folderIDs).Order("id").Find(&entries).Error; e != nil {
			return e
		}
		for _, row := range entries {
			row.FolderID = root.ParentID
			if e := paperlibrary.SaveEntry(tx, &row); e != nil {
				return e
			}
			affected = append(affected, row.ID)
		}
		return tx.Where("dataset_id=? AND id IN ?", dataset, folderIDs).Delete(&orm.PaperLibraryFolder{}).Error
	})
	if err != nil {
		replyLibraryError(w, err)
		return
	}
	out, e := libraryResult(r.Context(), store.DB(), dataset, store.UserID(r), affected)
	if e != nil {
		replyLibraryError(w, e)
		return
	}
	out.RemovedFolderIDs = folderIDs
	common.ReplyJSON(w, out)
}
