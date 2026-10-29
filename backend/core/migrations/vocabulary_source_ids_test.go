package migrations_test

import (
	"context"
	"database/sql"
	"fmt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/vocabulary"
	"strings"
	"testing"
)

func TestVocabularySourceIDsPreserveDocumentIdentity(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := openDatabase(t, dialect)
			applySQL(t, db, migrationPath(t, "dev_mode/v0_3/*_create_vocabulary_anki_tables.up.sql"), dialect)
			insert := func(id, dataset, document, segment string) error {
				_, err := db.Exec(fmt.Sprintf(`INSERT INTO vocabulary_source_refs(id,owner_id,word_id,dataset_id,document_id,segment_id,selected_text,context_sentence) VALUES ('%s','owner','word','%s','%s','%s','retrieve','retrieve evidence')`, id, dataset, document, segment))
				return err
			}
			if err := insert("old", "dataset", "document", "segment"); err != nil {
				t.Fatal(err)
			}
			up := migrationPath(t, "dev_mode/v0_3/*_widen_vocabulary_source_ids.up.sql")
			applySQL(t, db, up, dialect)
			dataset, document, segment := strings.Repeat("d", 255), strings.Repeat("x", 128), strings.Repeat("s", 255)
			if err := insert("long", dataset, document, segment); err != nil {
				t.Fatalf("valid source IDs rejected: %v", err)
			}
			var gotDataset, gotDoc, gotSegment string
			if err := db.QueryRow("SELECT dataset_id,document_id,segment_id FROM vocabulary_source_refs WHERE id='long'").Scan(&gotDataset, &gotDoc, &gotSegment); err != nil || gotDataset != dataset || gotDoc != document || gotSegment != segment {
				t.Fatalf("source identity was lost: %v", err)
			}
			if err := insert("duplicate", dataset, document, segment); err == nil {
				t.Fatal("source uniqueness lost")
			}
			if dialect == "postgres" {
				// Rollback must reject overlong identities instead of silently truncating them.
				if _, err := db.Exec("ALTER TABLE vocabulary_source_refs ALTER COLUMN document_id TYPE VARCHAR(64)"); err == nil {
					t.Fatal("unsafe rollback silently truncated identity")
				}
			}
			if _, err := db.Exec("DELETE FROM vocabulary_source_refs WHERE id='long'"); err != nil {
				t.Fatal(err)
			}
			applySQL(t, db, strings.Replace(up, ".up.sql", ".down.sql", 1), dialect)
			var count int
			if err := db.QueryRow("SELECT count(*) FROM vocabulary_source_refs WHERE id='old' AND document_id='document'").Scan(&count); err != nil || count != 1 {
				t.Fatal("rollback lost history", err)
			}
			if dialect == "postgres" {
				for _, column := range []string{"dataset_id", "document_id", "segment_id"} {
					var length sql.NullInt64
					if err := db.QueryRow("SELECT character_maximum_length FROM information_schema.columns WHERE table_name='vocabulary_source_refs' AND column_name=$1", column).Scan(&length); err != nil || length.Int64 != 64 {
						t.Fatalf("rollback %s=%v: %v", column, length, err)
					}
				}
			}
		})
	}
}

// Exercise AddWord against the actual PostgreSQL migration schema, not AutoMigrate.
func TestVocabularySourceIDsPostgresLocalSaveAndOutbox(t *testing.T) {
	db := openDatabase(t, "postgres")
	for _, version := range []string{"v0_1", "v0_2", "v0_3"} {
		applySQL(t, db, migrationPath(t, "version_mode/"+version+"/*.up.sql"), "postgres")
	}
	store, err := gorm.Open(postgres.New(postgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_VOCABULARY_ENABLED", "true")
	t.Setenv("LAZYMIND_RUNTIME_MODE", "local")
	c := vocabulary.CloudConnection{ID: "connection", OwnerID: "owner", Provider: "eudic", TokenCipher: "test-cipher-not-used-for-local-save", Enabled: true, TargetID: "book", AccountKey: "test-account"}
	if err := store.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	svc := vocabulary.New(store)
	request := vocabulary.AddWordRequest{Provider: "local", Term: "retrieve", Language: "en", Sentence: "retrieve evidence", DatasetID: strings.Repeat("d", 255), DocumentID: "zotero_" + strings.Repeat("a", 64), SegmentID: strings.Repeat("s", 255)}
	first, err := svc.AddWord(context.Background(), "owner", request)
	if err != nil || !first.CloudQueued {
		t.Fatalf("save with 71-character document ID: %+v %v", first, err)
	}
	var firstExamples int64
	if err := store.Model(&vocabulary.Example{}).Count(&firstExamples).Error; err != nil || firstExamples != 1 {
		t.Fatalf("first save examples=%d: %v", firstExamples, err)
	}
	second, err := svc.AddWord(context.Background(), "owner", request)
	if err != nil || second.Word.ID != first.Word.ID {
		t.Fatalf("repeat save: %+v %v", second, err)
	}
	for _, model := range []any{&vocabulary.Word{}, &vocabulary.SourceRef{}, &vocabulary.CloudOperation{}, &orm.AsyncJob{}} {
		var n int64
		if err := store.Model(model).Count(&n).Error; err != nil || n != 1 {
			t.Fatalf("%T count=%d err=%v", model, n, err)
		}
	}
	var ref vocabulary.SourceRef
	if err := store.First(&ref).Error; err != nil || ref.DocumentID != request.DocumentID || ref.DatasetID != request.DatasetID || ref.SegmentID != request.SegmentID {
		t.Fatalf("source identity changed: %v", err)
	}
}
