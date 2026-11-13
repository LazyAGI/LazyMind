package vocabulary

import (
	"context"
	"testing"
)

func TestInitialProviderUsesLocalWhenAvailable(t *testing.T) {
	t.Setenv("LAZYMIND_VOCABULARY_ANKI_SYNC_ENABLED", "true")
	s := testService(t)
	setting, err := s.Setting(context.Background(), "first-visit")
	if err != nil {
		t.Fatal(err)
	}
	if setting.SelectedProvider != "local" {
		t.Fatalf("first visit selected %q without Anki setup, want local", setting.SelectedProvider)
	}
	if setting.AnkiSyncEnabled {
		t.Fatal("first visit must not opt into Anki sync")
	}
}

func TestInitialProviderRemainsLocalWithLegacyDisableFlag(t *testing.T) {
	s := testService(t)
	t.Setenv("LAZYMIND_VOCABULARY_ENABLED", "false")
	setting, err := s.Setting(context.Background(), "first-visit")
	if err != nil || setting.SelectedProvider != "local" || setting.AnkiSyncEnabled {
		t.Fatalf("legacy flag changed local defaults: %+v, error %v", setting, err)
	}
}

func TestSavedAnkiChoiceBecomesSyncOptInWithoutChangingLocalProvider(t *testing.T) {
	t.Setenv("LAZYMIND_VOCABULARY_ANKI_SYNC_ENABLED", "true")
	s := testService(t)
	ctx := context.Background()
	if err := s.db.Create(&ProviderSetting{
		OwnerID: "configured-user", LegacySelectedProvider: ProviderAnki,
		AnkiEndpoint: defaultAnkiURL, AnkiDeckName: "Saved deck", AnkiModelVersion: ankiModelVersion,
	}).Error; err != nil {
		t.Fatal(err)
	}
	setting, err := s.Setting(ctx, "configured-user")
	if err != nil || setting.SelectedProvider != "local" || !setting.AnkiSyncEnabled || !setting.AnkiSyncAvailable || setting.AnkiDeckName != "Saved deck" {
		t.Fatalf("saved choice changed: %+v, error %v", setting, err)
	}
	var stored ProviderSetting
	if err := s.db.Where("owner_id = ?", "configured-user").First(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored.LegacySelectedProvider != ProviderAnki || stored.AnkiDeckName != "Saved deck" {
		t.Fatalf("reading settings rewrote the saved Anki choice: %+v", stored)
	}
}
