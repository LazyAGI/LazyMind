package migrate

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const customProviderMigration = "20261022072824_flatten_model_provider_connections"

func customProviderMigrationDB(t *testing.T, driver string) *sql.DB {
	t.Helper()
	var db *sql.DB
	if driver == "sqlite" {
		db = openRawSQLite(t, filepath.Join(t.TempDir(), "custom-provider.db"))
	} else {
		dsn := os.Getenv(migrationPostgresDSNEnv)
		if dsn == "" {
			t.Skip("PostgreSQL DSN required")
		}
		db = createTemporaryPostgresDatabase(t, dsn, "custom_provider")
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
			if migration.FileVersion < 20261022072824 {
				execMigrationFileForDriver(t, db, migration.UpPath, driver)
			}
		}
	}
	return db
}

func seedCustomProviderMigration(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
INSERT INTO default_model_providers(id,name,description,base_url,created_at,updated_at) VALUES
('catalog-openai','OpenAI','','https://official.example/v1/','2026-01-01','2026-01-01'),
('catalog-deepseek','DeepSeek','','https://deepseek.example/','2026-01-01','2026-01-01');
INSERT INTO user_model_providers(id,default_model_provider_id,name,description,base_url,category,active_key_group_id,active_key_id,active_key_initialized,active_key_revision,create_user_id,create_user_name,created_at,updated_at) VALUES
('p','catalog-openai','OpenAI','','https://packy.example/v1/','model','packy','selected-key-id',TRUE,5,'user','','2026-01-01','2026-01-01'),
('disabled','catalog-deepseek','DeepSeek','','https://latest.example/','model','','',TRUE,6,'user','','2026-01-01','2026-01-01'),
('keyless','catalog-openai','OpenAI','','http://local.example/v1/','model','','',FALSE,0,'keyless-user','','2026-01-01','2026-01-01');
INSERT INTO user_model_provider_groups(id,user_model_provider_id,name,base_url,api_key,api_key_ciphertext,credential_revision,is_verified,create_user_id,create_user_name,created_at,updated_at,deleted_at) VALUES
('packy','p','packyapi','https://packy.example/v1/','','cipher-packy',7,TRUE,'user','','2026-01-01','2026-01-01',NULL),
('aihub','p','AIhubmix','https://packy.example/v1/','','cipher-aihub',3,TRUE,'user','','2026-01-02','2026-01-02',NULL),
('standard','p','OpenAI','https://packy.example/v1/','','cipher-standard',2,TRUE,'user','','2026-01-03','2026-01-03',NULL),
('extra','p','OpenAI','https://packy.example/v1/','','cipher-extra',1,TRUE,'user','','2026-01-04','2026-01-04',NULL),
('disabled-group','disabled','ProxyDisabled','https://latest.example/','','cipher-disabled',8,FALSE,'user','','2026-01-01','2026-01-01',NULL),
('keyless-group','keyless','Local custom','http://local.example/v1/','','',0,TRUE,'keyless-user','','2026-01-01','2026-01-01',NULL),
('deleted','p','Deleted custom','https://deleted.example/','','cipher-deleted',1,TRUE,'user','','2025-01-01','2025-01-01','2025-02-01'),
('foreign','p','Foreign owner','https://foreign.example/','','cipher-foreign',1,TRUE,'other','','2025-01-01','2025-01-01',NULL);
INSERT INTO model_provider_endpoint_backups(row_kind,row_id,base_url,is_verified,credential_revision) VALUES
('provider','p','https://official.example/v1/',FALSE,0),
('provider','disabled','https://deepseek.example/',FALSE,0),
('connection','packy','https://packy.example/v1/',TRUE,7),
('connection','aihub','https://aihub.example/v1/',TRUE,3),
('connection','standard','https://official.example/v1/',TRUE,2),
('connection','extra','https://official.example/v1/',TRUE,1),
('connection','disabled-group','https://old.example/',TRUE,8);
INSERT INTO user_model_provider_group_models(id,user_model_provider_id,user_model_provider_group_id,provider_name,name,model_type,is_default,create_user_id,create_user_name,created_at,updated_at) VALUES
('model-aihub','p','aihub','OpenAI','chat-model','llm',FALSE,'user','','2026-01-01','2026-01-01');
INSERT INTO user_selected_models(user_id,user_name,model_type,user_model_provider_group_model_id,created_at,updated_at) VALUES
('user','','llm','model-aihub','2026-01-01','2026-01-01');
INSERT INTO cloud_credential_vault_accounts(id,cloud_issuer,cloud_account_id,vault_id,vault_member_id,client_member_key,signing_key_version,key_shard_id,active_key_id,backup_enabled,created_at,updated_at) VALUES
('account','https://cloud.example','cloud-user','vault','member','client',1,1,'key',TRUE,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
INSERT INTO cloud_credential_bindings(id,cloud_issuer,cloud_account_id,vault_id,cloud_record_id,local_provider_group_id,last_cloud_revision,last_local_credential_revision,backup_state,created_at,updated_at) VALUES
('binding-aihub','https://cloud.example','cloud-user','vault','record-aihub','aihub',2,3,'succeeded',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCustomProviderMigrationSplitsAndRestoresWithoutChangingReferences(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := customProviderMigrationDB(t, driver)
			seedCustomProviderMigration(t, db)
			base := filepath.Join("..", "migrations", "dev_mode", "v0_3", customProviderMigration)
			execMigrationFileForDriver(t, db, base+".up.sql", driver)
			type row struct {
				parent, name, url, protocol, keygroup, keyid string
				custom, verified, initialized                bool
			}
			read := func(groupID string) row {
				t.Helper()
				var out row
				if err := db.QueryRow(`SELECT p.id,p.name,p.base_url,p.protocol_name,p.is_custom,g.is_verified,p.active_key_group_id,p.active_key_id,p.active_key_initialized FROM user_model_provider_groups g JOIN user_model_providers p ON p.id=g.user_model_provider_id WHERE g.id=$1`, groupID).Scan(&out.parent, &out.name, &out.url, &out.protocol, &out.custom, &out.verified, &out.keygroup, &out.keyid, &out.initialized); err != nil {
					t.Fatal(err)
				}
				if len(out.parent) > 64 {
					t.Fatal("migrated supplier ID exceeds protocol bound")
				}
				return out
			}
			packy := read("packy")
			if !packy.custom || packy.name != "packyapi" || packy.protocol != "OpenAI" || packy.keygroup != "packy" || packy.keyid != "selected-key-id" || !packy.verified {
				t.Fatalf("current supplier credential was not preserved: %+v", packy)
			}
			aihub := read("aihub")
			if !aihub.custom || aihub.url != "https://aihub.example/v1/" || aihub.verified || aihub.initialized {
				t.Fatalf("original per-service URL not recovered safely: %+v", aihub)
			}
			standard := read("standard")
			if standard.custom || standard.parent != "p" || standard.url != "https://official.example/v1/" || standard.verified || standard.keyid != "" || !standard.initialized {
				t.Fatalf("builtin did not retain sole canonical store without silently enabling a successor: %+v", standard)
			}
			extra := read("extra")
			if !extra.custom || extra.parent == packy.parent || extra.parent == aihub.parent {
				t.Fatal("extra standard store was merged instead of independently migrated")
			}
			disabled := read("disabled-group")
			if !disabled.custom || disabled.url != "https://latest.example/" || disabled.keyid != "" || !disabled.initialized || disabled.verified {
				t.Fatalf("later endpoint edit or explicit disable lost: %+v", disabled)
			}
			keyless := read("keyless-group")
			if !keyless.custom || !keyless.verified || keyless.keyid != "" {
				t.Fatalf("verified keyless custom provider regressed: %+v", keyless)
			}
			var parent, modelID, cipher string
			var revision int64
			if err := db.QueryRow("SELECT user_model_provider_id FROM user_model_provider_group_models WHERE id='model-aihub'").Scan(&parent); err != nil || parent != aihub.parent {
				t.Fatal("model association not moved", err)
			}
			if err := db.QueryRow("SELECT user_model_provider_group_model_id FROM user_selected_models WHERE user_id='user'").Scan(&modelID); err != nil || modelID != "model-aihub" {
				t.Fatal("default model ID changed", err)
			}
			if err := db.QueryRow("SELECT api_key_ciphertext,credential_revision FROM user_model_provider_groups WHERE id='aihub'").Scan(&cipher, &revision); err != nil || cipher != "cipher-aihub" || revision != 3 {
				t.Fatal("ciphertext/AAD changed", err)
			}
			var count int
			if err := db.QueryRow("SELECT count(*) FROM credential_backup_outbox WHERE local_provider_group_id='aihub' AND backup_state='pending'").Scan(&count); err != nil || count != 1 {
				t.Fatal("updated cloud payload not queued", err)
			}
			if err := db.QueryRow("SELECT count(*) FROM user_model_provider_groups WHERE id IN ('deleted','foreign') AND user_model_provider_id='p'").Scan(&count); err != nil || count != 2 {
				t.Fatal("deleted/foreign-owner data was migrated", err)
			}
			// The supplier catalog can have multiple custom copies but one builtin copy.
			_, err := db.Exec(`INSERT INTO user_model_providers(id,default_model_provider_id,name,description,base_url,is_custom,protocol_name,create_user_id,create_user_name,created_at,updated_at) VALUES ('new-custom','catalog-openai','New custom','','https://new-custom.example/',TRUE,'OpenAI','user','','2026-01-01','2026-01-01');
INSERT INTO user_model_provider_groups(id,user_model_provider_id,name,base_url,api_key,create_user_id,create_user_name,created_at,updated_at) VALUES ('new-group','new-custom','New custom','https://new-custom.example/','','user','','2026-01-01','2026-01-01');`)
			if err != nil {
				t.Fatal("custom catalog copy rejected", err)
			}
			execMigrationFileForDriver(t, db, base+".down.sql", driver)
			if err := db.QueryRow("SELECT user_model_provider_id,base_url,api_key_ciphertext,credential_revision FROM user_model_provider_groups WHERE id='aihub'").Scan(&parent, &modelID, &cipher, &revision); err != nil || parent != "p" || modelID != "https://packy.example/v1/" || cipher != "cipher-aihub" || revision != 3 {
				t.Fatal("rollback failed to restore group without touching credential", err)
			}
			if err := db.QueryRow("SELECT active_key_id FROM user_model_providers WHERE id='p'").Scan(&modelID); err != nil || modelID != "selected-key-id" {
				t.Fatal("unchanged original selected key not restored", err)
			}
			if err := db.QueryRow("SELECT count(*) FROM user_model_providers WHERE id='new-custom' AND default_model_provider_id='new-custom' AND name='OpenAI'").Scan(&count); err != nil || count != 1 {
				t.Fatal("post-migration custom supplier lost on down", err)
			}
			if err := db.QueryRow("SELECT count(*) FROM user_model_provider_groups WHERE id='new-group' AND name='New custom'").Scan(&count); err != nil || count != 1 {
				t.Fatal("new supplier display/credential store lost", err)
			}
		})
	}
}

func TestCustomProviderRollbackDoesNotReviveEditedCredentialChoice(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := customProviderMigrationDB(t, driver)
			seedCustomProviderMigration(t, db)
			base := filepath.Join("..", "migrations", "dev_mode", "v0_3", customProviderMigration)
			execMigrationFileForDriver(t, db, base+".up.sql", driver)
			if _, err := db.Exec("UPDATE user_model_provider_groups SET api_key_ciphertext='edited-ciphertext',credential_revision=4 WHERE id='aihub'"); err != nil {
				t.Fatal(err)
			}
			execMigrationFileForDriver(t, db, base+".down.sql", driver)
			var key, cipher string
			var verified, initialized bool
			if err := db.QueryRow("SELECT active_key_id,active_key_initialized FROM user_model_providers WHERE id='p'").Scan(&key, &initialized); err != nil || key != "" || !initialized {
				t.Fatal("rollback revived stale supplier choice", err)
			}
			if err := db.QueryRow("SELECT api_key_ciphertext,is_verified FROM user_model_provider_groups WHERE id='aihub'").Scan(&cipher, &verified); err != nil || cipher != "edited-ciphertext" || verified {
				t.Fatal("rollback lost credential edit or restored stale verification", err)
			}
		})
	}
}

func TestCustomProviderMigrationIDDoesNotTruncateDistinctLongGroupIDs(t *testing.T) {
	db := customProviderMigrationDB(t, "sqlite")
	seedCustomProviderMigration(t, db)
	for _, tail := range []string{"a", "b"} {
		id := strings.Repeat("x", 63) + tail
		if _, err := db.Exec(`INSERT INTO user_model_provider_groups(id,user_model_provider_id,name,base_url,api_key,create_user_id,create_user_name,created_at,updated_at) VALUES ($1,'p','Long custom','https://packy.example/v1/','','user','','2026-01-10','2026-01-10')`, id); err != nil {
			t.Fatal(err)
		}
	}
	execMigrationFileForDriver(t, db, filepath.Join("..", "migrations", "dev_mode", "v0_3", customProviderMigration+".up.sql"), "sqlite")
	var count int
	if err := db.QueryRow("SELECT count(DISTINCT user_model_provider_id) FROM user_model_provider_groups WHERE name='Long custom'").Scan(&count); err != nil || count != 2 {
		t.Fatal("long group IDs collided after migration", err)
	}
}

func TestCustomProviderAggregateAndDevTransformExistingDataEqually(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			open := func(suffix string) *sql.DB {
				if driver == "sqlite" {
					return openRawSQLite(t, filepath.Join(t.TempDir(), suffix+".db"))
				}
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL DSN required")
				}
				return createTemporaryPostgresDatabase(t, dsn, "custom_parity_"+suffix)
			}
			release, dev := open("release"), open("dev")
			for i, mode := range catalog.Modes {
				if i < len(catalog.Modes)-1 {
					execMigrationFileForDriver(t, release, mode.Aggregate.UpPath, driver)
					execMigrationFileForDriver(t, dev, mode.Aggregate.UpPath, driver)
					continue
				}
				for _, db := range []*sql.DB{release, dev} {
					if _, err := db.Exec(`INSERT INTO default_model_providers(id,name,description,base_url,created_at,updated_at) VALUES ('custom-parity-source','OpenAI custom parity','','https://official.example/v1/','2026-01-01','2026-01-01');
INSERT INTO user_model_providers(id,default_model_provider_id,name,description,base_url,category,create_user_id,create_user_name,created_at,updated_at) VALUES ('parity-provider','custom-parity-source','OpenAI custom parity','','https://official.example/v1/','model','user','','2026-01-01','2026-01-01');
INSERT INTO user_model_provider_groups(id,user_model_provider_id,name,base_url,api_key,is_verified,create_user_id,create_user_name,created_at,updated_at) VALUES
('parity-a','parity-provider','packyapi','https://packy.example/v1/','first',TRUE,'user','','2026-01-01','2026-01-01'),
('parity-b','parity-provider','AIhubmix','https://aihub.example/v1/','second',TRUE,'user','','2026-01-02','2026-01-02'),
('parity-c','parity-provider','OpenAI custom parity','https://official.example/v1/','third',TRUE,'user','','2026-01-03','2026-01-03');
INSERT INTO user_model_provider_group_models(id,user_model_provider_id,user_model_provider_group_id,provider_name,name,model_type,is_default,create_user_id,create_user_name,created_at,updated_at) VALUES ('parity-model','parity-provider','parity-b','OpenAI custom parity','model','llm',FALSE,'user','','2026-01-01','2026-01-01');
INSERT INTO user_selected_models(user_id,user_name,model_type,user_model_provider_group_model_id,created_at,updated_at) VALUES ('user','','llm','parity-model','2026-01-01','2026-01-01');`); err != nil {
						t.Fatal(err)
					}
				}
				execMigrationFileForDriver(t, release, mode.Aggregate.UpPath, driver)
				for _, migration := range mode.Dev {
					execMigrationFileForDriver(t, dev, migration.UpPath, driver)
				}
			}
			// Compare final business state plus rollback metadata, rather than only an
			// empty-schema path. IDs, key pools and selected references must all match.
			for _, table := range []string{"user_model_providers", "user_model_provider_groups", "user_model_provider_group_models", "user_selected_models", "model_provider_endpoint_backups", "model_provider_custom_group_backups", "model_provider_custom_parent_backups"} {
				fingerprint := func(db *sql.DB) string {
					rows, err := db.Query("SELECT * FROM " + table)
					if err != nil {
						t.Fatal(err)
					}
					defer rows.Close()
					columns, err := rows.Columns()
					if err != nil {
						t.Fatal(err)
					}
					var lines []string
					for rows.Next() {
						values := make([]any, len(columns))
						targets := make([]any, len(columns))
						for i := range values {
							targets[i] = &values[i]
						}
						if err := rows.Scan(targets...); err != nil {
							t.Fatal(err)
						}
						var fields []string
						for i, value := range values {
							if columns[i] == "created_at" || columns[i] == "updated_at" {
								continue
							}
							if b, ok := value.([]byte); ok {
								value = string(b)
							}
							fields = append(fields, fmt.Sprintf("%s=%v", columns[i], value))
						}
						lines = append(lines, strings.Join(fields, "|"))
					}
					sort.Strings(lines)
					return strings.Join(lines, "\n")
				}
				if got, want := fingerprint(release), fingerprint(dev); got != want {
					t.Fatalf("%s aggregate/dev data differs\nrelease:%s\ndev:%s", table, got, want)
				}
			}
			if driver == "sqlite" {
				for _, object := range []string{"model_provider_custom_group_backups", "model_provider_custom_parent_backups", "uk_user_model_providers_user_default_provider"} {
					var left, right string
					if err := release.QueryRow("SELECT sql FROM sqlite_master WHERE name=$1", object).Scan(&left); err != nil {
						t.Fatal(err)
					}
					if err := dev.QueryRow("SELECT sql FROM sqlite_master WHERE name=$1", object).Scan(&right); err != nil || left != right {
						t.Fatal("aggregate/dev custom schema differs", object, err)
					}
				}
			}
		})
	}
}

func TestCustomProviderMigrationRecoversOriginalURLsAfterOldFirstStoreWasDeleted(t *testing.T) {
	db := customProviderMigrationDB(t, "sqlite")
	seedCustomProviderMigration(t, db)
	if _, err := db.Exec("UPDATE user_model_provider_groups SET deleted_at='2026-02-01' WHERE id='packy'; UPDATE user_model_providers SET active_key_group_id='',active_key_id='',active_key_initialized=TRUE WHERE id='p'"); err != nil {
		t.Fatal(err)
	}
	execMigrationFileForDriver(t, db, filepath.Join("..", "migrations", "dev_mode", "v0_3", customProviderMigration+".up.sql"), "sqlite")
	var endpoint string
	if err := db.QueryRow("SELECT base_url FROM user_model_provider_groups WHERE id='aihub'").Scan(&endpoint); err != nil || endpoint != "https://aihub.example/v1/" {
		t.Fatal("original endpoint was not recovered after original oldest store deletion", err)
	}
}

func TestCustomProviderMigrationSplitsRenamedParentsWithOfficialEndpoints(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := customProviderMigrationDB(t, driver)
			if _, err := db.Exec(`INSERT INTO default_model_providers(id,name,description,base_url,created_at,updated_at) VALUES ('renamed-source','OpenAI','','https://official.example/v1/','2026-01-01','2026-01-01');
INSERT INTO user_model_providers(id,default_model_provider_id,name,description,base_url,category,create_user_id,create_user_name,created_at,updated_at) VALUES ('renamed-parent','renamed-source','My supplier','','https://official.example/v1/','model','user','','2026-01-01','2026-01-01');
INSERT INTO user_model_provider_groups(id,user_model_provider_id,name,base_url,api_key,create_user_id,create_user_name,created_at,updated_at) VALUES ('renamed-group','renamed-parent','My supplier','https://official.example/v1/','','user','','2026-01-01','2026-01-01');`); err != nil {
				t.Fatal(err)
			}
			base := filepath.Join("..", "migrations", "dev_mode", "v0_3", customProviderMigration)
			execMigrationFileForDriver(t, db, base+".up.sql", driver)
			var custom bool
			var name, protocol string
			if err := db.QueryRow("SELECT p.name,p.protocol_name,p.is_custom FROM user_model_providers p JOIN user_model_provider_groups g ON g.user_model_provider_id=p.id WHERE g.id='renamed-group'").Scan(&name, &protocol, &custom); err != nil || name != "My supplier" || protocol != "OpenAI" || !custom {
				t.Fatal("renamed parent stayed builtin or lost display identity", err)
			}
			if err := db.QueryRow("SELECT name FROM user_model_providers WHERE id='renamed-parent'").Scan(&name); err != nil || name != "OpenAI" {
				t.Fatal("builtin display name not restored from catalog", err)
			}
			execMigrationFileForDriver(t, db, base+".down.sql", driver)
			if err := db.QueryRow("SELECT p.name FROM user_model_providers p JOIN user_model_provider_groups g ON g.user_model_provider_id=p.id WHERE g.id='renamed-group'").Scan(&name); err != nil || name != "My supplier" {
				t.Fatal("rollback lost original parent name", err)
			}
		})
	}
}
