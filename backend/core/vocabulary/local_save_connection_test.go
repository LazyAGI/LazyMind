package vocabulary

import (
	"context"
	"testing"
	"time"
)

func TestAddDictionaryWordWithSingleSQLiteConnection(t *testing.T) {
	s := testService(t)
	sqlDB, err := s.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	selectLocal(t, s)
	entry := DictionaryEntry{ID: "selected-entry", Language: "en", NormalizedTerm: "reasoning", Term: "reasoning", SourceName: "test dictionary", SourceVersion: "v1", LicenseID: "test-license", SourceLocator: "entry:reasoning"}
	if err := s.db.Create(&entry).Error; err != nil {
		t.Fatal(err)
	}
	sense := DictionarySense{ID: "sense", EntryID: entry.ID, Translation: "推理", PartOfSpeech: "noun"}
	if err := s.db.Create(&sense).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result, err := s.AddWord(ctx, "u", AddWordRequest{Provider: "local", Term: "Reasoning", Language: "en", DictionaryEntryID: entry.ID, Sentence: "Reasoning segmentation results among LISA and previous related works.", DatasetID: "papers", DocumentID: "lisa"})
	if err != nil {
		t.Fatalf("single-connection save blocked or failed: %v", err)
	}
	if result.Word.SourceName != entry.SourceName || result.Word.SourceVersion != entry.SourceVersion || result.Word.LicenseID != entry.LicenseID || result.Word.SourceLocator != entry.SourceLocator || result.Word.Meaning != "推理" {
		t.Fatalf("selected dictionary provenance lost: %+v", result.Word)
	}
	if result.Example == nil {
		t.Fatal("source example missing")
	}
	var refs int64
	if err := s.db.WithContext(ctx).Model(&SourceRef{}).Where("word_id=? AND document_id=?", result.Word.ID, "lisa").Count(&refs).Error; err != nil || refs != 1 {
		t.Fatalf("source ref missing or connection still blocked: refs=%d err=%v", refs, err)
	}
	if sqlDB.Stats().WaitCount != 0 {
		t.Fatalf("local save attempted nested connection acquisition: %+v", sqlDB.Stats())
	}
}
