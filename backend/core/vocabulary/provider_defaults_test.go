package vocabulary

import (
	"context"
	"testing"
)

func TestInitialProviderUsesLocalWhenAvailable(t *testing.T) {
	s := testService(t)
	setting, err := s.Setting(context.Background(), "first-visit")
	if err != nil {
		t.Fatal(err)
	}
	if setting.SelectedProvider != "local" {
		t.Fatalf("first visit selected %q without Anki setup, want local", setting.SelectedProvider)
	}
}

func TestInitialProviderDoesNotOfferUnavailableLocalStorage(t *testing.T) {
	s := testService(t)
	t.Setenv("LAZYMIND_VOCABULARY_ENABLED", "false")
	setting, err := s.Setting(context.Background(), "first-visit")
	if err != nil || setting.SelectedProvider != ProviderAnki {
		t.Fatalf("disabled local storage default = %q, error %v", setting.SelectedProvider, err)
	}
}

func TestSavedAnkiChoiceSurvivesInitialProviderDefault(t *testing.T) {
	s := testService(t)
	ctx := context.Background()
	_, err := s.SaveSetting(ctx, "configured-user", ProviderSetting{
		SelectedProvider: ProviderAnki, AnkiEndpoint: defaultAnkiURL, AnkiDeckName: "Saved deck",
	})
	if err != nil {
		t.Fatal(err)
	}
	setting, err := s.Setting(ctx, "configured-user")
	if err != nil || setting.SelectedProvider != ProviderAnki || setting.AnkiDeckName != "Saved deck" {
		t.Fatalf("saved choice changed: %+v, error %v", setting, err)
	}
}
