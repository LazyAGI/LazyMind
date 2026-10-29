package academic

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type zoteroDatabaseLibrary struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	Name       string `json:"name"`
	internalID int64
}
type zoteroDatabaseMetadata struct {
	Path        string                  `json:"path"`
	Libraries   []zoteroDatabaseLibrary `json:"libraries"`
	LibraryType string                  `json:"library_type"`
	LibraryID   string                  `json:"library_id"`
}

func newZoteroDatabaseClient(config zoteroConfig) (*zoteroClient, error) {
	location, err := discoverZoteroDatabase()
	if err != nil {
		return nil, err
	}
	config.APIKey = ""
	config.LocalURL = ""
	config.WebDAV = nil
	return &zoteroClient{config: config, http: &http.Client{}, database: location}, nil
}

type zoteroDatabaseSchema map[string]map[string]bool

func (s zoteroDatabaseSchema) has(table, column string) bool { return s[table][column] }
func (s zoteroDatabaseSchema) optional(table, column, fallback string) string {
	if s.has(table, column) {
		return "COALESCE(" + column + "," + fallback + ")"
	}
	return fallback
}

func openZoteroReadTransaction(ctx context.Context, location *zoteroDatabaseLocation) (*sql.Tx, func(), error) {
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(location.Path)}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	q.Add("_pragma", "busy_timeout(3000)")
	q.Add("_pragma", "trusted_schema(0)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, nil, zoteroDatabaseReadError(err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(0)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		db.Close()
		return nil, nil, zoteroDatabaseReadError(err)
	}
	cleanup := func() { _ = tx.Rollback(); _ = db.Close() }
	return tx, cleanup, nil
}

func readZoteroDatabaseSchema(ctx context.Context, tx *sql.Tx) (zoteroDatabaseSchema, error) {
	schema := zoteroDatabaseSchema{}
	// Fixed identifiers only. Checking columns supports old/new Zotero versions
	// without hard-coding a schema version or silently omitting required data.
	tables := []string{"items", "libraries", "groups", "settings", "itemTypes", "itemTypesCombined", "fields", "fieldsCombined", "itemData", "itemDataValues", "deletedItems", "collections", "deletedCollections", "collectionItems", "creators", "creatorTypes", "itemCreators", "tags", "itemTags", "itemNotes", "itemAttachments", "itemAnnotations", "itemRelations", "relationPredicates"}
	for _, table := range tables {
		columns := map[string]bool{}
		err := zoteroDatabaseEach(ctx, tx, "PRAGMA table_info("+table+")", nil, func(rows *sql.Rows) error {
			var id, notnull, primary int
			var name, kind string
			var defaultValue any
			if err := rows.Scan(&id, &name, &kind, &notnull, &defaultValue, &primary); err != nil {
				return err
			}
			columns[name] = true
			return nil
		})
		if err != nil {
			return nil, zoteroDatabaseReadError(err)
		}
		schema[table] = columns
	}
	required := map[string][]string{
		"items": {"itemID", "itemTypeID", "libraryID", "key"}, "libraries": {"libraryID", "type"}, "itemData": {"itemID", "fieldID", "valueID"}, "itemDataValues": {"valueID", "value"}, "deletedItems": {"itemID"},
		"collections": {"collectionID", "libraryID", "key", "collectionName", "parentCollectionID"}, "collectionItems": {"collectionID", "itemID"}, "creators": {"creatorID", "firstName", "lastName", "fieldMode"}, "creatorTypes": {"creatorTypeID", "creatorType"}, "itemCreators": {"itemID", "creatorID", "creatorTypeID", "orderIndex"},
		"itemNotes": {"itemID", "parentItemID", "note"}, "itemAttachments": {"itemID", "parentItemID", "linkMode", "contentType", "path"},
	}
	for table, columns := range required {
		for _, column := range columns {
			if !schema.has(table, column) {
				return nil, zoteroUnsupportedSchema()
			}
		}
	}
	if !(schema.has("fieldsCombined", "fieldID") && schema.has("fieldsCombined", "fieldName")) && !(schema.has("fields", "fieldID") && schema.has("fields", "fieldName")) {
		return nil, zoteroUnsupportedSchema()
	}
	if !(schema.has("itemTypesCombined", "itemTypeID") && schema.has("itemTypesCombined", "typeName")) && !(schema.has("itemTypes", "itemTypeID") && schema.has("itemTypes", "typeName")) {
		return nil, zoteroUnsupportedSchema()
	}
	for table, columns := range map[string][]string{
		"groups": {"groupID", "libraryID", "name"}, "settings": {"setting", "key", "value"}, "deletedCollections": {"collectionID"}, "tags": {"tagID", "name"}, "itemTags": {"itemID", "tagID", "type"},
		"itemAnnotations": {"itemID", "parentItemID", "type", "text", "comment", "color", "pageLabel", "position"}, "itemRelations": {"itemID", "predicateID", "object"}, "relationPredicates": {"predicateID", "predicate"},
	} {
		if len(schema[table]) > 0 {
			for _, column := range columns {
				if !schema.has(table, column) {
					return nil, zoteroUnsupportedSchema()
				}
			}
		}
	}
	return schema, nil
}

func zoteroUnsupportedSchema() *zoteroError {
	return zoteroFailure(422, "ZOTERO_DATABASE_UNSUPPORTED", "此 Zotero 数据库缺少导入所需的数据表或字段，请更新 Zotero 或使用 API 导入")
}
func zoteroDatabaseReadError(err error) error {
	var known *zoteroError
	if errors.As(err, &known) {
		return known
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return zoteroFailure(408, "ZOTERO_DATABASE_TIMEOUT", "读取本机 Zotero 数据库超时，请稍后重试")
	}
	return zoteroFailure(503, "ZOTERO_DATABASE_READ_FAILED", "无法只读访问 Zotero 数据库，请确认数据库及 WAL 文件可读取，或关闭 Zotero 后重试")
}

func zoteroDatabaseEach(ctx context.Context, tx *sql.Tx, query string, args []any, visit func(*sql.Rows) error) error {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := visit(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (c *zoteroClient) databaseLibrary(ctx context.Context, tx *sql.Tx, schema zoteroDatabaseSchema) (zoteroDatabaseLibrary, []zoteroDatabaseLibrary, string, error) {
	libraries := []zoteroDatabaseLibrary{}
	accountID := ""
	if len(schema["settings"]) > 0 {
		var value string
		err := tx.QueryRowContext(ctx, "SELECT CAST(value AS TEXT) FROM settings WHERE setting='account' AND key='userID'").Scan(&value)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return zoteroDatabaseLibrary{}, nil, "", err
		}
		if zoteroLibraryID.MatchString(value) && value != "0" {
			accountID = value
		}
	}
	err := zoteroDatabaseEach(ctx, tx, "SELECT libraryID,type FROM libraries WHERE type IN ('user','group') ORDER BY CASE type WHEN 'user' THEN 0 ELSE 1 END,libraryID", nil, func(rows *sql.Rows) error {
		var id int64
		var kind string
		if err := rows.Scan(&id, &kind); err != nil {
			return err
		}
		lib := zoteroDatabaseLibrary{internalID: id}
		if kind == "user" {
			lib.Type = "users"
			lib.ID = "0"
			lib.Name = "我的文献库"
		} else {
			lib.Type = "groups"
		}
		libraries = append(libraries, lib)
		return nil
	})
	if err != nil {
		return zoteroDatabaseLibrary{}, nil, "", err
	}
	for i := range libraries {
		if libraries[i].Type == "groups" {
			if len(schema["groups"]) == 0 {
				return zoteroDatabaseLibrary{}, nil, "", zoteroUnsupportedSchema()
			}
			var groupID int64
			if err := tx.QueryRowContext(ctx, "SELECT groupID,name FROM groups WHERE libraryID=?", libraries[i].internalID).Scan(&groupID, &libraries[i].Name); err != nil {
				return zoteroDatabaseLibrary{}, nil, "", err
			}
			libraries[i].ID = strconv.FormatInt(groupID, 10)
		}
	}
	for _, lib := range libraries {
		if c.config.LibraryType == lib.Type && (c.config.LibraryID == lib.ID || (lib.Type == "users" && accountID != "" && c.config.LibraryID == accountID)) {
			source := "groups:" + lib.ID
			if lib.Type == "users" {
				if accountID != "" {
					source = "users:" + accountID
				} else {
					hash := sha256.Sum256([]byte(c.database.Dir))
					source = "localdb:" + hex.EncodeToString(hash[:16]) + ":users:" + strconv.FormatInt(lib.internalID, 10)
				}
			}
			return lib, libraries, source, nil
		}
	}
	return zoteroDatabaseLibrary{}, nil, "", zoteroFailure(404, "ZOTERO_DATABASE_LIBRARY_NOT_FOUND", "本机 Zotero 中未找到所选文献库，请重新选择个人或群组文献库")
}

type zoteroDatabaseItem struct {
	ID, Parent int64
	Key, Kind  string
	Deleted    bool
	Data       map[string]any
	Attachment *zoteroDatabaseAttachment
}
type zoteroDatabaseAttachment struct {
	Key, Path, ContentType, Hash string
	Mode                         int
}

func (c *zoteroClient) previewDatabase(ctx context.Context) (zoteroPreview, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	result := zoteroPreview{Items: []json.RawMessage{}, Collections: []json.RawMessage{}, Warnings: []string{}}
	tx, closeDB, err := openZoteroReadTransaction(ctx, c.database)
	if err != nil {
		return result, err
	}
	defer closeDB()
	schema, err := readZoteroDatabaseSchema(ctx, tx)
	if err != nil {
		return result, err
	}
	lib, libraries, source, err := c.databaseLibrary(ctx, tx, schema)
	if err != nil {
		return result, zoteroDatabaseReadError(err)
	}
	result.SourceID = source
	result.Database = &zoteroDatabaseMetadata{Path: c.database.Path, Libraries: libraries, LibraryType: lib.Type, LibraryID: lib.ID}
	collectionKeys, collections, err := readZoteroDatabaseCollections(ctx, tx, schema, lib.internalID)
	if err != nil {
		return result, zoteroDatabaseReadError(err)
	}
	result.Collections = collections
	items, order, err := readZoteroDatabaseItems(ctx, tx, schema, lib.internalID, collectionKeys)
	if err != nil {
		return result, zoteroDatabaseReadError(err)
	}
	visible := map[int64]bool{}
	visiting := map[int64]bool{}
	var active func(int64, int) (bool, error)
	active = func(id int64, depth int) (bool, error) {
		if value, ok := visible[id]; ok {
			return value, nil
		}
		item := items[id]
		if item == nil {
			return false, zoteroFailure(422, "ZOTERO_DATABASE_INVALID_GRAPH", "Zotero 条目引用了不存在或属于其他文献库的父条目")
		}
		if item.Deleted {
			return false, nil
		}
		if visiting[id] || depth > 32 {
			return false, zoteroFailure(422, "ZOTERO_DATABASE_INVALID_GRAPH", "Zotero 条目存在循环或过深的父子关系")
		}
		visiting[id] = true
		ok := true
		if item.Parent != 0 {
			var err error
			ok, err = active(item.Parent, depth+1)
			if err != nil {
				return false, err
			}
		}
		delete(visiting, id)
		visible[id] = ok
		return ok, nil
	}
	// Bound I/O concurrency on mounted directories and reuse confined root handles.
	// Only active PDF attachments are checked; original files are never copied.
	toCheck := map[int64]*zoteroDatabaseAttachment{}
	for _, id := range order {
		ok, err := active(id, 0)
		if err != nil {
			return result, err
		}
		if a := items[id].Attachment; ok && a != nil && a.ContentType == "application/pdf" {
			toCheck[id] = a
		}
	}
	availability := c.databaseAttachmentAvailability(ctx, toCheck)
	if ctx.Err() != nil {
		return result, zoteroDatabaseReadError(ctx.Err())
	}
	missing, papers, bytes := 0, 0, 0
	for _, id := range order {
		item := items[id]
		ok, err := active(id, 0)
		if err != nil {
			return result, err
		}
		if !ok {
			continue
		}
		if item.Parent != 0 {
			item.Data["parentItem"] = items[item.Parent].Key
		}
		if available, checked := availability[id]; checked {
			item.Data["localFileAvailable"] = available
			if !available {
				missing++
			}
		}
		if item.Kind != "attachment" && item.Kind != "note" && item.Kind != "annotation" {
			papers++
			if papers > 10000 {
				return result, zoteroFailure(413, "ZOTERO_LIBRARY_TOO_LARGE", "单次最多导入 10000 篇文献，请分批导入")
			}
		}
		raw, err := json.Marshal(map[string]any{"key": item.Key, "data": item.Data})
		if err != nil {
			return result, zoteroDatabaseReadError(err)
		}
		bytes += len(raw)
		if bytes > zoteroMaxMetadataBytes {
			return result, zoteroFailure(413, "ZOTERO_LIBRARY_TOO_LARGE", "Zotero 题录超过 20 MiB 导入上限，请分批导入")
		}
		result.Items = append(result.Items, raw)
	}
	for _, raw := range result.Collections {
		bytes += len(raw)
	}
	if bytes > zoteroMaxMetadataBytes {
		return result, zoteroFailure(413, "ZOTERO_LIBRARY_TOO_LARGE", "Zotero 题录超过 20 MiB 导入上限，请分批导入")
	}
	if missing > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%d 个 PDF 附件在本机不可读取（未下载、路径来自其他电脑或未配置关联文件目录）；题录、笔记和批注仍可导入。", missing))
	}
	return result, nil
}

// Roots remain open only for this preview. os.Root retains symlink confinement;
// the actual import still reopens and validates the PDF, since files may change.
func (c *zoteroClient) databaseAttachmentAvailability(ctx context.Context, attachments map[int64]*zoteroDatabaseAttachment) map[int64]bool {
	roots := map[string]*os.Root{}
	for _, path := range []string{c.database.Dir, c.database.LinkedDir} {
		if path != "" && roots[path] == nil {
			root, err := os.OpenRoot(path)
			if err == nil {
				roots[path] = root
				defer root.Close()
			}
		}
	}
	type check struct {
		id         int64
		attachment *zoteroDatabaseAttachment
	}
	jobs := make(chan check, len(attachments))
	for id, attachment := range attachments {
		jobs <- check{id, attachment}
	}
	close(jobs)
	result := make(map[int64]bool, len(attachments))
	var mu sync.Mutex
	var workers sync.WaitGroup
	for i := 0; i < 4 && i < len(attachments); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range jobs {
				if ctx.Err() != nil {
					return
				}
				root, relative, err := c.databaseAttachmentPath(*job.attachment)
				available := false
				if err == nil && roots[root] != nil {
					f, openErr := roots[root].Open(relative)
					if openErr == nil {
						info, statErr := f.Stat()
						available = statErr == nil && info.Mode().IsRegular() && info.Size() <= zoteroMaxPDFBytes
						_ = f.Close()
					}
				}
				mu.Lock()
				result[job.id] = available
				mu.Unlock()
			}
		}()
	}
	workers.Wait()
	return result
}

func readZoteroDatabaseCollections(ctx context.Context, tx *sql.Tx, s zoteroDatabaseSchema, library int64) (map[int64]string, []json.RawMessage, error) {
	type collection struct {
		id, parent int64
		key, name  string
		deleted    bool
	}
	all := map[int64]collection{}
	order := []int64{}
	deleted := "0"
	if s.has("deletedCollections", "collectionID") {
		deleted = "EXISTS(SELECT 1 FROM deletedCollections d WHERE d.collectionID=c.collectionID)"
	}
	err := zoteroDatabaseEach(ctx, tx, "SELECT c.collectionID,COALESCE(c.parentCollectionID,0),c.key,c.collectionName,"+deleted+" FROM collections c WHERE c.libraryID=? ORDER BY c.collectionID", []any{library}, func(rows *sql.Rows) error {
		var value collection
		if err := rows.Scan(&value.id, &value.parent, &value.key, &value.name, &value.deleted); err != nil {
			return err
		}
		if !zoteroItemKey.MatchString(value.key) || value.name == "" {
			return invalidZoteroCollectionReference()
		}
		if len(order) >= 10000 {
			return zoteroFailure(413, "ZOTERO_LIBRARY_TOO_LARGE", "Zotero 文件夹超过单次导入上限")
		}
		all[value.id] = value
		order = append(order, value.id)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	states := map[int64]int{}
	var active func(int64, int) (bool, error)
	active = func(id int64, depth int) (bool, error) {
		if states[id] == 2 {
			return true, nil
		}
		if states[id] == 3 {
			return false, nil
		}
		if states[id] == 1 || depth > 32 {
			return false, invalidZoteroCollectionReference()
		}
		value, ok := all[id]
		if !ok {
			return false, invalidZoteroCollectionReference()
		}
		if value.deleted {
			states[id] = 3
			return false, nil
		}
		states[id] = 1
		if value.parent != 0 {
			keep, err := active(value.parent, depth+1)
			if err != nil {
				return false, err
			}
			if !keep {
				states[id] = 3
				return false, nil
			}
		}
		states[id] = 2
		return true, nil
	}
	keys := map[int64]string{}
	for id := range all {
		// An empty value is a known trashed collection. An absent ID is a
		// broken relationship and must not silently lose organization data.
		keys[id] = ""
	}
	result := []json.RawMessage{}
	seen := map[string]bool{}
	for _, id := range order {
		keep, err := active(id, 0)
		if err != nil {
			return nil, nil, err
		}
		if !keep {
			continue
		}
		value := all[id]
		if seen[value.key] {
			return nil, nil, invalidZoteroCollectionReference()
		}
		seen[value.key] = true
		var parent any = false
		if value.parent != 0 {
			parent = all[value.parent].key
		}
		raw, _ := json.Marshal(map[string]any{"key": value.key, "data": map[string]any{"key": value.key, "name": value.name, "parentCollection": parent}})
		keys[id] = value.key
		result = append(result, raw)
	}
	return keys, result, nil
}

func readZoteroDatabaseItems(ctx context.Context, tx *sql.Tx, s zoteroDatabaseSchema, library int64, collections map[int64]string) (map[int64]*zoteroDatabaseItem, []int64, error) {
	items := map[int64]*zoteroDatabaseItem{}
	order := []int64{}
	seen := map[string]bool{}
	types := "itemTypes"
	if s.has("itemTypesCombined", "typeName") {
		types = "itemTypesCombined"
	}
	dateAdded := s.optional("items", "dateAdded", "''")
	dateModified := s.optional("items", "dateModified", "''")
	version := s.optional("items", "version", "0")
	query := "SELECT i.itemID,i.key,COALESCE(t.typeName,'')," + dateAdded + "," + dateModified + "," + version + ",EXISTS(SELECT 1 FROM deletedItems d WHERE d.itemID=i.itemID) FROM items i LEFT JOIN " + types + " t ON t.itemTypeID=i.itemTypeID WHERE i.libraryID=? ORDER BY i.itemID"
	err := zoteroDatabaseEach(ctx, tx, query, []any{library}, func(rows *sql.Rows) error {
		item := &zoteroDatabaseItem{}
		var added, modified string
		var version int64
		if err := rows.Scan(&item.ID, &item.Key, &item.Kind, &added, &modified, &version, &item.Deleted); err != nil {
			return err
		}
		if !zoteroItemKey.MatchString(item.Key) || seen[item.Key] || item.Kind == "" {
			return zoteroUnsupportedSchema()
		}
		seen[item.Key] = true
		if len(order) >= zoteroMaxObjects {
			return zoteroFailure(413, "ZOTERO_LIBRARY_TOO_LARGE", "Zotero 条目超过 50000 项，请分批导入")
		}
		if item.Kind == "annotation" && len(s["itemAnnotations"]) == 0 {
			return zoteroUnsupportedSchema()
		}
		item.Data = map[string]any{"key": item.Key, "itemType": item.Kind, "dateAdded": added, "dateModified": modified, "version": version, "creators": []any{}, "collections": []string{}, "tags": []any{}, "relations": map[string][]string{}}
		items[item.ID] = item
		order = append(order, item.ID)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	fields := "fields"
	if s.has("fieldsCombined", "fieldName") {
		fields = "fieldsCombined"
	}
	err = zoteroDatabaseEach(ctx, tx, "SELECT d.itemID,f.fieldName,COALESCE(CAST(v.value AS TEXT),'') FROM itemData d JOIN items i ON i.itemID=d.itemID JOIN "+fields+" f ON f.fieldID=d.fieldID JOIN itemDataValues v ON v.valueID=d.valueID WHERE i.libraryID=?", []any{library}, func(rows *sql.Rows) error {
		var id int64
		var name, value string
		if err := rows.Scan(&id, &name, &value); err != nil {
			return err
		}
		if item := items[id]; item != nil {
			switch name {
			case "key", "itemType", "creators", "collections", "tags", "relations", "parentItem":
				return zoteroUnsupportedSchema()
			}
			if len(value) > zoteroMaxMetadataBytes {
				return zoteroFailure(413, "ZOTERO_LIBRARY_TOO_LARGE", "Zotero 字段超过导入大小上限")
			}
			item.Data[name] = value
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	err = zoteroDatabaseEach(ctx, tx, "SELECT ic.itemID,c.firstName,c.lastName,c.fieldMode,t.creatorType FROM itemCreators ic JOIN items i ON i.itemID=ic.itemID JOIN creators c ON c.creatorID=ic.creatorID JOIN creatorTypes t ON t.creatorTypeID=ic.creatorTypeID WHERE i.libraryID=? ORDER BY ic.itemID,ic.orderIndex", []any{library}, func(rows *sql.Rows) error {
		var id int64
		var first, last, kind string
		var mode int
		if err := rows.Scan(&id, &first, &last, &mode, &kind); err != nil {
			return err
		}
		if item := items[id]; item != nil {
			creator := map[string]any{"creatorType": kind}
			if mode == 1 {
				creator["name"] = last
			} else {
				creator["firstName"] = first
				creator["lastName"] = last
			}
			item.Data["creators"] = append(item.Data["creators"].([]any), creator)
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	err = zoteroDatabaseEach(ctx, tx, "SELECT ci.itemID,ci.collectionID FROM collectionItems ci JOIN items i ON i.itemID=ci.itemID WHERE i.libraryID=? ORDER BY ci.collectionID", []any{library}, func(rows *sql.Rows) error {
		var id, collection int64
		if err := rows.Scan(&id, &collection); err != nil {
			return err
		}
		if item := items[id]; item != nil {
			key, ok := collections[collection]
			if !ok {
				return invalidZoteroCollectionReference()
			}
			if key != "" {
				item.Data["collections"] = append(item.Data["collections"].([]string), key)
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if len(s["tags"]) > 0 && len(s["itemTags"]) > 0 {
		err = zoteroDatabaseEach(ctx, tx, "SELECT it.itemID,t.name,it.type FROM itemTags it JOIN items i ON i.itemID=it.itemID JOIN tags t ON t.tagID=it.tagID WHERE i.libraryID=? ORDER BY it.tagID", []any{library}, func(rows *sql.Rows) error {
			var id int64
			var tag string
			var kind int
			if err := rows.Scan(&id, &tag, &kind); err != nil {
				return err
			}
			if item := items[id]; item != nil {
				item.Data["tags"] = append(item.Data["tags"].([]any), map[string]any{"tag": tag, "type": kind})
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	err = zoteroDatabaseEach(ctx, tx, "SELECT n.itemID,COALESCE(n.parentItemID,0),COALESCE(n.note,'') FROM itemNotes n JOIN items i ON i.itemID=n.itemID WHERE i.libraryID=?", []any{library}, func(rows *sql.Rows) error {
		var id, parent int64
		var note string
		if err := rows.Scan(&id, &parent, &note); err != nil {
			return err
		}
		if item := items[id]; item != nil {
			item.Parent = parent
			item.Data["note"] = note
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	hash := "''"
	if s.has("itemAttachments", "storageHash") {
		hash = "COALESCE(a.storageHash,'')"
	}
	err = zoteroDatabaseEach(ctx, tx, "SELECT a.itemID,COALESCE(a.parentItemID,0),COALESCE(a.linkMode,3),COALESCE(a.contentType,''),COALESCE(a.path,''),"+hash+" FROM itemAttachments a JOIN items i ON i.itemID=a.itemID WHERE i.libraryID=?", []any{library}, func(rows *sql.Rows) error {
		var id, parent int64
		var a zoteroDatabaseAttachment
		if err := rows.Scan(&id, &parent, &a.Mode, &a.ContentType, &a.Path, &a.Hash); err != nil {
			return err
		}
		if item := items[id]; item != nil {
			a.Key = item.Key
			item.Parent = parent
			item.Attachment = &a
			item.Data["contentType"] = a.ContentType
			item.Data["linkMode"] = zoteroDatabaseLinkMode(a.Mode)
			item.Data["filename"] = zoteroDatabaseFilename(a.Path)
			if zoteroMD5.MatchString(a.Hash) {
				item.Data["md5"] = a.Hash
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if len(s["itemAnnotations"]) > 0 {
		author := "''"
		if s.has("itemAnnotations", "authorName") {
			author = "COALESCE(a.authorName,'')"
		}
		sortIndex := "''"
		if s.has("itemAnnotations", "sortIndex") {
			sortIndex = "COALESCE(a.sortIndex,'')"
		}
		err = zoteroDatabaseEach(ctx, tx, "SELECT a.itemID,a.parentItemID,a.type,"+author+",COALESCE(a.text,''),COALESCE(a.comment,''),COALESCE(a.color,''),COALESCE(a.pageLabel,''),"+sortIndex+",COALESCE(a.position,'') FROM itemAnnotations a JOIN items i ON i.itemID=a.itemID WHERE i.libraryID=?", []any{library}, func(rows *sql.Rows) error {
			var id, parent int64
			var kind int
			var author, text, comment, color, page, sortIndex, position string
			if err := rows.Scan(&id, &parent, &kind, &author, &text, &comment, &color, &page, &sortIndex, &position); err != nil {
				return err
			}
			if item := items[id]; item != nil {
				if kind < 1 || kind > 6 || !json.Valid([]byte(position)) {
					return zoteroUnsupportedSchema()
				}
				item.Parent = parent
				item.Data["annotationType"] = []string{"", "highlight", "note", "image", "ink", "underline", "text"}[kind]
				item.Data["annotationAuthorName"] = author
				item.Data["annotationText"] = text
				item.Data["annotationComment"] = comment
				item.Data["annotationColor"] = color
				item.Data["annotationPageLabel"] = page
				item.Data["annotationSortIndex"] = sortIndex
				item.Data["annotationPosition"] = position
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	if len(s["itemRelations"]) > 0 && len(s["relationPredicates"]) > 0 {
		err = zoteroDatabaseEach(ctx, tx, "SELECT r.itemID,p.predicate,r.object FROM itemRelations r JOIN items i ON i.itemID=r.itemID JOIN relationPredicates p ON p.predicateID=r.predicateID WHERE i.libraryID=?", []any{library}, func(rows *sql.Rows) error {
			var id int64
			var predicate, object string
			if err := rows.Scan(&id, &predicate, &object); err != nil {
				return err
			}
			if item := items[id]; item != nil {
				relations := item.Data["relations"].(map[string][]string)
				relations[predicate] = append(relations[predicate], object)
			}
			return nil
		})
		if err != nil {
			return nil, nil, err
		}
	}
	return items, order, nil
}

func zoteroDatabaseLinkMode(mode int) string {
	switch mode {
	case 0:
		return "imported_file"
	case 1:
		return "imported_url"
	case 2:
		return "linked_file"
	case 4:
		return "embedded_image"
	default:
		return "linked_url"
	}
}
func zoteroDatabaseFilename(path string) string {
	path = strings.TrimPrefix(strings.TrimPrefix(path, "storage:"), "attachments:")
	return filepath.Base(strings.ReplaceAll(path, "\\", "/"))
}

func (c *zoteroClient) databaseAttachmentPath(a zoteroDatabaseAttachment) (string, string, error) {
	if !zoteroItemKey.MatchString(a.Key) || a.ContentType != "application/pdf" {
		return "", "", zoteroLocalAttachmentUnavailable()
	}
	if a.Mode == 0 || a.Mode == 1 {
		if !strings.HasPrefix(a.Path, "storage:") {
			return "", "", zoteroLocalAttachmentUnavailable()
		}
		relative := strings.TrimPrefix(a.Path, "storage:")
		if !safeZoteroRelativePath(relative) {
			return "", "", zoteroLocalAttachmentUnavailable()
		}
		return c.database.Dir, filepath.Join("storage", a.Key, filepath.FromSlash(relative)), nil
	}
	if a.Mode != 2 {
		return "", "", zoteroLocalAttachmentUnavailable()
	}
	if strings.HasPrefix(a.Path, "attachments:") {
		relative := strings.TrimPrefix(a.Path, "attachments:")
		if c.database.LinkedDir == "" || !filepath.IsAbs(c.database.LinkedDir) || !safeZoteroRelativePath(relative) {
			return "", "", zoteroLocalAttachmentUnavailable()
		}
		return c.database.LinkedDir, filepath.FromSlash(relative), nil
	}
	if !filepath.IsAbs(a.Path) || strings.ContainsAny(a.Path, "\x00\r\n") {
		return "", "", zoteroLocalAttachmentUnavailable()
	}
	for _, root := range []string{c.database.Dir, c.database.LinkedDir} {
		if root == "" || !filepath.IsAbs(root) {
			continue
		}
		relative, err := filepath.Rel(root, a.Path)
		if err == nil && safeZoteroRelativePath(filepath.ToSlash(relative)) {
			return root, relative, nil
		}
	}
	return "", "", zoteroLocalAttachmentUnavailable()
}

func safeZoteroRelativePath(path string) bool {
	if path == "" || strings.ContainsAny(path, "\\\x00\r\n:") || strings.HasPrefix(path, "/") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

// os.Root enforces confinement during the actual open, including symlink races.
// A preflight EvalSymlinks check alone would leave a check/open race.
func openZoteroRootedFile(root, path string) (*os.File, error) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return dir.Open(path)
}
func zoteroLocalAttachmentUnavailable() *zoteroError {
	return zoteroFailure(404, "ZOTERO_DATABASE_ATTACHMENT_UNAVAILABLE", "此 PDF 在本机不可读取，请在 Zotero 中下载附件；关联文件需位于受信任的数据目录或已配置的关联文件目录")
}

func (c *zoteroClient) attachmentDatabase(ctx context.Context, key string) (zoteroFile, error) {
	if !zoteroItemKey.MatchString(key) {
		return zoteroFile{}, zoteroFailure(400, "INVALID_ZOTERO_ITEM_KEY", "Zotero 附件编号无效")
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	tx, closeDB, err := openZoteroReadTransaction(ctx, c.database)
	if err != nil {
		return zoteroFile{}, err
	}
	defer closeDB()
	s, err := readZoteroDatabaseSchema(ctx, tx)
	if err != nil {
		return zoteroFile{}, err
	}
	lib, _, _, err := c.databaseLibrary(ctx, tx, s)
	if err != nil {
		return zoteroFile{}, zoteroDatabaseReadError(err)
	}
	hash := "''"
	if s.has("itemAttachments", "storageHash") {
		hash = "COALESCE(a.storageHash,'')"
	}
	a := zoteroDatabaseAttachment{Key: key}
	var itemID, parent int64
	err = tx.QueryRowContext(ctx, "SELECT i.itemID,COALESCE(a.parentItemID,0),COALESCE(a.linkMode,3),COALESCE(a.contentType,''),COALESCE(a.path,''),"+hash+" FROM items i JOIN itemAttachments a ON a.itemID=i.itemID WHERE i.libraryID=? AND i.key=? AND NOT EXISTS(SELECT 1 FROM deletedItems d WHERE d.itemID=i.itemID)", lib.internalID, key).Scan(&itemID, &parent, &a.Mode, &a.ContentType, &a.Path, &a.Hash)
	if errors.Is(err, sql.ErrNoRows) {
		return zoteroFile{}, zoteroLocalAttachmentUnavailable()
	}
	if err != nil {
		return zoteroFile{}, zoteroDatabaseReadError(err)
	}
	if parent != 0 {
		var active int
		err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM items i WHERE i.itemID=? AND i.libraryID=? AND NOT EXISTS(SELECT 1 FROM deletedItems d WHERE d.itemID=i.itemID)", parent, lib.internalID).Scan(&active)
		if err != nil {
			return zoteroFile{}, zoteroDatabaseReadError(err)
		}
		if active != 1 {
			return zoteroFile{}, zoteroLocalAttachmentUnavailable()
		}
	}
	root, relative, err := c.databaseAttachmentPath(a)
	if err != nil {
		return zoteroFile{}, err
	}
	f, err := openZoteroRootedFile(root, relative)
	if err != nil {
		return zoteroFile{}, zoteroLocalAttachmentUnavailable()
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return zoteroFile{}, zoteroLocalAttachmentUnavailable()
	}
	if info.Size() > zoteroMaxPDFBytes {
		return zoteroFile{}, zoteroFailure(413, "ZOTERO_RESPONSE_TOO_LARGE", "PDF 附件超过 128 MiB 导入上限")
	}
	data, err := io.ReadAll(io.LimitReader(f, zoteroMaxPDFBytes+1))
	if err != nil {
		return zoteroFile{}, zoteroLocalAttachmentUnavailable()
	}
	if len(data) > zoteroMaxPDFBytes {
		return zoteroFile{}, zoteroFailure(413, "ZOTERO_RESPONSE_TOO_LARGE", "PDF 附件超过 128 MiB 导入上限")
	}
	if len(data) < 5 || string(data[:5]) != "%PDF-" {
		return zoteroFile{}, zoteroFailure(422, "ZOTERO_INVALID_PDF", "Zotero 本地附件不是有效 PDF")
	}
	if zoteroMD5.MatchString(a.Hash) {
		sum := md5.Sum(data)
		if !strings.EqualFold(a.Hash, hex.EncodeToString(sum[:])) {
			return zoteroFile{}, zoteroFailure(409, "ZOTERO_ATTACHMENT_CHANGED", "附件与 Zotero 记录的校验值不一致，请完成同步后重试")
		}
	}
	return zoteroFile{Name: zoteroDatabaseFilename(a.Path), Data: data}, nil
}
