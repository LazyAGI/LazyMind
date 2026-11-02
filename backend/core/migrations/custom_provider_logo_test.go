package migrations_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func providerLogoSchema(t *testing.T, db *sql.DB, dialect string) string {
	t.Helper()
	var columns, indexes *sql.Rows
	var err error
	lines := []string{}
	if dialect == "sqlite" {
		columns, err = db.Query("PRAGMA table_info(user_model_providers)")
	} else {
		columns, err = db.Query("SELECT column_name,data_type,is_nullable,COALESCE(column_default,'') FROM information_schema.columns WHERE table_schema='public' AND table_name='user_model_providers'")
	}
	if err != nil {
		t.Fatal(err)
	}
	for columns.Next() {
		if dialect == "sqlite" {
			var position, notnull, primary int
			var name, kind string
			var def sql.NullString
			if err := columns.Scan(&position, &name, &kind, &notnull, &def, &primary); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, fmt.Sprintf("column|%s|%s|%d|%s|%d", name, strings.ToLower(kind), notnull, def.String, primary))
		} else {
			var name, kind, nullable, def string
			if err := columns.Scan(&name, &kind, &nullable, &def); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, fmt.Sprintf("column|%s|%s|%s|%s", name, kind, nullable, def))
		}
	}
	if err := columns.Err(); err != nil {
		t.Fatal(err)
	}
	columns.Close()
	if dialect == "sqlite" {
		indexes, err = db.Query("SELECT name,COALESCE(sql,'') FROM sqlite_master WHERE type='index' AND tbl_name='user_model_providers'")
	} else {
		indexes, err = db.Query("SELECT indexname,indexdef FROM pg_indexes WHERE schemaname='public' AND tablename='user_model_providers'")
	}
	if err != nil {
		t.Fatal(err)
	}
	for indexes.Next() {
		var name, definition string
		if err := indexes.Scan(&name, &definition); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, "index|"+name+"|"+strings.Join(strings.Fields(definition), " "))
	}
	if err := indexes.Err(); err != nil {
		t.Fatal(err)
	}
	indexes.Close()
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func assertProviderLogoColumn(t *testing.T, db *sql.DB, dialect string) {
	t.Helper()
	want := "column|logo_data_url|text|1|''|0"
	if dialect == "postgres" {
		want = "column|logo_data_url|text|NO|''::text"
	}
	if schema := providerLogoSchema(t, db, dialect); !strings.Contains(schema, want) {
		t.Fatalf("provider logo must be TEXT NOT NULL DEFAULT '':\n%s", schema)
	}
	rows, err := db.Query("SELECT logo_data_url FROM user_model_provider_group_models LIMIT 0")
	if err == nil {
		rows.Close()
		t.Fatal("supplier logo migration added a model logo field")
	}
}

func insertProviderLogoHistory(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO default_model_providers (id,name,description,base_url,category,capabilities,created_at,updated_at)
VALUES ('logo-default','Logo fixture provider','Preserved description','https://logo.example/v1/','model','multi_group,custom_base_url,has_models',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
INSERT INTO user_model_providers (id,default_model_provider_id,name,description,base_url,category,capabilities,create_user_id,create_user_name,created_at,updated_at)
VALUES ('logo-provider','logo-default','Logo fixture provider','Preserved description','https://logo.example/v1/','model','multi_group,custom_base_url,has_models','logo-owner','',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
INSERT INTO user_model_provider_groups (id,user_model_provider_id,name,base_url,api_key,is_verified,create_user_id,create_user_name,created_at,updated_at)
VALUES ('logo-group','logo-provider','Logo fixture provider','https://logo.example/v1/','fixture-only-key',TRUE,'logo-owner','',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
INSERT INTO user_model_provider_group_models (id,user_model_provider_id,user_model_provider_group_id,provider_name,name,model_type,max_input_tokens,is_default,create_user_id,create_user_name,created_at,updated_at)
VALUES ('logo-model','logo-provider','logo-group','Logo fixture provider','retained-model','llm','262144',FALSE,'logo-owner','',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
INSERT INTO user_selected_models (user_id,user_name,model_type,user_model_provider_group_model_id,share,created_at,updated_at)
VALUES ('logo-owner','','llm','logo-model',TRUE,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);`)
	if err != nil {
		t.Fatal(err)
	}
}

func assertProviderLogoHistory(t *testing.T, db *sql.DB) {
	t.Helper()
	var providerID, groupID, name, modelType, context, selectedID string
	if err := db.QueryRow("SELECT user_model_provider_id,user_model_provider_group_id,name,model_type,max_input_tokens FROM user_model_provider_group_models WHERE id='logo-model'").Scan(&providerID, &groupID, &name, &modelType, &context); err != nil {
		t.Fatal(err)
	}
	if providerID != "logo-provider" || groupID != "logo-group" || name != "retained-model" || modelType != "llm" || context != "262144" {
		t.Fatalf("historical model changed: %q %q %q %q %q", providerID, groupID, name, modelType, context)
	}
	var share bool
	if err := db.QueryRow("SELECT user_model_provider_group_model_id,share FROM user_selected_models WHERE user_id='logo-owner' AND model_type='llm'").Scan(&selectedID, &share); err != nil || selectedID != "logo-model" || !share {
		t.Fatalf("default/shared reference changed: %q %v %v", selectedID, share, err)
	}
	var catalog, providerName, baseURL, key string
	if err := db.QueryRow("SELECT default_model_provider_id,name,base_url FROM user_model_providers WHERE id='logo-provider'").Scan(&catalog, &providerName, &baseURL); err != nil || catalog != "logo-default" || providerName != "Logo fixture provider" || baseURL != "https://logo.example/v1/" {
		t.Fatalf("historical provider changed: %q %q %q %v", catalog, providerName, baseURL, err)
	}
	if err := db.QueryRow("SELECT api_key FROM user_model_provider_groups WHERE id='logo-group' AND user_model_provider_id='logo-provider'").Scan(&key); err != nil || key != "fixture-only-key" {
		t.Fatal("fixture credential/group reference changed")
	}
}

func TestCustomProviderLogoAggregateMatchesDevAndRollsBack(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			aggregateDB, devDB := openDatabase(t, dialect), openDatabase(t, dialect)
			for _, db := range []*sql.DB{aggregateDB, devDB} {
				for _, version := range []string{"v0_1", "v0_2"} {
					applySQL(t, db, migrationPath(t, "version_mode/"+version+"/*.up.sql"), dialect)
				}
				insertProviderLogoHistory(t, db)
			}
			baseline := providerLogoSchema(t, aggregateDB, dialect)
			aggregate := migrationPath(t, "version_mode/v0_3/*.up.sql")
			applySQL(t, aggregateDB, aggregate, dialect)
			paths, err := filepath.Glob("dev_mode/v0_3/*.up.sql")
			if err != nil {
				t.Fatal(err)
			}
			sort.Strings(paths)
			for _, path := range paths {
				applySQL(t, devDB, path, dialect)
			}
			for _, db := range []*sql.DB{aggregateDB, devDB} {
				var logo string
				if err := db.QueryRow("SELECT logo_data_url FROM user_model_providers WHERE id='logo-provider'").Scan(&logo); err != nil {
					t.Fatalf("supplier logo column missing: %v", err)
				}
				assertProviderLogoColumn(t, db, dialect)
				if logo != "" {
					t.Fatal("migration fabricated a historical logo")
				}
				assertProviderLogoHistory(t, db)
				if _, err := db.Exec("UPDATE user_model_providers SET logo_data_url='data:image/png;base64,bG9nbw==' WHERE id='logo-provider'"); err != nil {
					t.Fatal(err)
				}
			}
			if got, want := providerLogoSchema(t, aggregateDB, dialect), providerLogoSchema(t, devDB, dialect); got != want {
				t.Fatalf("aggregate/dev supplier schemas differ\n%s\n---\n%s", got, want)
			}
			applySQL(t, aggregateDB, strings.Replace(aggregate, ".up.sql", ".down.sql", 1), dialect)
			if got := providerLogoSchema(t, aggregateDB, dialect); got != baseline {
				t.Fatalf("aggregate rollback changed prior supplier schema\n%s\n---\n%s", got, baseline)
			}
			assertProviderLogoHistory(t, aggregateDB)
		})
	}
}

func TestCustomProviderLogoDevMigrationPreservesMetadataAndRollsBack(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := openDatabase(t, dialect)
			_, err := db.Exec(`CREATE TABLE user_model_providers (id VARCHAR(64) PRIMARY KEY, name VARCHAR(255) NOT NULL, base_url VARCHAR(1024) NOT NULL, is_custom BOOLEAN NOT NULL, request_config TEXT NOT NULL, active_key_id VARCHAR(64) NOT NULL, create_user_id VARCHAR(255) NOT NULL);
CREATE INDEX logo_test_supplier_owner ON user_model_providers(create_user_id);
CREATE TABLE user_model_provider_groups (id VARCHAR(64) PRIMARY KEY, user_model_provider_id VARCHAR(64) NOT NULL REFERENCES user_model_providers(id), api_key_ciphertext TEXT NOT NULL, credential_revision BIGINT NOT NULL);
CREATE TABLE user_model_provider_group_models (id VARCHAR(64) PRIMARY KEY, user_model_provider_id VARCHAR(64) NOT NULL REFERENCES user_model_providers(id), user_model_provider_group_id VARCHAR(64) NOT NULL REFERENCES user_model_provider_groups(id), name VARCHAR(512) NOT NULL, model_type VARCHAR(64) NOT NULL, max_input_tokens VARCHAR(16), input_token_limit VARCHAR(16), max_output_tokens VARCHAR(16));
CREATE UNIQUE INDEX logo_test_model_name ON user_model_provider_group_models(user_model_provider_group_id,name);
CREATE TABLE logo_test_selected_models (id VARCHAR(64) PRIMARY KEY, model_id VARCHAR(64) NOT NULL REFERENCES user_model_provider_group_models(id), share BOOLEAN NOT NULL);
INSERT INTO user_model_providers VALUES ('old','Custom supplier','https://logo.example/v1/',TRUE,'{"default_protocol":"openai"}','key-uuid','owner');
INSERT INTO user_model_provider_groups VALUES ('group','old','fixture-only-ciphertext',7);
INSERT INTO user_model_provider_group_models VALUES ('model','old','group','custom-model','','262144','131072','65536');
INSERT INTO logo_test_selected_models VALUES ('selection','model',TRUE);`)
			if err != nil {
				t.Fatal(err)
			}
			baseline := providerLogoSchema(t, db, dialect)
			up := migrationPath(t, "dev_mode/v0_3/*_add_custom_provider_logo.up.sql")
			applySQL(t, db, up, dialect)
			assertProviderLogoColumn(t, db, dialect)
			var logo string
			if err := db.QueryRow("SELECT logo_data_url FROM user_model_providers WHERE id='old'").Scan(&logo); err != nil || logo != "" {
				t.Fatalf("legacy logo default: %q %v", logo, err)
			}
			if _, err := db.Exec("UPDATE user_model_providers SET logo_data_url='data:image/png;base64,bG9nbw==' WHERE id='old'"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("UPDATE user_model_providers SET logo_data_url=NULL WHERE id='old'"); err == nil {
				t.Fatal("logo_data_url must be NOT NULL")
			}
			applySQL(t, db, strings.Replace(up, ".up.sql", ".down.sql", 1), dialect)
			if got := providerLogoSchema(t, db, dialect); got != baseline {
				t.Fatalf("dev rollback changed supplier schema\n%s\n---\n%s", got, baseline)
			}
			var name, baseURL, requestConfig, activeKey, modelID, context, input, output, selected, ciphertext string
			var custom, share bool
			var revision int64
			if err := db.QueryRow("SELECT name,base_url,is_custom,request_config,active_key_id FROM user_model_providers WHERE id='old'").Scan(&name, &baseURL, &custom, &requestConfig, &activeKey); err != nil || name != "Custom supplier" || baseURL != "https://logo.example/v1/" || !custom || requestConfig != `{"default_protocol":"openai"}` || activeKey != "key-uuid" {
				t.Fatal("supplier settings/active selection changed")
			}
			if err := db.QueryRow("SELECT id,max_input_tokens,input_token_limit,max_output_tokens FROM user_model_provider_group_models WHERE id='model' AND user_model_provider_id='old' AND user_model_provider_group_id='group'").Scan(&modelID, &context, &input, &output); err != nil || modelID != "model" || context != "262144" || input != "131072" || output != "65536" {
				t.Fatalf("model metadata changed: %q %q %q %q %v", modelID, context, input, output, err)
			}
			if err := db.QueryRow("SELECT api_key_ciphertext,credential_revision FROM user_model_provider_groups WHERE id='group' AND user_model_provider_id='old'").Scan(&ciphertext, &revision); err != nil || ciphertext != "fixture-only-ciphertext" || revision != 7 {
				t.Fatal("fixture credential metadata changed")
			}
			if err := db.QueryRow("SELECT model_id,share FROM logo_test_selected_models WHERE id='selection'").Scan(&selected, &share); err != nil || selected != "model" || !share {
				t.Fatalf("default/shared reference changed: %q %v %v", selected, share, err)
			}
		})
	}
}
