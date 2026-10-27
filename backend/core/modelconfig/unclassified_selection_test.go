package modelconfig

import (
	"testing"

	"lazymind/core/common/orm"
)

func TestUnclassifiedSavedModelCannotBorrowItsSelectedRoleAsRuntimeType(t *testing.T) {
	db, calls := unverifiedSupplierFixture(t)
	if err := db.Model(&orm.UserModelProviderGroupModel{}).Where("id = ?", "chat").Update("model_type", "").Error; err != nil {
		t.Fatal(err)
	}
	// Old or imported selections must not turn an unclassified model into an
	// LLM merely because the selection points at the llm role.
	if err := db.Create(&orm.UserSelectedModel{UserID: "owner", ModelKey: "llm", UserModelProviderGroupModelID: "chat"}).Error; err != nil {
		t.Fatal(err)
	}
	config, err := LoadLLMConfig(t.Context(), db, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if config["llm"] != nil {
		t.Fatal("unclassified saved model was admitted as an LLM")
	}
	if err := db.Model(&orm.UserModelProviderGroupModel{}).Where("id = ?", "chat").Update("model_type", "llm").Error; err != nil {
		t.Fatal(err)
	}
	config, err = LoadLLMConfig(t.Context(), db, "owner")
	if err != nil || config["llm"] == nil {
		t.Fatalf("explicit classification did not restore the saved role: %#v %v", config, err)
	}
	if calls.Load() != 0 {
		t.Fatal("runtime type validation attempted an upstream request")
	}
}
