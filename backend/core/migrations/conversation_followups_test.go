package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestConversationFollowUpsMigrationRoundTrip(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := openDatabase(t, dialect)
			up := migrationPath(t, "dev_mode/v0_3/*_conversation_followups.up.sql")
			applySQL(t, db, up, dialect)
			_, err := db.Exec(`INSERT INTO conversation_followups (
id,conversation_id,content,text,mode,status,sort_order,revision,idempotency_key,
create_user_id,create_user_name,created_at,updated_at
) VALUES ('f1','c1','{}','guide','queued','pending',1024,1,'request-1','u1','u1',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO conversation_followups (
id,conversation_id,content,text,mode,status,sort_order,revision,idempotency_key,
create_user_id,create_user_name,created_at,updated_at
) VALUES ('f2','c1','{}','duplicate','queued','pending',2048,1,'request-1','u1','u1',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err == nil {
				t.Fatal("idempotency key was not unique within a user conversation")
			}
			applySQL(t, db, strings.Replace(up, ".up.sql", ".down.sql", 1), dialect)
			if _, err := db.Query("SELECT id FROM conversation_followups"); err == nil {
				t.Fatal("conversation_followups survived rollback")
			}
		})
	}
}

func TestConversationFollowUpsAggregateIncludesFinalSchema(t *testing.T) {
	for _, direction := range []string{"up", "down"} {
		dev := migrationPath(t, "dev_mode/v0_3/*_conversation_followups."+direction+".sql")
		aggregate := migrationPath(t, "version_mode/v0_3/*."+direction+".sql")
		aggregateSQL, err := os.ReadFile(aggregate)
		if err != nil {
			t.Fatal(err)
		}
		devSQL, err := os.ReadFile(dev)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(aggregateSQL), string(devSQL)) {
			t.Fatalf("aggregate missing final conversation follow-up %s schema", direction)
		}
	}
}
