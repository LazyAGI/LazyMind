package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func TestScriptsApprovedForPublishRequiresMatchingAuditHash(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:script_publish?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.PluginGenerationAnalysis{}); err != nil {
		t.Fatal(err)
	}
	source := "def run(value):\n    return value\n"
	sum := sha256.Sum256([]byte(source))
	hash := hex.EncodeToString(sum[:])
	analysis := orm.PluginGenerationAnalysis{ID: "a1", DraftID: "d1", ScriptReportJSON: `{"scripts/run.py":{"classification":"importable_tool","sha256":"` + hash + `"}}`}
	if err := db.Create(&analysis).Error; err != nil {
		t.Fatal(err)
	}
	draft := orm.PluginDraft{ID: "d1", SourceAnalysisID: "a1", ScriptsContent: `{"scripts/run.py":"def run(value):\n    return value\n"}`}
	if !scriptsApprovedForPublish(db, draft) {
		t.Fatal("matching audited script should be publishable")
	}
	draft.ScriptsContent = `{"scripts/run.py":"def run(value):\n    return value + 1\n"}`
	if scriptsApprovedForPublish(db, draft) {
		t.Fatal("modified script must invalidate audit")
	}
}

func TestFrameworkToolsAvailableForPublishRequiresAuditedAvailability(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:framework_tool_publish?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.PluginGenerationAnalysis{}); err != nil {
		t.Fatal(err)
	}
	draft := orm.PluginDraft{ID: "d2", SourceAnalysisID: "a2"}
	analysis := orm.PluginGenerationAnalysis{
		ID:                    "a2",
		DraftID:               draft.ID,
		ToolMappingReportJSON: `{"search":{"action":"replace","framework_tool":"web_search","available":true}}`,
	}
	if err := db.Create(&analysis).Error; err != nil {
		t.Fatal(err)
	}
	if !frameworkToolsAvailableForPublish(db, draft) {
		t.Fatal("an audited available framework replacement should be publishable")
	}
	if err := db.Model(&analysis).Update("tool_mapping_report_json", `{"search":{"action":"replace","framework_tool":"web_search","available":false}}`).Error; err != nil {
		t.Fatal(err)
	}
	if frameworkToolsAvailableForPublish(db, draft) {
		t.Fatal("an unavailable framework replacement must block publishing")
	}
	if err := db.Model(&analysis).Update("tool_mapping_report_json", `{"search":{"action":"replace","framework_tool":"web_search"}}`).Error; err != nil {
		t.Fatal(err)
	}
	if frameworkToolsAvailableForPublish(db, draft) {
		t.Fatal("a replacement without an availability audit must fail closed")
	}
}
