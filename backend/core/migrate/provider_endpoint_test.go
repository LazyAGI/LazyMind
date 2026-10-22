package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const providerEndpointMigration = "20261022060632_share_model_provider_endpoints"

func TestProviderEndpointMigrationRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, filepath.Join(t.TempDir(), "endpoints.db"))
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL DSN required")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "provider_endpoint")
			}
			catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			for i, mode := range catalog.Modes {
				if i < len(catalog.Modes)-1 {
					execMigrationFileForDriver(t, db, mode.Aggregate.UpPath, driver)
					continue
				}
				for _, migration := range mode.Dev {
					if !strings.Contains(migration.UpPath, providerEndpointMigration) {
						execMigrationFileForDriver(t, db, migration.UpPath, driver)
					}
				}
			}
			_, err = db.Exec(`
INSERT INTO user_model_providers (id,default_model_provider_id,name,description,base_url,category,create_user_id,create_user_name,created_at,updated_at) VALUES
('p','fixture','Fixture','','https://catalog.example','model','user','','2026-01-01','2026-01-01'),
('empty','fixture-empty','Empty','','https://empty.example','model','user','','2026-01-01','2026-01-01'),
('aux','fixture-aux','Aux','','https://aux.example','ocr','user','','2026-01-01','2026-01-01');
INSERT INTO user_model_provider_groups (id,user_model_provider_id,name,base_url,api_key,is_verified,credential_revision,create_user_id,create_user_name,created_at,updated_at,deleted_at) VALUES
('deleted','p','deleted','https://deleted.example','deleted-secret',TRUE,1,'user','','2025-01-01','2025-01-01','2025-02-01'),
('wrong-tenant','p','wrong tenant','https://wrong.example','wrong-secret',TRUE,1,'other','','2025-01-01','2025-01-01',NULL),
('a','p','z-name','https://first.example','first-secret',TRUE,3,'user','','2026-01-01','2026-01-01',NULL),
('b','p','a-name','https://second.example','second-secret',TRUE,7,'user','','2026-01-01','2026-01-01',NULL),
('c','p','same endpoint','https://first.example','third-secret',TRUE,5,'user','','2026-01-02','2026-01-02',NULL),
('aux-group','aux','aux','https://instance-aux.example','aux-secret',TRUE,1,'user','','2026-01-01','2026-01-01',NULL);
INSERT INTO user_model_provider_group_models (id,user_model_provider_id,user_model_provider_group_id,provider_name,name,model_type,max_input_tokens,is_default,create_user_id,create_user_name,created_at,updated_at)
VALUES ('selected-model','p','b','Fixture','model','llm',128000,FALSE,'user','','2026-01-01','2026-01-01');
INSERT INTO user_selected_models (user_id,user_name,model_type,user_model_provider_group_model_id,created_at,updated_at)
VALUES ('user','','llm','selected-model','2026-01-01','2026-01-01');
INSERT INTO cloud_credential_vault_accounts (id,cloud_issuer,cloud_account_id,vault_id,vault_member_id,client_member_key,signing_key_version,key_shard_id,active_key_id,backup_enabled,created_at,updated_at)
VALUES ('account','https://cloud.example','cloud-user','vault','member','client',1,1,'key',TRUE,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
INSERT INTO cloud_credential_bindings (id,cloud_issuer,cloud_account_id,vault_id,cloud_record_id,local_provider_group_id,last_cloud_revision,last_local_credential_revision,backup_state,created_at,updated_at)
VALUES ('binding-b','https://cloud.example','cloud-user','vault','record-b','b',2,7,'succeeded',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
`)
			if err != nil {
				t.Fatal(err)
			}
			migrationBase := filepath.Join("..", "migrations", "dev_mode", "v0_3", providerEndpointMigration)
			execMigrationFileForDriver(t, db, migrationBase+".up.sql", driver)
			assertURL := func(table, id, want string) {
				t.Helper()
				var got string
				if err := db.QueryRow("SELECT base_url FROM " + table + " WHERE id='" + id + "'").Scan(&got); err != nil || got != want {
					t.Fatalf("%s/%s endpoint=%q want=%q: %v", table, id, got, want, err)
				}
			}
			assertURL("user_model_providers", "p", "https://first.example")
			assertURL("user_model_providers", "empty", "https://empty.example")
			assertURL("user_model_providers", "aux", "https://aux.example")
			for _, id := range []string{"a", "b", "c"} {
				assertURL("user_model_provider_groups", id, "https://first.example")
			}
			assertURL("user_model_provider_groups", "deleted", "https://deleted.example")
			assertURL("user_model_provider_groups", "wrong-tenant", "https://wrong.example")
			assertURL("user_model_provider_groups", "aux-group", "https://instance-aux.example")
			var verified bool
			var key string
			var revision int64
			if err := db.QueryRow("SELECT is_verified,api_key,credential_revision FROM user_model_provider_groups WHERE id='b'").Scan(&verified, &key, &revision); err != nil || verified || key != "second-secret" || revision != 7 {
				t.Fatalf("changed connection state not preserved: verified=%v revision=%d err=%v", verified, revision, err)
			}
			if err := db.QueryRow("SELECT is_verified FROM user_model_provider_groups WHERE id='c'").Scan(&verified); err != nil || !verified {
				t.Fatal("unchanged endpoint lost verification", err)
			}
			var selected string
			if err := db.QueryRow("SELECT user_model_provider_group_model_id FROM user_selected_models WHERE user_id='user'").Scan(&selected); err != nil || selected != "selected-model" {
				t.Fatal("selected model lost", err)
			}
			var queued int64
			if err := db.QueryRow("SELECT local_credential_revision FROM credential_backup_outbox WHERE local_provider_group_id='b' AND backup_state='pending'").Scan(&queued); err != nil || queued != 7 {
				t.Fatal("cloud payload refresh not queued", err)
			}
			// A later key revision must not regain stale snapshot verification on rollback.
			if _, err := db.Exec("UPDATE user_model_provider_groups SET credential_revision=6, is_verified=FALSE WHERE id='c'"); err != nil {
				t.Fatal(err)
			}
			execMigrationFileForDriver(t, db, migrationBase+".down.sql", driver)
			assertURL("user_model_providers", "p", "https://catalog.example")
			assertURL("user_model_provider_groups", "b", "https://second.example")
			if err := db.QueryRow("SELECT is_verified FROM user_model_provider_groups WHERE id='b'").Scan(&verified); err != nil || !verified {
				t.Fatal("down lost original verification", err)
			}
			if err := db.QueryRow("SELECT is_verified FROM user_model_provider_groups WHERE id='c'").Scan(&verified); err != nil || verified {
				t.Fatal("down restored stale verification for edited credential", err)
			}
		})
	}
}

func TestProviderEndpointSQLiteAggregateMatchesDev(t *testing.T) {
	catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	releaseDB := openRawSQLite(t, filepath.Join(t.TempDir(), "release.db"))
	devDB := openRawSQLite(t, filepath.Join(t.TempDir(), "dev.db"))
	for i, mode := range catalog.Modes {
		if i < len(catalog.Modes)-1 {
			execMigrationFileForDriver(t, releaseDB, mode.Aggregate.UpPath, "sqlite")
			execMigrationFileForDriver(t, devDB, mode.Aggregate.UpPath, "sqlite")
			continue
		}
		for _, db := range []*sql.DB{releaseDB, devDB} {
			if _, err := db.Exec(`INSERT INTO user_model_providers (id,default_model_provider_id,name,description,base_url,category,create_user_id,create_user_name,created_at,updated_at) VALUES ('p','fixture','Fixture','','https://catalog.example','model','user','','2026-01-01','2026-01-01');
INSERT INTO user_model_provider_groups (id,user_model_provider_id,name,base_url,api_key,is_verified,create_user_id,create_user_name,created_at,updated_at) VALUES ('a','p','z-first','https://first.example','first-secret',TRUE,'user','','2026-01-01','2026-01-01'),('b','p','a-second','https://second.example','second-secret',TRUE,'user','','2026-01-02','2026-01-02');`); err != nil {
				t.Fatal(err)
			}
		}
		execMigrationFileForDriver(t, releaseDB, mode.Aggregate.UpPath, "sqlite")
		for _, migration := range mode.Dev {
			execMigrationFileForDriver(t, devDB, migration.UpPath, "sqlite")
		}
	}
	// Compare the schema introduced here. Whole-repository SQLite DDL text differs
	// between ALTER/rebuild and aggregate paths for unrelated existing tables.
	schema := func(db *sql.DB) string {
		var ddl string
		if err := db.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='model_provider_endpoint_backups'").Scan(&ddl); err != nil {
			t.Fatal(err)
		}
		return ddl
	}
	if schema(releaseDB) != schema(devDB) {
		t.Fatal("endpoint rollback schema differs between aggregate/dev")
	}
	for _, db := range []*sql.DB{releaseDB, devDB} {
		var supplier, endpoint, key string
		var verified bool
		if err := db.QueryRow("SELECT base_url FROM user_model_providers WHERE id='p'").Scan(&supplier); err != nil || supplier != "https://first.example" {
			t.Fatal("aggregate/dev supplier promotion differs", err)
		}
		if err := db.QueryRow("SELECT base_url,api_key,is_verified FROM user_model_provider_groups WHERE id='b'").Scan(&endpoint, &key, &verified); err != nil || endpoint != supplier || key != "second-secret" || verified {
			t.Fatal("aggregate/dev connection promotion differs", err)
		}
	}
}
