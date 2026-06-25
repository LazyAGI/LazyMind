package source

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm/clause"
)

func TestListObjectsScansJoinedProjectionInOneRowsScan(t *testing.T) {
	now := time.Date(2026, 5, 31, 16, 0, 0, 0, time.UTC)
	db := openStoreFakeDB(t, []storeFakeQuery{
		{
			columns: storeFakeColumns(42),
			rows:    [][]driver.Value{objectWithStateRowValues(now, false)},
		},
	})
	repo := NewSQLRepository(db)

	items, nextCursor, hasMore, err := repo.ListObjects(context.Background(), ObjectListRequest{
		SourceID:         "source-1",
		BindingID:        "binding-1",
		TreeKey:          "tree-root",
		IncludeDocuments: true,
		PageSize:         10,
	})
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}
	if nextCursor != "" || hasMore || len(items) != 1 {
		t.Fatalf("unexpected list result: nextCursor=%q hasMore=%v items=%d", nextCursor, hasMore, len(items))
	}
	item := items[0]
	if item.Object.ObjectKey != "doc-1" || item.State != nil {
		t.Fatalf("expected object with nil state, got %+v", item)
	}
}

func TestListDocumentsScansJoinedProjectionInOneRowsScan(t *testing.T) {
	now := time.Date(2026, 5, 31, 16, 0, 0, 0, time.UTC)
	db := openStoreFakeDB(t, []storeFakeQuery{
		{columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}},
		{
			columns: storeFakeColumns(57),
			rows:    [][]driver.Value{documentWithStateRowValues(now, false)},
		},
	})
	repo := NewSQLRepository(db)

	items, total, err := repo.ListDocuments(context.Background(), SourceDocumentListRequest{SourceID: "source-1", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list documents: %v", err)
	}
	if total != 1 || len(items) != 1 {
		t.Fatalf("unexpected list result: total=%d items=%d", total, len(items))
	}
	item := items[0]
	if item.Object.ObjectKey != "doc-1" || item.State.ObjectKey != "doc-1" || item.Document != nil {
		t.Fatalf("expected object/state with nil document, got %+v", item)
	}
}

func TestApplyCheckpointFinishDoesNotAdvanceCursorOnFailure(t *testing.T) {
	t.Parallel()

	finishedAt := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	checkpoint := SyncCheckpoint{
		Cursor:     "cursor-before",
		RetryCount: 2,
		LastError:  JSON{},
	}

	applyCheckpointFinish(&checkpoint, SyncRunFinish{
		Status:       SyncRunStatusFailed,
		Cursor:       "cursor-after-failure",
		ErrorCode:    "FETCH_FAILED",
		ErrorMessage: "temporary connector error",
		FinishedAt:   finishedAt,
	})

	if checkpoint.Cursor != "cursor-before" {
		t.Fatalf("failed run advanced cursor: %+v", checkpoint)
	}
	if checkpoint.RetryCount != 3 || checkpoint.LastError["code"] != "FETCH_FAILED" || checkpoint.LockOwner != "" || checkpoint.LockUntil != nil {
		t.Fatalf("failure checkpoint state not recorded: %+v", checkpoint)
	}
}

func TestDecodeJSONPreservesNumberTokens(t *testing.T) {
	t.Parallel()

	value := decodeJSON([]byte(`{"max_object_size_bytes":209715200,"nested":{"delay":10},"items":[1,"two"]}`))

	assertStoreJSONNumber(t, value["max_object_size_bytes"], "209715200")
	nested, ok := value["nested"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested object, got %#v", value["nested"])
	}
	assertStoreJSONNumber(t, nested["delay"], "10")
	items, ok := value["items"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("expected array, got %#v", value["items"])
	}
	assertStoreJSONNumber(t, items[0], "1")
}

func TestMustJSONReturnsPostgresJSONTextParam(t *testing.T) {
	t.Parallel()

	param := mustJSON(JSON{
		"include_patterns":        []any{"**/*.md", "**/*.docx"},
		"max_object_size_bytes":   json.Number("209715200"),
		"reconcile_after_sync":    true,
		"reconcile_delay_minutes": json.Number("10"),
	})
	text, ok := param.(string)
	if !ok {
		t.Fatalf("jsonb param must be JSON text, got %T", param)
	}

	var decoded map[string]any
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatalf("jsonb param is not valid JSON: %v text=%q", err, text)
	}
	assertStoreJSONNumber(t, decoded["max_object_size_bytes"], "209715200")
	if decoded["reconcile_after_sync"] != true {
		t.Fatalf("boolean JSON value was not preserved: %#v", decoded["reconcile_after_sync"])
	}
	items, ok := decoded["include_patterns"].([]any)
	if !ok || len(items) != 2 || items[0] != "**/*.md" {
		t.Fatalf("array JSON value was not preserved: %#v", decoded["include_patterns"])
	}
}

func TestJSONValueReturnsValidJSONTextForStructuredProviderOptions(t *testing.T) {
	t.Parallel()

	value, err := JSON{"include_patterns": []any{"**/*.md"}, "max_object_size_bytes": json.Number("209715200"), "reconcile_after_sync": true}.Value()
	if err != nil {
		t.Fatalf("json value: %v", err)
	}
	assertValidJSONTextParam(t, value, "provider_options")
}

func TestListSourcesScansProjectedSourceFields(t *testing.T) {
	now := time.Date(2026, 5, 31, 15, 0, 0, 0, time.UTC)
	db := openStoreFakeDB(t, []storeFakeQuery{
		{columns: []string{"count"}, rows: [][]driver.Value{{int64(1)}}},
		{
			columns: []string{
				"source_id", "tenant_id", "created_by", "name", "dataset_id", "status",
				"source_options_json", "include_extensions_json", "exclude_extensions_json",
				"config_version", "deleted_at", "created_at", "updated_at", "binding_count",
			},
			rows: [][]driver.Value{{
				"source-1", "tenant-1", "user-1", "本地数据源", "dataset-1", "ACTIVE",
				[]byte(`{"source_type":"local_fs"}`), []byte(`{"items":[".md"]}`), []byte(`{"items":["~$*"]}`),
				int64(7), nil, now, now.Add(time.Minute), int64(2),
			}},
		},
	})
	repo := NewSQLRepository(db)

	records, total, err := repo.ListSources(context.Background(), SourceListRequest{TenantID: "tenant-1", Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list sources: %v", err)
	}
	if total != 1 || len(records) != 1 {
		t.Fatalf("unexpected list result: total=%d records=%d", total, len(records))
	}
	record := records[0]
	if record.Source.SourceID != "source-1" || record.Source.Name != "本地数据源" || record.Source.Status != "ACTIVE" {
		t.Fatalf("source projection was not scanned: %+v", record.Source)
	}
	if record.Source.CreatedAt != now || record.Source.UpdatedAt != now.Add(time.Minute) {
		t.Fatalf("source timestamps were not scanned: %+v", record.Source)
	}
	if record.BindingCount != 2 {
		t.Fatalf("binding count not scanned: got=%d", record.BindingCount)
	}
	if record.Source.SourceOptions["source_type"] != "local_fs" {
		t.Fatalf("source JSON options were not scanned: %#v", record.Source.SourceOptions)
	}
}

func TestListBindingsBySourceIDsScansAuthConnections(t *testing.T) {
	now := time.Date(2026, 5, 31, 15, 0, 0, 0, time.UTC)
	db := openStoreFakeDB(t, []storeFakeQuery{
		{
			columns: []string{
				"binding_id", "source_id", "binding_type", "connector_type", "target_type", "target_ref",
				"target_fingerprint", "agent_id", "auth_connection_id", "provider_options_json", "tree_key",
				"binding_generation", "core_parent_document_id", "core_parent_document_name", "sync_mode",
				"schedule_policy_json", "next_sync_at", "include_extensions_json", "exclude_extensions_json",
				"status", "last_error", "deleted_at", "created_at", "updated_at",
			},
			rows: [][]driver.Value{{
				"binding-1", "source-1", "connector_target", "feishu", "wiki_node", "wiki:space:node",
				"fingerprint-1", nil, "auth-1", []byte(`{"user_id":"user-1"}`), "tree-1",
				int64(1), "folder-1", "Docs", "manual",
				nil, nil, []byte(`{"items":[".md"]}`), []byte(`{"items":[]}`),
				"ACTIVE", []byte(`{}`), nil, now, now,
			}},
		},
	})
	repo := NewSQLRepository(db)

	bindings, err := repo.ListBindingsBySourceIDs(context.Background(), []string{"source-1", "source-2", "source-1", ""})
	if err != nil {
		t.Fatalf("list bindings by source ids: %v", err)
	}
	if len(bindings) != 1 {
		t.Fatalf("unexpected binding count: %d", len(bindings))
	}
	binding := bindings[0]
	if binding.SourceID != "source-1" || binding.BindingID != "binding-1" || binding.AuthConnectionID != "auth-1" {
		t.Fatalf("binding projection was not scanned: %+v", binding)
	}
	if binding.ProviderOptions["user_id"] != "user-1" {
		t.Fatalf("provider options were not scanned: %#v", binding.ProviderOptions)
	}
}

func TestGetSourceSummaryComputesDocumentCounts(t *testing.T) {
	db := openStoreFakeDB(t, []storeFakeQuery{
		{columns: []string{"source_id"}, rows: [][]driver.Value{{"source-1"}}},
		{columns: []string{"source_id", "binding_id"}, rows: [][]driver.Value{{"source-1", "binding-1"}}},
		{columns: []string{"count"}, rows: [][]driver.Value{{int64(5)}}},
		{columns: []string{"count"}, rows: [][]driver.Value{{int64(3)}}},
		{columns: []string{"count"}, rows: [][]driver.Value{{int64(2)}}},
		{columns: []string{"storage_bytes"}, rows: [][]driver.Value{{int64(42)}}},
		{columns: []string{"count"}, rows: [][]driver.Value{{int64(7)}}},
		{columns: []string{"source_state", "count"}, rows: [][]driver.Value{}},
		{columns: []string{"status", "count"}, rows: [][]driver.Value{}},
		{columns: []string{"binding_id"}, rows: [][]driver.Value{}},
	})
	repo := NewSQLRepository(db)

	summary, err := repo.GetSourceSummary(context.Background(), SourceSummaryRequest{SourceID: "source-1", BindingID: "binding-1"})
	if err != nil {
		t.Fatalf("get source summary: %v", err)
	}
	if summary.TotalObjects != 5 || summary.DocumentObjects != 3 || summary.ContainerObjects != 2 {
		t.Fatalf("summary counts were not scanned: %+v", summary)
	}
	if summary.StorageBytes != 42 {
		t.Fatalf("storage bytes were not aggregated: got=%d", summary.StorageBytes)
	}
	if summary.ParsedDocumentCount != 7 {
		t.Fatalf("parsed document count was not aggregated: got=%d", summary.ParsedDocumentCount)
	}
}

func TestValidateSourceObjectIndexRowEnforcesParentDepthContract(t *testing.T) {
	t.Parallel()

	valid := SourceObject{
		SourceID:    "source-1",
		BindingID:   "binding-1",
		TreeKey:     "root",
		ObjectKey:   "root",
		DisplayName: "Root",
		SearchName:  "root",
		ObjectType:  "folder",
		Depth:       0,
	}
	if err := validateSourceObjectIndexRow(valid); err != nil {
		t.Fatalf("valid root rejected: %v", err)
	}
	child := valid
	child.ObjectKey = "doc-1"
	child.ParentKey = "root"
	child.Depth = 1
	if err := validateSourceObjectIndexRow(child); err != nil {
		t.Fatalf("valid child rejected: %v", err)
	}
	badRoot := valid
	badRoot.Depth = 1
	if err := validateSourceObjectIndexRow(badRoot); ErrorCodeOf(err) != ErrCodeInternal {
		t.Fatalf("expected invalid root depth error, got %v", err)
	}
	badChild := child
	badChild.Depth = 0
	if err := validateSourceObjectIndexRow(badChild); ErrorCodeOf(err) != ErrCodeInternal {
		t.Fatalf("expected invalid child depth error, got %v", err)
	}
}

func TestSourceObjectUpsertAssignmentsPreserveExistingSizeOnZero(t *testing.T) {
	t.Parallel()

	assignments := sourceObjectUpsertAssignments()
	for _, assignment := range assignments {
		if assignment.Column.Name != "size_bytes" {
			continue
		}
		expr, ok := assignment.Value.(clause.Expr)
		if !ok {
			t.Fatalf("size_bytes update should use expression, got %#v", assignment.Value)
		}
		if !strings.Contains(expr.SQL, "excluded.size_bytes > 0") ||
			!strings.Contains(expr.SQL, "provider_meta_json->>'kind' IN") ||
			!strings.Contains(expr.SQL, "wiki_node") ||
			!strings.Contains(expr.SQL, "drive_file") ||
			!strings.Contains(expr.SQL, "source_object_index.size_bytes") {
			t.Fatalf("size_bytes update should keep existing feishu export size when incoming size is zero: %q", expr.SQL)
		}
		return
	}
	t.Fatalf("size_bytes assignment was not configured")
}

type storeFakeQuery struct {
	columns []string
	rows    [][]driver.Value
}

type storeFakeDriver struct{}

type storeFakeConn struct {
	dsn string
}

type storeFakeRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

var (
	storeFakeDriverOnce sync.Once
	storeFakeMu         sync.Mutex
	storeFakeQueries    = map[string][]storeFakeQuery{}
)

func openStoreFakeDB(t *testing.T, queries []storeFakeQuery) *sql.DB {
	t.Helper()
	storeFakeDriverOnce.Do(func() {
		sql.Register("scan_store_fake", storeFakeDriver{})
	})
	dsn := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
	storeFakeMu.Lock()
	storeFakeQueries[dsn] = queries
	storeFakeMu.Unlock()
	db, err := sql.Open("scan_store_fake", dsn)
	if err != nil {
		t.Fatalf("open fake db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		storeFakeMu.Lock()
		delete(storeFakeQueries, dsn)
		storeFakeMu.Unlock()
	})
	return db
}

func (storeFakeDriver) Open(name string) (driver.Conn, error) {
	return storeFakeConn{dsn: name}, nil
}

func (storeFakeConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare is not supported by storeFakeConn")
}

func (storeFakeConn) Close() error {
	return nil
}

func (storeFakeConn) Begin() (driver.Tx, error) {
	return nil, errors.New("transactions are not supported by storeFakeConn")
}

func (c storeFakeConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	storeFakeMu.Lock()
	defer storeFakeMu.Unlock()
	queries := storeFakeQueries[c.dsn]
	if len(queries) == 0 {
		return nil, errors.New("unexpected query")
	}
	query := queries[0]
	storeFakeQueries[c.dsn] = queries[1:]
	return &storeFakeRows{columns: query.columns, rows: query.rows}, nil
}

func (r *storeFakeRows) Columns() []string {
	return r.columns
}

func (r *storeFakeRows) Close() error {
	return nil
}

func (r *storeFakeRows) Next(dest []driver.Value) error {
	if r.index >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.index])
	r.index++
	return nil
}

func assertStoreJSONNumber(t *testing.T, value any, want string) {
	t.Helper()
	number, ok := value.(json.Number)
	if !ok {
		t.Fatalf("expected json.Number %q, got %#v", want, value)
	}
	if number.String() != want {
		t.Fatalf("unexpected number: got=%s want=%s", number.String(), want)
	}
}

func assertValidJSONTextParam(t *testing.T, value any, name string) {
	t.Helper()
	text, ok := value.(string)
	if !ok {
		t.Fatalf("%s JSON param must be string, got %T", name, value)
	}
	var decoded map[string]any
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatalf("%s JSON param is invalid: %v text=%q", name, err, text)
	}
}

func fillObjectScan(dest []any, now time.Time) {
	*(dest[0].(*string)) = "source-1"
	*(dest[1].(*string)) = "binding-1"
	*(dest[2].(*string)) = "tree-root"
	*(dest[3].(*string)) = "doc-1"
	*(dest[4].(*sql.NullString)) = sql.NullString{}
	*(dest[5].(*string)) = "Doc 1"
	*(dest[6].(*string)) = "doc 1"
	*(dest[7].(*string)) = "file"
	*(dest[8].(*bool)) = true
	*(dest[9].(*bool)) = false
	*(dest[10].(*bool)) = false
	*(dest[11].(*sql.NullString)) = sql.NullString{String: "v1", Valid: true}
	*(dest[12].(*sql.NullInt64)) = sql.NullInt64{Int64: 3, Valid: true}
	*(dest[13].(*sql.NullString)) = sql.NullString{String: "text/markdown", Valid: true}
	*(dest[14].(*sql.NullString)) = sql.NullString{String: ".md", Valid: true}
	*(dest[15].(*sql.NullTime)) = sql.NullTime{Time: now, Valid: true}
	*(dest[16].(*sql.NullTime)) = sql.NullTime{}
	*(dest[17].(*int64)) = 0
	*(dest[18].(*[]byte)) = []byte(`{"id":"doc-1"}`)
	*(dest[19].(*sql.NullString)) = sql.NullString{String: "run-1", Valid: true}
	*(dest[20].(*time.Time)) = now
	*(dest[21].(*time.Time)) = now
}

func fillDocumentStateScan(dest []any, now time.Time) {
	*(dest[0].(*sql.NullString)) = sql.NullString{String: "source-1", Valid: true}
	*(dest[1].(*sql.NullString)) = sql.NullString{String: "binding-1", Valid: true}
	*(dest[2].(*sql.NullInt64)) = sql.NullInt64{Int64: 1, Valid: true}
	*(dest[3].(*sql.NullString)) = sql.NullString{String: "doc-1", Valid: true}
	*(dest[4].(*sql.NullString)) = sql.NullString{String: "v1", Valid: true}
	*(dest[5].(*sql.NullString)) = sql.NullString{String: "baseline-1", Valid: true}
	*(dest[6].(*sql.NullTime)) = sql.NullTime{}
	*(dest[7].(*sql.NullString)) = sql.NullString{String: "NEW", Valid: true}
	*(dest[8].(*sql.NullString)) = sql.NullString{String: "IDLE", Valid: true}
	*(dest[9].(*sql.NullString)) = sql.NullString{String: "CREATE", Valid: true}
	*(dest[10].(*sql.NullBool)) = sql.NullBool{Bool: true, Valid: true}
	*(dest[11].(*sql.NullBool)) = sql.NullBool{Bool: true, Valid: true}
	*(dest[12].(*sql.NullString)) = sql.NullString{String: "NONE", Valid: true}
	*(dest[13].(*sql.NullString)) = sql.NullString{}
	*(dest[14].(*sql.NullString)) = sql.NullString{}
	*(dest[15].(*sql.NullTime)) = sql.NullTime{Time: now, Valid: true}
	*(dest[16].(*sql.NullTime)) = sql.NullTime{}
	*(dest[17].(*[]byte)) = nil
	*(dest[18].(*sql.NullTime)) = sql.NullTime{Time: now, Valid: true}
	*(dest[19].(*sql.NullTime)) = sql.NullTime{Time: now, Valid: true}
}

func fillNullDocumentStateScan(dest []any) {
	*(dest[0].(*sql.NullString)) = sql.NullString{}
	*(dest[1].(*sql.NullString)) = sql.NullString{}
	*(dest[2].(*sql.NullInt64)) = sql.NullInt64{}
	*(dest[3].(*sql.NullString)) = sql.NullString{}
	*(dest[4].(*sql.NullString)) = sql.NullString{}
	*(dest[5].(*sql.NullString)) = sql.NullString{}
	*(dest[6].(*sql.NullTime)) = sql.NullTime{}
	*(dest[7].(*sql.NullString)) = sql.NullString{}
	*(dest[8].(*sql.NullString)) = sql.NullString{}
	*(dest[9].(*sql.NullString)) = sql.NullString{}
	*(dest[10].(*sql.NullBool)) = sql.NullBool{}
	*(dest[11].(*sql.NullBool)) = sql.NullBool{}
	*(dest[12].(*sql.NullString)) = sql.NullString{}
	*(dest[13].(*sql.NullString)) = sql.NullString{}
	*(dest[14].(*sql.NullString)) = sql.NullString{}
	*(dest[15].(*sql.NullTime)) = sql.NullTime{}
	*(dest[16].(*sql.NullTime)) = sql.NullTime{}
	*(dest[17].(*[]byte)) = nil
	*(dest[18].(*sql.NullTime)) = sql.NullTime{}
	*(dest[19].(*sql.NullTime)) = sql.NullTime{}
}

func fillNullDocumentScan(dest []any) {
	*(dest[0].(*sql.NullString)) = sql.NullString{}
	*(dest[1].(*sql.NullString)) = sql.NullString{}
	*(dest[2].(*sql.NullString)) = sql.NullString{}
	*(dest[3].(*sql.NullString)) = sql.NullString{}
	*(dest[4].(*sql.NullString)) = sql.NullString{}
	*(dest[5].(*sql.NullString)) = sql.NullString{}
	*(dest[6].(*sql.NullString)) = sql.NullString{}
	*(dest[7].(*sql.NullString)) = sql.NullString{}
	*(dest[8].(*sql.NullString)) = sql.NullString{}
	*(dest[9].(*sql.NullString)) = sql.NullString{}
	*(dest[10].(*sql.NullString)) = sql.NullString{}
	*(dest[11].(*sql.NullString)) = sql.NullString{}
	*(dest[12].(*sql.NullString)) = sql.NullString{}
	*(dest[13].(*sql.NullTime)) = sql.NullTime{}
	*(dest[14].(*sql.NullTime)) = sql.NullTime{}
}

func storeFakeColumns(count int) []string {
	columns := make([]string, count)
	for i := range columns {
		columns[i] = fmt.Sprintf("column_%d", i)
	}
	return columns
}

func objectWithStateRowValues(now time.Time, includeState bool) []driver.Value {
	values := []driver.Value{
		"source-1", "binding-1", "tree-root", "doc-1", nil,
		"Doc 1", "doc 1", "file", true, false,
		false, "v1", int64(3), "text/markdown", ".md",
		now, nil, int64(0), []byte(`{"id":"doc-1"}`), "run-1",
		now, now,
	}
	if includeState {
		return append(values,
			"source-1", "binding-1", int64(1), "doc-1", "v1",
			"baseline-1", nil, "NEW", "IDLE", "CREATE",
			true, true, "NONE", nil, nil,
			now, nil, nil, now, now,
		)
	}
	return append(values,
		nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil,
	)
}

func documentWithStateRowValues(now time.Time, includeDocument bool) []driver.Value {
	values := append([]driver.Value(nil), objectWithStateRowValues(now, true)...)
	if includeDocument {
		return append(values,
			"document-1", "tenant-1", "source-1", "binding-1", "doc-1",
			"core-document-1", "current-version-1", "desired-version-1", "v1", "Doc 1",
			"text/markdown", ".md", "PENDING", now, now,
		)
	}
	return append(values,
		nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil,
		nil, nil, nil, nil, nil,
	)
}
