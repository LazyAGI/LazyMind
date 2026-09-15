package sqliteproxy

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const DriverName = "lazymind-sqliteproxy"

const (
	serverURLEnv       = "LAZYMIND_SQLITE_SERVER_URL"
	serverTokenFileEnv = "LAZYMIND_SQLITE_SERVER_TOKEN_FILE"
)

type wireValue struct {
	Type  string  `json:"type"`
	Text  string  `json:"text,omitempty"`
	Int   string  `json:"int,omitempty"`
	Float float64 `json:"float,omitempty"`
	Bool  bool    `json:"bool,omitempty"`
}

type request struct {
	DB      string        `json:"db"`
	TxID    string        `json:"txId,omitempty"`
	SQL     string        `json:"sql,omitempty"`
	Args    []wireValue   `json:"args,omitempty"`
	Batches [][]wireValue `json:"batches,omitempty"`
}

type response struct {
	TxID         string        `json:"txId,omitempty"`
	Columns      []string      `json:"columns,omitempty"`
	Rows         [][]wireValue `json:"rows,omitempty"`
	RowsAffected int64         `json:"rowsAffected,omitempty"`
	LastInsertID int64         `json:"lastInsertId,omitempty"`
	Error        string        `json:"error,omitempty"`
}

type proxyDriver struct{}

type proxyConn struct {
	baseURL string
	token   string
	db      string
	client  *http.Client
	mu      sync.Mutex
	txID    string
	closed  bool
}

type proxyStmt struct {
	conn  *proxyConn
	query string
}

type proxyTx struct {
	conn *proxyConn
	once sync.Once
}

type proxyResult struct {
	lastInsertID int64
	rowsAffected int64
}

type proxyRows struct {
	columns []string
	rows    [][]driver.Value
	index   int
}

type proxyError struct {
	message string
}

func (err *proxyError) Error() string {
	return err.message
}

func newProxyError(format string, args ...any) error {
	return &proxyError{message: fmt.Sprintf(format, args...)}
}

func init() {
	sql.Register(DriverName, proxyDriver{})
}

func Open(alias string) (*sql.DB, error) {
	alias = normalizeAlias(alias)
	if alias == "" {
		return nil, newProxyError("sqlite proxy database alias is empty")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv(serverURLEnv)), "/")
	if baseURL == "" {
		return nil, newProxyError("%s is empty", serverURLEnv)
	}
	if _, err := readToken(); err != nil {
		return nil, err
	}
	return sql.Open(DriverName, alias)
}

func normalizeAlias(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "sqliteproxy://")
	return strings.Trim(value, "/")
}

func (proxyDriver) Open(name string) (driver.Conn, error) {
	alias := normalizeAlias(name)
	if alias == "" {
		return nil, newProxyError("sqlite proxy database alias is empty")
	}
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv(serverURLEnv)), "/")
	if baseURL == "" {
		return nil, newProxyError("%s is empty", serverURLEnv)
	}
	token, err := readToken()
	if err != nil {
		return nil, err
	}
	return &proxyConn{
		baseURL: baseURL,
		token:   token,
		db:      alias,
		client:  &http.Client{},
	}, nil
}

func readToken() (string, error) {
	path := strings.TrimSpace(os.Getenv(serverTokenFileEnv))
	if path == "" {
		return "", newProxyError("%s is empty", serverTokenFileEnv)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", newProxyError("read sqlite proxy token: %v", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", newProxyError("sqlite proxy token is empty")
	}
	return token, nil
}

func (conn *proxyConn) Prepare(query string) (driver.Stmt, error) {
	return &proxyStmt{conn: conn, query: query}, nil
}

func (conn *proxyConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return conn.Prepare(query)
}

func (conn *proxyConn) Close() error {
	conn.mu.Lock()
	if conn.closed {
		conn.mu.Unlock()
		return nil
	}
	conn.closed = true
	txID := conn.txID
	conn.txID = ""
	conn.mu.Unlock()
	if txID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := conn.call(ctx, "/v1/rollback", request{DB: conn.db, TxID: txID})
	return err
}

func (conn *proxyConn) Begin() (driver.Tx, error) {
	return conn.BeginTx(context.Background(), driver.TxOptions{})
}

func (conn *proxyConn) BeginTx(ctx context.Context, options driver.TxOptions) (driver.Tx, error) {
	_ = options
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.closed {
		return nil, driver.ErrBadConn
	}
	if conn.txID != "" {
		return nil, newProxyError("sqlite proxy transaction already active")
	}
	resp, err := conn.call(ctx, "/v1/begin", request{DB: conn.db})
	if err != nil {
		return nil, err
	}
	conn.txID = resp.TxID
	return &proxyTx{conn: conn}, nil
}

func (conn *proxyConn) Ping(ctx context.Context) error {
	rows, err := conn.query(ctx, "SELECT 1", nil)
	if err != nil {
		return err
	}
	return rows.Close()
}

func (conn *proxyConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	values, err := encodeNamedValues(args)
	if err != nil {
		return nil, err
	}
	return conn.exec(ctx, query, values)
}

func (conn *proxyConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	values, err := encodeNamedValues(args)
	if err != nil {
		return nil, err
	}
	return conn.query(ctx, query, values)
}

func (conn *proxyConn) IsValid() bool {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	return !conn.closed
}

func (conn *proxyConn) exec(ctx context.Context, query string, args []wireValue) (driver.Result, error) {
	txID, err := conn.currentTxID()
	if err != nil {
		return nil, err
	}
	resp, err := conn.call(ctx, "/v1/execute", request{DB: conn.db, TxID: txID, SQL: query, Args: args})
	if err != nil {
		return nil, err
	}
	return proxyResult{lastInsertID: resp.LastInsertID, rowsAffected: resp.RowsAffected}, nil
}

func (conn *proxyConn) query(ctx context.Context, query string, args []wireValue) (driver.Rows, error) {
	txID, err := conn.currentTxID()
	if err != nil {
		return nil, err
	}
	resp, err := conn.call(ctx, "/v1/query", request{DB: conn.db, TxID: txID, SQL: query, Args: args})
	if err != nil {
		return nil, err
	}
	rows := &proxyRows{columns: resp.Columns, rows: make([][]driver.Value, len(resp.Rows))}
	for rowIndex, encodedRow := range resp.Rows {
		row := make([]driver.Value, len(encodedRow))
		for columnIndex, encoded := range encodedRow {
			row[columnIndex], err = decodeValue(encoded)
			if err != nil {
				return nil, err
			}
		}
		rows.rows[rowIndex] = row
	}
	return rows, nil
}

func (conn *proxyConn) currentTxID() (string, error) {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if conn.closed {
		return "", driver.ErrBadConn
	}
	return conn.txID, nil
}

func (conn *proxyConn) call(ctx context.Context, path string, payload request) (response, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return response{}, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, conn.baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return response{}, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+conn.token)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpResponse, err := conn.client.Do(httpRequest)
	if err != nil {
		return response{}, err
	}
	defer httpResponse.Body.Close()
	var decoded response
	if err := json.NewDecoder(httpResponse.Body).Decode(&decoded); err != nil {
		if errors.Is(err, io.EOF) {
			return response{}, newProxyError("sqlite proxy returned HTTP %d without a response", httpResponse.StatusCode)
		}
		return response{}, err
	}
	if httpResponse.StatusCode < 200 || httpResponse.StatusCode >= 300 {
		if decoded.Error == "" {
			decoded.Error = httpResponse.Status
		}
		return response{}, newProxyError("%s", decoded.Error)
	}
	return decoded, nil
}

func (statement *proxyStmt) Close() error {
	return nil
}

func (statement *proxyStmt) NumInput() int {
	return -1
}

func (statement *proxyStmt) Exec(args []driver.Value) (driver.Result, error) {
	return statement.ExecContext(context.Background(), namedValues(args))
}

func (statement *proxyStmt) Query(args []driver.Value) (driver.Rows, error) {
	return statement.QueryContext(context.Background(), namedValues(args))
}

func (statement *proxyStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	return statement.conn.ExecContext(ctx, statement.query, args)
}

func (statement *proxyStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	return statement.conn.QueryContext(ctx, statement.query, args)
}

func namedValues(values []driver.Value) []driver.NamedValue {
	result := make([]driver.NamedValue, len(values))
	for index, value := range values {
		result[index] = driver.NamedValue{Ordinal: index + 1, Value: value}
	}
	return result
}

func (transaction *proxyTx) Commit() error {
	return transaction.finish("/v1/commit")
}

func (transaction *proxyTx) Rollback() error {
	return transaction.finish("/v1/rollback")
}

func (transaction *proxyTx) finish(path string) error {
	var finishErr error
	transaction.once.Do(func() {
		transaction.conn.mu.Lock()
		defer transaction.conn.mu.Unlock()
		if transaction.conn.txID == "" {
			finishErr = sql.ErrTxDone
			return
		}
		txID := transaction.conn.txID
		_, finishErr = transaction.conn.call(
			context.Background(),
			path,
			request{DB: transaction.conn.db, TxID: txID},
		)
		transaction.conn.txID = ""
	})
	return finishErr
}

func (result proxyResult) LastInsertId() (int64, error) {
	return result.lastInsertID, nil
}

func (result proxyResult) RowsAffected() (int64, error) {
	return result.rowsAffected, nil
}

func (rows *proxyRows) Columns() []string {
	return append([]string(nil), rows.columns...)
}

func (rows *proxyRows) Close() error {
	rows.index = len(rows.rows)
	return nil
}

func (rows *proxyRows) Next(dest []driver.Value) error {
	if rows.index >= len(rows.rows) {
		return io.EOF
	}
	copy(dest, rows.rows[rows.index])
	rows.index++
	return nil
}

func encodeNamedValues(values []driver.NamedValue) ([]wireValue, error) {
	encoded := make([]wireValue, len(values))
	for index, value := range values {
		var err error
		encoded[index], err = encodeValue(value.Value)
		if err != nil {
			return nil, newProxyError("argument %d: %v", index, err)
		}
	}
	return encoded, nil
}

func encodeValue(value any) (wireValue, error) {
	switch typed := value.(type) {
	case nil:
		return wireValue{Type: "null"}, nil
	case int64:
		return wireValue{Type: "int", Int: strconv.FormatInt(typed, 10)}, nil
	case float64:
		return wireValue{Type: "float", Float: typed}, nil
	case bool:
		return wireValue{Type: "bool", Bool: typed}, nil
	case string:
		return wireValue{Type: "text", Text: typed}, nil
	case []byte:
		return wireValue{Type: "bytes", Text: base64.StdEncoding.EncodeToString(typed)}, nil
	case time.Time:
		return wireValue{Type: "time", Text: typed.Format(time.RFC3339Nano)}, nil
	default:
		return wireValue{}, newProxyError("unsupported sqlite value type %T", value)
	}
}

func decodeValue(value wireValue) (driver.Value, error) {
	switch value.Type {
	case "null":
		return nil, nil
	case "int":
		return strconv.ParseInt(value.Int, 10, 64)
	case "float":
		return value.Float, nil
	case "bool":
		return value.Bool, nil
	case "text":
		return value.Text, nil
	case "bytes":
		return base64.StdEncoding.DecodeString(value.Text)
	case "time":
		return time.Parse(time.RFC3339Nano, value.Text)
	default:
		return nil, newProxyError("unsupported sqlite value type %q", value.Type)
	}
}
