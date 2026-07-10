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
