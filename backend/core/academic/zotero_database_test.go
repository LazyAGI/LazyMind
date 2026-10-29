package academic

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/paperlibrary"
)

// These fixtures deliberately keep the writer open: the importer must see
// committed WAL contents without copying or checkpointing the source database.
func zoteroDatabaseFixture(t *testing.T) (string, *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "zotero.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	const schema = `
PRAGMA journal_mode=WAL;
PRAGMA wal_autocheckpoint=0;
CREATE TABLE items(itemID INTEGER PRIMARY KEY,itemTypeID INT,libraryID INT,key TEXT,dateAdded TEXT,dateModified TEXT,version INT);
CREATE TABLE libraries(libraryID INTEGER PRIMARY KEY,type TEXT);
CREATE TABLE groups(groupID INTEGER PRIMARY KEY,libraryID INT,name TEXT);
CREATE TABLE settings(setting TEXT,key TEXT,value);
CREATE TABLE itemTypes(itemTypeID INTEGER PRIMARY KEY,typeName TEXT);
CREATE TABLE fields(fieldID INTEGER PRIMARY KEY,fieldName TEXT);
CREATE TABLE itemData(itemID INT,fieldID INT,valueID INT);
CREATE TABLE itemDataValues(valueID INTEGER PRIMARY KEY,value TEXT);
CREATE TABLE deletedItems(itemID INTEGER PRIMARY KEY);
CREATE TABLE collections(collectionID INTEGER PRIMARY KEY,libraryID INT,key TEXT,collectionName TEXT,parentCollectionID INT);
CREATE TABLE deletedCollections(collectionID INTEGER PRIMARY KEY);
CREATE TABLE collectionItems(collectionID INT,itemID INT,orderIndex INT);
CREATE TABLE creators(creatorID INTEGER PRIMARY KEY,firstName TEXT,lastName TEXT,fieldMode INT);
CREATE TABLE creatorTypes(creatorTypeID INTEGER PRIMARY KEY,creatorType TEXT);
CREATE TABLE itemCreators(itemID INT,creatorID INT,creatorTypeID INT,orderIndex INT);
CREATE TABLE tags(tagID INTEGER PRIMARY KEY,name TEXT);
CREATE TABLE itemTags(itemID INT,tagID INT,type INT);
CREATE TABLE itemNotes(itemID INTEGER PRIMARY KEY,parentItemID INT,note TEXT);
CREATE TABLE itemAttachments(itemID INTEGER PRIMARY KEY,parentItemID INT,linkMode INT,contentType TEXT,path TEXT,storageHash TEXT);
CREATE TABLE itemAnnotations(itemID INTEGER PRIMARY KEY,parentItemID INT,type INT,authorName TEXT,text TEXT,comment TEXT,color TEXT,pageLabel TEXT,sortIndex TEXT,position TEXT,isExternal INT);
CREATE TABLE relationPredicates(predicateID INTEGER PRIMARY KEY,predicate TEXT);
CREATE TABLE itemRelations(itemID INT,predicateID INT,object TEXT);
INSERT INTO libraries VALUES (1,'user'),(2,'group');
INSERT INTO groups VALUES (42,2,'Research group');
INSERT INTO settings VALUES ('account','userID','1234'),('account','username','must-not-be-in-preview');
INSERT INTO itemTypes VALUES (1,'journalArticle'),(2,'attachment'),(3,'note'),(4,'annotation');
INSERT INTO fields VALUES (1,'title'),(2,'DOI');
INSERT INTO items VALUES (1,1,1,'PAPER001','2026-01-01 00:00:00','2026-01-02 00:00:00',3),(2,2,1,'ATTACH01','','',1),(3,3,1,'NOTE0001','','',1),(4,4,1,'ANNOT001','','',1),(5,1,1,'DELETED1','','',1),(6,2,1,'ORPHAN01','','',1),(7,1,2,'GROUP001','','',1);
INSERT INTO itemDataValues VALUES (1,'Live WAL paper'),(2,'10.1234/example'),(3,'Group paper');
INSERT INTO itemData VALUES (1,1,1),(1,2,2),(7,1,3);
INSERT INTO deletedItems VALUES (5);
INSERT INTO collections VALUES (1,1,'FOLDER01','Root',NULL),(2,1,'FOLDER02','Nested',1),(3,1,'TRASH001','Trashed',NULL),(4,1,'TRASH002','Child of trash',3);
INSERT INTO deletedCollections VALUES (3);
INSERT INTO collectionItems VALUES (2,1,0),(3,1,1),(4,1,2);
INSERT INTO creators VALUES (1,'Ada','Lovelace',0),(2,'','Example Institute',1);
INSERT INTO creatorTypes VALUES (1,'author');
INSERT INTO itemCreators VALUES (1,2,1,1),(1,1,1,0);
INSERT INTO tags VALUES (1,'reading');
INSERT INTO itemTags VALUES (1,1,0);
INSERT INTO itemNotes VALUES (3,1,'<p>Note body</p>');
INSERT INTO itemAttachments VALUES (2,1,1,'application/pdf','storage:paper.pdf',''),(6,5,1,'application/pdf','storage:gone.pdf','');
INSERT INTO itemAnnotations VALUES (4,2,5,'Reader','Quoted text','Comment','#ffff00','2','00001','{"pageIndex":1,"rects":[[1,2,3,4]]}',0);
INSERT INTO relationPredicates VALUES (1,'dc:relation');
INSERT INTO itemRelations VALUES (1,1,'https://example.com/related');`
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(filepath.Join(dir, "storage", "ATTACH01"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "storage", "ATTACH01", "paper.pdf"), []byte("%PDF-1.7\nfixture"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_ZOTERO_DATA_DIR", dir)
	t.Setenv("LAZYMIND_ZOTERO_LINKED_ATTACHMENT_DIR", "")
	return dir, db
}

func databasePreviewForTest(t *testing.T, config zoteroConfig) zoteroPreview {
	t.Helper()
	config.Source = "database"
	client, err := newZoteroClient(config)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := client.preview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return preview
}

func previewDataForTest(t *testing.T, items []json.RawMessage) map[string]map[string]any {
	t.Helper()
	result := map[string]map[string]any{}
	for _, raw := range items {
		var envelope struct {
			Key  string         `json:"key"`
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		result[envelope.Key] = envelope.Data
	}
	return result
}

func TestZoteroDatabasePreviewReadsWALAndPreservesGraph(t *testing.T) {
	zoteroDatabaseFixture(t)
	preview := databasePreviewForTest(t, zoteroConfig{})
	items := previewDataForTest(t, preview.Items)
	if len(items) != 4 || len(preview.Collections) != 2 || preview.SourceID != "users:1234" {
		t.Fatalf("unexpected graph counts/source: items=%d collections=%d source=%s", len(items), len(preview.Collections), preview.SourceID)
	}
	paper := items["PAPER001"]
	if paper["title"] != "Live WAL paper" || paper["DOI"] != "10.1234/example" {
		t.Fatalf("metadata lost: %v", paper)
	}
	creators := paper["creators"].([]any)
	if len(creators) != 2 || creators[0].(map[string]any)["firstName"] != "Ada" || creators[1].(map[string]any)["name"] != "Example Institute" {
		t.Fatalf("creators/order lost: %v", creators)
	}
	collections := paper["collections"].([]any)
	if len(collections) != 1 || collections[0] != "FOLDER02" {
		t.Fatalf("deleted collection membership retained: %v", collections)
	}
	if items["NOTE0001"]["parentItem"] != "PAPER001" || items["ANNOT001"]["parentItem"] != "ATTACH01" || items["ANNOT001"]["annotationType"] != "underline" {
		t.Fatalf("child graph lost: %v", items)
	}
	if items["ATTACH01"]["filename"] != "paper.pdf" || items["ATTACH01"]["localFileAvailable"] != true {
		t.Fatalf("attachment unavailable: %v", items["ATTACH01"])
	}
	if paper["tags"].([]any)[0].(map[string]any)["tag"] != "reading" || paper["relations"] == nil {
		t.Fatal("tags or relations lost")
	}
	encoded, _ := json.Marshal(preview)
	if strings.Contains(string(encoded), "must-not-be-in-preview") {
		t.Fatal("unrelated account settings leaked")
	}
}

func TestZoteroDatabaseGroupUsesExternalGroupID(t *testing.T) {
	zoteroDatabaseFixture(t)
	preview := databasePreviewForTest(t, zoteroConfig{LibraryType: "groups", LibraryID: "42"})
	if preview.SourceID != "groups:42" || len(preview.Items) != 1 || !strings.Contains(string(preview.Items[0]), "GROUP001") {
		t.Fatalf("wrong group: %s %d", preview.SourceID, len(preview.Items))
	}
	client, err := newZoteroClient(zoteroConfig{Source: "database", LibraryType: "groups", LibraryID: "2"})
	if err == nil {
		_, err = client.preview(context.Background())
	}
	if err == nil {
		t.Fatal("internal library ID accepted as group ID")
	}
}

func TestZoteroDatabaseAttachmentConfinement(t *testing.T) {
	dir, db := zoteroDatabaseFixture(t)
	client, err := newZoteroClient(zoteroConfig{Source: "database"})
	if err != nil {
		t.Fatal(err)
	}
	file, err := client.attachment(context.Background(), "ATTACH01")
	if err != nil || file.Name != "paper.pdf" || !strings.HasPrefix(string(file.Data), "%PDF-") {
		t.Fatalf("PDF read failed: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "private.pdf")
	if err = os.WriteFile(outside, []byte("%PDF-private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, filepath.Join(dir, "storage", "ATTACH01", "link.pdf")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path string
		mode       int
	}{{"traversal", "storage:../../../private.pdf", 1}, {"symlink", "storage:link.pdf", 1}, {"absolute outside", outside, 2}, {"foreign Windows path", `C:\papers\paper.pdf`, 2}, {"linked URL", "https://example.com/paper.pdf", 3}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Exec("UPDATE itemAttachments SET path=?,linkMode=? WHERE itemID=2", tc.path, tc.mode); err != nil {
				t.Fatal(err)
			}
			if _, err := client.attachment(context.Background(), "ATTACH01"); err == nil {
				t.Fatal("read untrusted attachment path")
			}
		})
	}
	if _, err := client.attachment(context.Background(), "ORPHAN01"); err == nil {
		t.Fatal("read attachment of deleted paper")
	}
	if _, err := client.attachment(context.Background(), "../x.pdf"); err == nil {
		t.Fatal("read unregistered path")
	}
}

func TestZoteroDatabaseLinkedAttachmentRequiresTrustedRoot(t *testing.T) {
	_, db := zoteroDatabaseFixture(t)
	linked := t.TempDir()
	if err := os.WriteFile(filepath.Join(linked, "linked.pdf"), []byte("%PDF-linked"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_ZOTERO_LINKED_ATTACHMENT_DIR", linked)
	if _, err := db.Exec("UPDATE itemAttachments SET path='attachments:linked.pdf',linkMode=2 WHERE itemID=2"); err != nil {
		t.Fatal(err)
	}
	client, err := newZoteroClient(zoteroConfig{Source: "database"})
	if err != nil {
		t.Fatal(err)
	}
	file, err := client.attachment(context.Background(), "ATTACH01")
	if err != nil || file.Name != "linked.pdf" {
		t.Fatalf("trusted linked attachment unavailable: %v", err)
	}
}

func TestZoteroDatabaseRejectsStorageDirectorySymlinkEscape(t *testing.T) {
	dir, _ := zoteroDatabaseFixture(t)
	outside := t.TempDir()
	if err := os.Rename(filepath.Join(dir, "storage"), filepath.Join(outside, "storage")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "storage"), filepath.Join(dir, "storage")); err != nil {
		t.Fatal(err)
	}
	client, err := newZoteroClient(zoteroConfig{Source: "database"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.attachment(context.Background(), "ATTACH01"); err == nil {
		t.Fatal("storage symlink escaped the configured data directory")
	}
}

func TestZoteroDatabaseReadTransactionIsReadOnlyAndConsistent(t *testing.T) {
	_, writer := zoteroDatabaseFixture(t)
	location, err := discoverZoteroDatabase()
	if err != nil {
		t.Fatal(err)
	}
	tx, closeDB, err := openZoteroReadTransaction(context.Background(), location)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	var before, after string
	if err = tx.QueryRow("SELECT value FROM itemDataValues WHERE valueID=1").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Exec("UPDATE itemDataValues SET value='Concurrent edit' WHERE valueID=1"); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow("SELECT value FROM itemDataValues WHERE valueID=1").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != "Live WAL paper" || after != before {
		t.Fatalf("read transaction changed: %q -> %q", before, after)
	}
	if _, err = tx.Exec("UPDATE itemDataValues SET value='forbidden' WHERE valueID=1"); err == nil {
		t.Fatal("source connection permits writes")
	}
}

func TestZoteroDatabaseRejectsUnsupportedSchemaAndBrokenCollectionParents(t *testing.T) {
	for _, mutation := range []string{"DROP TABLE itemData", "UPDATE collections SET parentCollectionID=999 WHERE collectionID=2", "UPDATE collections SET parentCollectionID=2 WHERE collectionID=1", "INSERT INTO collectionItems VALUES (999,1,0)", "UPDATE itemNotes SET parentItemID=999 WHERE itemID=3", "UPDATE items SET itemTypeID=999 WHERE itemID=1"} {
		t.Run(mutation, func(t *testing.T) {
			_, db := zoteroDatabaseFixture(t)
			if _, err := db.Exec(mutation); err != nil {
				t.Fatal(err)
			}
			client, err := newZoteroClient(zoteroConfig{Source: "database"})
			if err == nil {
				_, err = client.preview(context.Background())
			}
			if err == nil {
				t.Fatal("silently imported malformed database")
			}
			if _, ok := err.(*zoteroError); !ok {
				t.Fatalf("unstructured error: %v", err)
			}
		})
	}
}

func TestZoteroDatabaseSupportsOlderOptionalColumns(t *testing.T) {
	_, db := zoteroDatabaseFixture(t)
	for _, query := range []string{"ALTER TABLE items DROP COLUMN version", "ALTER TABLE itemAttachments DROP COLUMN storageHash", "DROP TABLE itemAnnotations", "DELETE FROM items WHERE itemID=4", "DROP TABLE deletedCollections", "DELETE FROM collectionItems WHERE collectionID IN (3,4)", "DELETE FROM collections WHERE collectionID IN (3,4)"} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	preview := databasePreviewForTest(t, zoteroConfig{})
	if len(preview.Items) != 3 {
		t.Fatalf("older schema items=%d", len(preview.Items))
	}
}

func zoteroTestProfileRoot(home string) string {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Zotero")
	case "windows":
		return filepath.Join(home, "AppData", "Roaming", "Zotero", "Zotero")
	default:
		return filepath.Join(home, ".zotero", "zotero")
	}
}

func TestZoteroDatabaseDiscoveryHonorsDefaultProfileAndCustomDirectory(t *testing.T) {
	dir, _ := zoteroDatabaseFixture(t)
	t.Setenv("LAZYMIND_ZOTERO_DATA_DIR", "")
	home := t.TempDir()
	t.Setenv("LAZYMIND_HOST_HOME", home)
	t.Setenv("APPDATA", "")
	root := zoteroTestProfileRoot(home)
	profile := filepath.Join(root, "Profiles", "chosen")
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "profiles.ini"), []byte("[Profile0]\nName=stale\nIsRelative=1\nPath=Profiles/stale\n\n[Profile1]\nName=chosen\nIsRelative=1\nPath=Profiles/chosen\nDefault=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	pathJSON, _ := json.Marshal(dir)
	if err := os.WriteFile(filepath.Join(profile, "prefs.js"), []byte("user_pref(\"extensions.zotero.useDataDir\", true);\nuser_pref(\"extensions.zotero.dataDir\", "+string(pathJSON)+");\n"), 0600); err != nil {
		t.Fatal(err)
	}
	preview := databasePreviewForTest(t, zoteroConfig{})
	if len(preview.Items) != 4 {
		t.Fatal("custom profile not discovered")
	}
	if err := os.WriteFile(filepath.Join(profile, "prefs.js"), []byte("user_pref(\"extensions.zotero.useDataDir\", true);\nuser_pref(\"extensions.zotero.dataDir\", \"/missing/zotero-data\");\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, filepath.Join(home, "Zotero")); err != nil {
		t.Fatal(err)
	}
	if _, err := newZoteroClient(zoteroConfig{Source: "database"}); err == nil {
		t.Fatal("silently fell back to stale default after configured directory disappeared")
	}
}

func TestZoteroDatabaseMissingAndInvalidFilesDoNotCreateSource(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LAZYMIND_ZOTERO_DATA_DIR", dir)
	if _, err := newZoteroClient(zoteroConfig{Source: "database"}); err == nil {
		t.Fatal("missing database accepted")
	}
	path := filepath.Join(dir, "zotero.sqlite")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("created missing source database")
	}
	if err := os.WriteFile(path, []byte("not sqlite"), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := newZoteroClient(zoteroConfig{Source: "database"})
	if err == nil {
		_, err = client.preview(context.Background())
	}
	if err == nil {
		t.Fatal("invalid database accepted")
	}
}

func TestZoteroDatabaseInstallProfileOverridesLegacyDefault(t *testing.T) {
	dir, _ := zoteroDatabaseFixture(t)
	t.Setenv("LAZYMIND_ZOTERO_DATA_DIR", "")
	home := t.TempDir()
	t.Setenv("LAZYMIND_HOST_HOME", home)
	t.Setenv("APPDATA", "")
	root := zoteroTestProfileRoot(home)
	profile := filepath.Join(root, "Profiles", "installed")
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	ini := "[Profile0]\nIsRelative=1\nPath=Profiles/legacy\nDefault=1\n[Profile1]\nIsRelative=1\nPath=Profiles/installed\n[InstallCURRENT]\nDefault=Profiles/installed\nLocked=1\n"
	if err := os.WriteFile(filepath.Join(root, "profiles.ini"), []byte(ini), 0600); err != nil {
		t.Fatal(err)
	}
	quoted, _ := json.Marshal(dir)
	if err := os.WriteFile(filepath.Join(profile, "prefs.js"), []byte("user_pref(\"extensions.zotero.useDataDir\", true);\nuser_pref(\"extensions.zotero.dataDir\", "+string(quoted)+");\n"), 0600); err != nil {
		t.Fatal(err)
	}
	preview := databasePreviewForTest(t, zoteroConfig{})
	if len(preview.Items) != 4 {
		t.Fatal("installation default profile was not selected")
	}
}

func TestZoteroDatabaseRealReadOnlyIntegration(t *testing.T) {
	dir := os.Getenv("TEST_ZOTERO_DATA_DIR")
	if dir == "" {
		t.Skip("set TEST_ZOTERO_DATA_DIR to opt into read-only local verification")
	}
	t.Setenv("LAZYMIND_ZOTERO_DATA_DIR", dir)
	client, err := newZoteroClient(zoteroConfig{Source: "database"})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := client.preview(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if output := os.Getenv("TEST_ZOTERO_PREVIEW_OUTPUT"); output != "" {
		raw, err := json.Marshal(preview)
		if err != nil {
			t.Fatal(err)
		}
		file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := file.Write(raw)
		closeErr := file.Close()
		if writeErr != nil {
			t.Fatal(writeErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	items := previewDataForTest(t, preview.Items)
	counts := map[string]int{}
	pdfChecked := false
	for key, item := range items {
		kind, _ := item["itemType"].(string)
		counts[kind]++
		if !pdfChecked && kind == "attachment" && item["contentType"] == "application/pdf" && item["localFileAvailable"] == true {
			file, err := client.attachment(context.Background(), key)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(string(file.Data), "%PDF-") {
				t.Fatal("invalid PDF header")
			}
			parent, _ := item["parentItem"].(string)
			if parent == "" || items[parent] == nil {
				continue
			}
			verifyRealZoteroPaperPersistence(t, parent, items[parent], key, file)
			pdfChecked = true
		}
	}
	if !pdfChecked {
		t.Fatal("no local PDF was verified")
	}
	t.Logf("read-only source counts: %v; collections=%d; one PDF header and isolated selected-paper persistence/replay verified", counts, len(preview.Collections))
}

func verifyRealZoteroPaperPersistence(t *testing.T, key string, item map[string]any, attachmentKey string, file zoteroFile) {
	t.Helper()
	db := paperLibraryTestDB(t, orm.OpenTestDB(t).DB)
	if err := db.AutoMigrate(&orm.UploadedFile{}, &orm.UserUIPreferences{}, &orm.AsyncJob{}); err != nil {
		t.Fatal(err)
	}
	previous := http.DefaultTransport
	http.DefaultTransport = importErrorTransport(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/source-access/by-dataset:batch") {
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"items":[{"dataset_id":"papers","exists":false,"allowed":true}]}`))}, nil
		}
		return previous.RoundTrip(r)
	})
	t.Cleanup(func() { http.DefaultTransport = previous })
	t.Setenv("LAZYMIND_UPLOAD_ROOT", t.TempDir())
	title, _ := item["title"].(string)
	doi, _ := item["DOI"].(string)
	abstract, _ := item["abstractNote"].(string)
	authors := []string{}
	if creators, ok := item["creators"].([]any); ok {
		for _, raw := range creators {
			creator, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			kind, _ := creator["creatorType"].(string)
			if kind != "author" && kind != "bookAuthor" {
				continue
			}
			name, _ := creator["name"].(string)
			if name == "" {
				first, _ := creator["firstName"].(string)
				last, _ := creator["lastName"].(string)
				name = strings.TrimSpace(first + " " + last)
			}
			if name != "" {
				authors = append(authors, name)
			}
		}
	}
	graph := paperlibrary.EmptyData()
	graph.Papers = []paperlibrary.Paper{{ID: key, Metadata: paperlibrary.Metadata{Title: title, Authors: authors, Abstract: abstract, Identifiers: paperlibrary.Identifiers{DOI: doi}}, Source: "zotero", AttachmentIDs: []string{attachmentKey}, Tags: []string{}, NoteIDs: []string{}}}
	graph.Attachments = []paperlibrary.Attachment{{ID: attachmentKey, PaperID: key, Name: file.Name, Kind: "pdf", Source: "zotero", IsPrimary: true, AddedAt: time.Now().UTC()}}
	raw, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "selected.pdf")
	if err = os.WriteFile(path, file.Data, 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = paperlibrary.ImportSnapshot(t.Context(), db, "papers", "owner", raw); err != nil {
			t.Fatal("selected metadata persistence failed")
		}
		view, err := storeZoteroFile(t.Context(), db, "papers", key, attachmentKey, path, file.Name, "owner", "Owner")
		if err != nil {
			t.Fatal("selected PDF persistence failed:", err)
		}
		if len(view.Data.Papers) != 1 || view.Data.Papers[0].Metadata.Title != title || len(view.Data.Attachments) != 1 || view.Data.Attachments[0].ContentRef == "" {
			t.Fatal("selected metadata/PDF was lost or duplicated")
		}
	}
	for _, model := range []any{&orm.PaperLibraryEntry{}, &orm.Document{}, &orm.Task{}} {
		var count int64
		if err = db.Model(model).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("selected-paper replay count=%d for %T", count, model)
		}
	}
}

func TestZoteroDatabasePreviewNullableCreatorNames(t *testing.T) {
	for _, tc := range []struct {
		name                          string
		first, last                   any
		mode                          int
		wantFirst, wantLast, wantName string
	}{
		{name: "institution", last: "Example Institute", mode: 1, wantName: "Example Institute"},
		{name: "null first", last: "Lovelace", wantLast: "Lovelace"},
		{name: "null last", first: "Ada", wantFirst: "Ada"},
		{name: "both null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, db := zoteroDatabaseFixture(t)
			if _, err := db.Exec("UPDATE creators SET firstName=?,lastName=?,fieldMode=? WHERE creatorID=1", tc.first, tc.last, tc.mode); err != nil {
				t.Fatal(err)
			}
			preview := databasePreviewForTest(t, zoteroConfig{})
			items := previewDataForTest(t, preview.Items)
			if len(items) != 4 {
				t.Fatalf("lost unrelated items: %d", len(items))
			}
			creators := items["PAPER001"]["creators"].([]any)
			if len(creators) != 2 {
				t.Fatalf("lost creators: %v", creators)
			}
			got := creators[0].(map[string]any)
			if tc.mode == 1 {
				if got["name"] != tc.wantName {
					t.Fatalf("institution: %v", got)
				}
			} else if got["firstName"] != tc.wantFirst || got["lastName"] != tc.wantLast {
				t.Fatalf("person: %v", got)
			}
			var first, last sql.NullString
			if err := db.QueryRow("SELECT firstName,lastName FROM creators WHERE creatorID=1").Scan(&first, &last); err != nil {
				t.Fatal(err)
			}
			if first.Valid != (tc.first != nil) || last.Valid != (tc.last != nil) {
				t.Fatal("preview modified source NULL values")
			}
		})
	}
}
