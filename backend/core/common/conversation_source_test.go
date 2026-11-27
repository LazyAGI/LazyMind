package common_test

import (
	"slices"
	"testing"

	"lazymind/core/common"
	"lazymind/core/common/orm"
)

func TestConversationSourceIDsScopesBindingsAndVisibility(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.ExternalAgentBinding{}, &orm.ExternalAgentSession{}).DB
	for _, row := range []struct {
		id, owner, provider string
		managed, active     bool
	}{
		{"local", "alice", "", false, false},
		{"outbound", "alice", "deepseek-harness", true, true},
		{"imported", "alice", "deepseek-harness", false, true},
		{"pending", "alice", "deepseek-harness", false, false},
		{"codex", "alice", "codex", true, false},
		{"cursor", "alice", "cursor", false, true},
		{"workbuddy", "alice", "workbuddy", false, true},
		{"other", "bob", "deepseek-harness", false, true},
		{"other-local", "bob", "", false, false},
	} {
		if err := db.Create(&orm.Conversation{ID: row.id, BaseModel: orm.BaseModel{CreateUserID: row.owner}}).Error; err != nil {
			t.Fatal(err)
		}
		if row.provider == "" {
			continue
		}
		if err := db.Create(&orm.ExternalAgentBinding{
			ID: row.id, ConversationID: row.id, CreatedByUserID: row.owner,
			Provider: row.provider, HostID: "host", ProviderThreadID: row.id, ManagedByLazyMind: row.managed,
		}).Error; err != nil {
			t.Fatal(err)
		}
		if row.active {
			if err := db.Create(&orm.ExternalAgentSession{
				ID: row.id, OwnerUserID: row.owner, Provider: row.provider, HostID: "host", ProviderThreadID: row.id, Active: true,
			}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	// Bob's catalog entry shares the native identifiers of Alice's pending
	// import. Only the binding owner's catalog can make the import visible.
	if err := db.Create(&orm.ExternalAgentSession{
		ID: "bob-pending", OwnerUserID: "bob", Provider: "deepseek-harness",
		HostID: "host", ProviderThreadID: "pending", Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		owner, filter string
		want          []string
	}{
		{"alice", "", []string{"codex", "cursor", "imported", "local", "outbound", "workbuddy"}},
		{"alice", "lazymind", []string{"local", "outbound"}},
		{"alice", "deepseek-harness", []string{"imported"}},
		{"alice", "lazymind,deepseek-harness", []string{"imported", "local", "outbound"}},
		{"alice", "codex,cursor,workbuddy", []string{"codex", "cursor", "workbuddy"}},
		{"bob", "", []string{"other", "other-local"}},
		{"bob", "deepseek-harness", []string{"other"}},
		{"charlie", "", nil},
	} {
		t.Run(tc.owner+"/"+tc.filter, func(t *testing.T) {
			query, err := common.ConversationSourceIDs(db, tc.owner, tc.filter)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			if err := query.Order("conversations.id").Scan(&got).Error; err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
