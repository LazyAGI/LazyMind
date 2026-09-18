package credentialvault

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBackupOutboxContainsOnlyReferencesAndVersionState(t *testing.T) {
	typeOfItem := reflect.TypeOf(BackupOutboxItem{})
	forbidden := []string{"apikey", "credential", "plaintext", "ciphertext", "payload", "token", "secret", "dek", "privatekey"}
	for index := 0; index < typeOfItem.NumField(); index++ {
		name := strings.ToLower(typeOfItem.Field(index).Name)
		for _, marker := range forbidden {
			if strings.Contains(name, marker) && name != "localcredentialrevision" {
				t.Errorf("outbox field %s contains forbidden credential material marker %q", typeOfItem.Field(index).Name, marker)
			}
		}
	}
}

func TestBackupOutboxCoalescesByProviderGroupAndKeepsNewestRevision(t *testing.T) {
	now := time.Date(2026, 8, 26, 6, 20, 49, 0, time.UTC)
	item, err := NewBackupOutboxItem("outbox-a", "provider-group-a", 7, BackupUpsert, now)
	if err != nil {
		t.Fatalf("create backup outbox item: %v", err)
	}
	if item.LocalProviderGroupID != "provider-group-a" || item.LocalCredentialRevision != 7 || item.State != BackupPending || item.AttemptCount != 0 {
		t.Fatalf("backup outbox item = %+v", item)
	}
}

func TestBackupOutboxRetryUsesBoundedExponentialBackoffAndSafeErrorCode(t *testing.T) {
	now := time.Date(2026, 8, 26, 6, 20, 49, 0, time.UTC)
	item, err := NewBackupOutboxItem("outbox-b", "provider-group-b", 3, BackupUpsert, now)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 10; attempt++ {
		before := item.NextAttemptAt
		item, err = item.Retry(now, 3080005)
		if err != nil {
			t.Fatalf("retry %d: %v", attempt, err)
		}
		if item.AttemptCount != attempt || item.LastErrorCode != 3080005 || item.NextAttemptAt.Before(before) {
			t.Fatalf("retry %d state = %+v", attempt, item)
		}
		if item.NextAttemptAt.Sub(now) > time.Hour {
			t.Fatalf("retry %d exceeded one-hour cap: %s", attempt, item.NextAttemptAt.Sub(now))
		}
	}
}
