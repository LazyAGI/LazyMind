package chat

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

func newArtifactTestDB(t *testing.T) *orm.DB {
	t.Helper()
	db, err := orm.Connect(orm.DriverSQLite, filepath.Join(t.TempDir(), "artifacts.db"))
	if err != nil {
		t.Fatalf("connect artifact test db: %v", err)
	}
	if err := db.AutoMigrate(&orm.ConversationArtifact{}); err != nil {
		t.Fatalf("migrate artifact test db: %v", err)
	}
	return db
}

func TestPersistConversationArtifactBindsAuthoritativeTurn(t *testing.T) {
	db := newArtifactTestDB(t)
	event := &ArtifactCreatedEvent{
		ArtifactID:  "09f9027d-9338-4e38-9674-238acf7ae173",
		Filename:    "result.txt",
		ContentType: "text",
		Value:       json.RawMessage(`{"text":"hello"}`),
	}

	dto, err := persistConversationArtifact(
		context.Background(), db.DB, "conversation-1", "history-1", "user-1", event,
	)
	if err != nil {
		t.Fatalf("persist artifact: %v", err)
	}
	if dto.ConversationID != "conversation-1" || dto.HistoryID != "history-1" {
		t.Fatalf("artifact was not bound to the current turn: %#v", dto)
	}

	var stored orm.ConversationArtifact
	if err := db.First(&stored, "id = ?", event.ArtifactID).Error; err != nil {
		t.Fatalf("load stored artifact: %v", err)
	}
	if stored.CreateUserID != "user-1" || stored.Filename != "result.txt" {
		t.Fatalf("unexpected stored artifact: %#v", stored)
	}
}

func TestPersistConversationArtifactRejectsInvalidOrDuplicateInput(t *testing.T) {
	db := newArtifactTestDB(t)
	valid := &ArtifactCreatedEvent{
		ArtifactID:  "bd27e81e-3767-4fc2-a6b6-9270633ce646",
		Filename:    "result.json",
		ContentType: "json",
		Value:       json.RawMessage(`{"data":{"ok":true}}`),
	}
	if _, err := persistConversationArtifact(
		context.Background(), db.DB, "conversation-1", "history-1", "user-1", valid,
	); err != nil {
		t.Fatalf("persist valid artifact: %v", err)
	}
	if _, err := persistConversationArtifact(
		context.Background(), db.DB, "conversation-1", "history-1", "user-1", valid,
	); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected duplicate id error, got %v", err)
	}

	invalid := *valid
	invalid.ArtifactID = "84e68a57-766a-4b3a-bd2f-f8f1b4d52354"
	invalid.Filename = "../escape.txt"
	if _, err := persistConversationArtifact(
		context.Background(), db.DB, "conversation-1", "history-1", "user-1", &invalid,
	); err == nil {
		t.Fatal("expected unsafe filename to be rejected")
	}
}

func TestPersistConversationArtifactUsesCharacterLimitsForUnicode(t *testing.T) {
	db := newArtifactTestDB(t)
	caption := strings.Repeat("说明", 1000)
	event := &ArtifactCreatedEvent{
		ArtifactID:  "67cd1254-bb2a-4d14-ac70-4e6913c2b245",
		Filename:    strings.Repeat("文", 100) + ".txt",
		ContentType: "text",
		Value:       json.RawMessage(`{"text":"内容"}`),
		Caption:     &caption,
	}

	if _, err := persistConversationArtifact(
		context.Background(), db.DB, "conversation-1", "history-1", "user-1", event,
	); err != nil {
		t.Fatalf("valid Unicode metadata should be accepted: %v", err)
	}
}
