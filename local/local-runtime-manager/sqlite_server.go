package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "github.com/glebarez/go-sqlite"
)

const (
	sqliteServerHealthPath     = "/healthz"
	sqliteServerTransactionTTL = 5 * time.Minute
	sqliteServerMaxBodyBytes   = 16 << 20
)

type sqliteWireValue struct {
	Type  string  `json:"type"`
	Text  string  `json:"text,omitempty"`
	Int   string  `json:"int,omitempty"`
	Float float64 `json:"float,omitempty"`
	Bool  bool    `json:"bool,omitempty"`
}

type sqliteRequest struct {
	DB      string              `json:"db"`
	TxID    string              `json:"txId,omitempty"`
	SQL     string              `json:"sql,omitempty"`
	Args    []sqliteWireValue   `json:"args,omitempty"`
	Batches [][]sqliteWireValue `json:"batches,omitempty"`
}

type sqliteResponse struct {
	TxID         string              `json:"txId,omitempty"`
	Columns      []string            `json:"columns,omitempty"`
	Rows         [][]sqliteWireValue `json:"rows,omitempty"`
	RowsAffected int64               `json:"rowsAffected,omitempty"`
	LastInsertID int64               `json:"lastInsertId,omitempty"`
	Error        string              `json:"error,omitempty"`
}

type sqliteDatabase struct {
	name string
	db   *sql.DB
	gate chan struct{}
}

type sqliteTransaction struct {
	id       string
	database *sqliteDatabase
	tx       *sql.Tx
	timer    *time.Timer
	once     sync.Once
	opMu     sync.Mutex
}

type SQLiteServerManager struct{}

type sqliteHTTPServer struct {
	token        string
	databases    map[string]*sqliteDatabase
	transactions map[string]*sqliteTransaction
	mu           sync.Mutex
	nextTxID     atomic.Uint64
	txTTL        time.Duration
}

func NewSQLiteServerManager() *SQLiteServerManager {
	return &SQLiteServerManager{}
}

func (m *SQLiteServerManager) Run(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	if err := paths.EnsureAllDirs(); err != nil {
		return err
	}
	token, err := readSQLiteServerToken(paths.RunDirTokenFile)
	if err != nil {
		return err
	}
	server, err := newSQLiteHTTPServer(
		token, sqliteServerDatabaseAliases(paths), sqliteServerTransactionTTL,
	)
	if err != nil {
		return err
	}
	defer server.Close()
	if err := os.WriteFile(paths.SQLiteServerPIDFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		return err
	}
	defer os.Remove(paths.SQLiteServerPIDFile)
	registerLocalProcess(
		paths,
		sqliteServerProcessName,
		os.Getpid(),
		[]int{cfg.SQLiteServerPort},
		append([]string(nil), os.Args...),
	)
	defer unregisterLocalProcess(paths, sqliteServerProcessName, os.Getpid())

	httpServer := &http.Server{
		Addr:              "127.0.0.1:" + strconv.Itoa(cfg.SQLiteServerPort),
		Handler:           server.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	fmt.Printf("sqlite-server listening on %s\n", httpServer.Addr)
	err = httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func sqliteServerDatabaseAliases(paths RuntimePaths) map[string]string {
	aliases := map[string]string{
		"core":    paths.CoreDBPath,
		"lazyllm": paths.LazyLLMDBPath,
	}
	// OpenSearch owns segments in that supported mode. Its HTTP(S) endpoint is
	// not a SQLite filename and must never be passed to sql.Open.
	if !strings.EqualFold(localSegmentStoreType(), "opensearch") {
		aliases["segments"] = localSegmentStoreBackingPath(paths)
	}
	return aliases
}

func (m *SQLiteServerManager) Down(ctx context.Context, cfg RuntimeConfig, paths RuntimePaths) error {
	return stopManagedServiceByPIDFile(ctx, paths, sqliteServerProcessName, paths.SQLiteServerPIDFile)
}

func newSQLiteHTTPServer(token string, aliases map[string]string, txTTL time.Duration) (*sqliteHTTPServer, error) {
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("sqlite-server token is empty")
	}
	server := &sqliteHTTPServer{
		token:        token,
		databases:    make(map[string]*sqliteDatabase, len(aliases)),
		transactions: make(map[string]*sqliteTransaction),
		txTTL:        txTTL,
	}
	for alias, path := range aliases {
		database, err := openSQLiteServerDatabase(alias, path)
		if err != nil {
			server.Close()
			return nil, err
		}
		server.databases[alias] = database
	}
	return server, nil
}

func openSQLiteServerDatabase(alias, path string) (*sqliteDatabase, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, fmt.Errorf("open sqlite alias %s: %w", alias, err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, statement := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=30000",
	} {
		if _, err := db.Exec(statement); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("configure sqlite alias %s: %w", alias, err)
		}
	}
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	return &sqliteDatabase{name: alias, db: db, gate: gate}, nil
}

func readSQLiteServerToken(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read sqlite-server token: %w", err)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("sqlite-server token file is empty: %s", path)
	}
	return token, nil
}

func (s *sqliteHTTPServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(sqliteServerHealthPath, s.handleHealth)
	mux.HandleFunc("/v1/begin", s.handleBegin)
	mux.HandleFunc("/v1/execute", s.handleExecute)
	mux.HandleFunc("/v1/executemany", s.handleExecuteMany)
	mux.HandleFunc("/v1/query", s.handleQuery)
	mux.HandleFunc("/v1/commit", s.handleCommit)
	mux.HandleFunc("/v1/rollback", s.handleRollback)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != sqliteServerHealthPath && r.Header.Get("Authorization") != "Bearer "+s.token {
			writeSQLiteError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func (s *sqliteHTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeSQLiteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeSQLiteJSON(w, http.StatusOK, sqliteResponse{})
}

func (s *sqliteHTTPServer) handleBegin(w http.ResponseWriter, r *http.Request) {
	req, database, ok := s.decodeDatabaseRequest(w, r)
	if !ok {
		return
	}
	select {
	case <-database.gate:
	case <-r.Context().Done():
		writeSQLiteError(w, http.StatusRequestTimeout, r.Context().Err().Error())
		return
	}
	// The request context ends when /begin returns, but the transaction must
	// remain alive for later execute/commit calls from the same client.
	tx, err := database.db.BeginTx(context.Background(), nil)
	if err != nil {
		database.gate <- struct{}{}
		writeSQLiteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	txID := fmt.Sprintf("%s-%d", database.name, s.nextTxID.Add(1))
	managed := &sqliteTransaction{id: txID, database: database, tx: tx}
	managed.timer = time.AfterFunc(s.txTTL, func() { s.expireTransaction(txID) })
	s.mu.Lock()
	s.transactions[txID] = managed
	s.mu.Unlock()
	_ = req
	writeSQLiteJSON(w, http.StatusOK, sqliteResponse{TxID: txID})
}

func (s *sqliteHTTPServer) handleExecute(w http.ResponseWriter, r *http.Request) {
	req, database, ok := s.decodeDatabaseRequest(w, r)
	if !ok {
		return
	}
	args, err := decodeSQLiteValues(req.Args)
	if err != nil {
		writeSQLiteError(w, http.StatusBadRequest, err.Error())
		return
	}
	var result sql.Result
	err = s.withExecutor(r.Context(), req, database, func(executor sqliteExecutor) error {
		var execErr error
		result, execErr = executor.ExecContext(r.Context(), req.SQL, args...)
		return execErr
	})
	if err != nil {
		writeSQLiteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeSQLiteResult(w, result)
}

func (s *sqliteHTTPServer) handleExecuteMany(w http.ResponseWriter, r *http.Request) {
	req, database, ok := s.decodeDatabaseRequest(w, r)
	if !ok {
		return
	}
	var rowsAffected int64
	var lastInsertID int64
	err := s.withExecutor(r.Context(), req, database, func(executor sqliteExecutor) error {
		for _, batch := range req.Batches {
			args, err := decodeSQLiteValues(batch)
			if err != nil {
				return err
			}
			result, err := executor.ExecContext(r.Context(), req.SQL, args...)
			if err != nil {
				return err
			}
			if count, err := result.RowsAffected(); err == nil {
				rowsAffected += count
			}
			if id, err := result.LastInsertId(); err == nil {
				lastInsertID = id
			}
		}
		return nil
	})
	if err != nil {
		writeSQLiteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeSQLiteJSON(w, http.StatusOK, sqliteResponse{
		RowsAffected: rowsAffected,
		LastInsertID: lastInsertID,
	})
}

func (s *sqliteHTTPServer) handleQuery(w http.ResponseWriter, r *http.Request) {
	req, database, ok := s.decodeDatabaseRequest(w, r)
	if !ok {
		return
	}
	args, err := decodeSQLiteValues(req.Args)
	if err != nil {
		writeSQLiteError(w, http.StatusBadRequest, err.Error())
		return
	}
	var response sqliteResponse
	err = s.withExecutor(r.Context(), req, database, func(executor sqliteExecutor) error {
		rows, err := executor.QueryContext(r.Context(), req.SQL, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		response.Columns, err = rows.Columns()
		if err != nil {
			return err
		}
		for rows.Next() {
			values := make([]any, len(response.Columns))
			destinations := make([]any, len(values))
			for index := range values {
				destinations[index] = &values[index]
			}
			if err := rows.Scan(destinations...); err != nil {
				return err
			}
			encoded := make([]sqliteWireValue, len(values))
			for index, value := range values {
				encoded[index], err = encodeSQLiteValue(value)
				if err != nil {
					return err
				}
			}
			response.Rows = append(response.Rows, encoded)
		}
		return rows.Err()
	})
	if err != nil {
		writeSQLiteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeSQLiteJSON(w, http.StatusOK, response)
}

func (s *sqliteHTTPServer) handleCommit(w http.ResponseWriter, r *http.Request) {
	s.finishTransaction(w, r, true)
}

func (s *sqliteHTTPServer) handleRollback(w http.ResponseWriter, r *http.Request) {
	s.finishTransaction(w, r, false)
}

func (s *sqliteHTTPServer) finishTransaction(w http.ResponseWriter, r *http.Request, commit bool) {
	req, _, ok := s.decodeDatabaseRequest(w, r)
	if !ok {
		return
	}
	transaction, err := s.takeTransaction(req.DB, req.TxID)
	if err != nil {
		writeSQLiteError(w, http.StatusBadRequest, err.Error())
		return
	}
	transaction.opMu.Lock()
	defer transaction.opMu.Unlock()
	if commit {
		err = transaction.tx.Commit()
	} else {
		err = transaction.tx.Rollback()
	}
	transaction.release()
	if err != nil && !errors.Is(err, sql.ErrTxDone) {
		writeSQLiteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeSQLiteJSON(w, http.StatusOK, sqliteResponse{})
}

type sqliteExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (s *sqliteHTTPServer) withExecutor(
	ctx context.Context,
	req sqliteRequest,
	database *sqliteDatabase,
	operation func(sqliteExecutor) error,
) error {
	if req.TxID != "" {
		transaction, err := s.getTransaction(req.DB, req.TxID)
		if err != nil {
			return err
		}
		transaction.touch(s.txTTL)
		transaction.opMu.Lock()
		defer transaction.opMu.Unlock()
		return operation(transaction.tx)
	}
	select {
	case <-database.gate:
		defer func() { database.gate <- struct{}{} }()
	case <-ctx.Done():
		return ctx.Err()
	}
	return operation(database.db)
}

func (s *sqliteHTTPServer) decodeDatabaseRequest(w http.ResponseWriter, r *http.Request) (sqliteRequest, *sqliteDatabase, bool) {
	if r.Method != http.MethodPost {
		writeSQLiteError(w, http.StatusMethodNotAllowed, "method not allowed")
		return sqliteRequest{}, nil, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, sqliteServerMaxBodyBytes)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var req sqliteRequest
	if err := decoder.Decode(&req); err != nil {
		writeSQLiteError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return sqliteRequest{}, nil, false
	}
	database, ok := s.databases[req.DB]
	if !ok {
		writeSQLiteError(w, http.StatusBadRequest, "unknown database alias")
		return sqliteRequest{}, nil, false
	}
	return req, database, true
}

func (s *sqliteHTTPServer) getTransaction(dbAlias, txID string) (*sqliteTransaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	transaction, ok := s.transactions[txID]
	if !ok || transaction.database.name != dbAlias {
		return nil, fmt.Errorf("unknown transaction")
	}
	return transaction, nil
}

func (s *sqliteHTTPServer) takeTransaction(dbAlias, txID string) (*sqliteTransaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	transaction, ok := s.transactions[txID]
	if !ok || transaction.database.name != dbAlias {
		return nil, fmt.Errorf("unknown transaction")
	}
	delete(s.transactions, txID)
	transaction.timer.Stop()
	return transaction, nil
}

func (s *sqliteHTTPServer) expireTransaction(txID string) {
	s.mu.Lock()
	transaction, ok := s.transactions[txID]
	if ok {
		delete(s.transactions, txID)
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	transaction.opMu.Lock()
	defer transaction.opMu.Unlock()
	_ = transaction.tx.Rollback()
	transaction.release()
}

func (transaction *sqliteTransaction) touch(ttl time.Duration) {
	transaction.timer.Reset(ttl)
}

func (transaction *sqliteTransaction) release() {
	transaction.once.Do(func() { transaction.database.gate <- struct{}{} })
}

func (s *sqliteHTTPServer) Close() error {
	s.mu.Lock()
	transactions := make([]*sqliteTransaction, 0, len(s.transactions))
	for id, transaction := range s.transactions {
		delete(s.transactions, id)
		transaction.timer.Stop()
		transactions = append(transactions, transaction)
	}
	s.mu.Unlock()
	for _, transaction := range transactions {
		_ = transaction.tx.Rollback()
		transaction.release()
	}
	var closeErr error
	for _, database := range s.databases {
		if err := database.db.Close(); err != nil {
			closeErr = errors.Join(closeErr, err)
		}
	}
	return closeErr
}

func encodeSQLiteValue(value any) (sqliteWireValue, error) {
	switch typed := value.(type) {
	case nil:
		return sqliteWireValue{Type: "null"}, nil
	case int64:
		return sqliteWireValue{Type: "int", Int: strconv.FormatInt(typed, 10)}, nil
	case float64:
		return sqliteWireValue{Type: "float", Float: typed}, nil
	case bool:
		return sqliteWireValue{Type: "bool", Bool: typed}, nil
	case string:
		return sqliteWireValue{Type: "text", Text: typed}, nil
	case []byte:
		return sqliteWireValue{Type: "bytes", Text: base64.StdEncoding.EncodeToString(typed)}, nil
	case time.Time:
		return sqliteWireValue{Type: "time", Text: typed.Format(time.RFC3339Nano)}, nil
	default:
		return sqliteWireValue{}, fmt.Errorf("unsupported sqlite value type %T", value)
	}
}

func decodeSQLiteValues(values []sqliteWireValue) ([]any, error) {
	decoded := make([]any, len(values))
	for index, value := range values {
		var err error
		decoded[index], err = decodeSQLiteValue(value)
		if err != nil {
			return nil, fmt.Errorf("argument %d: %w", index, err)
		}
	}
	return decoded, nil
}

func decodeSQLiteValue(value sqliteWireValue) (any, error) {
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
		return nil, fmt.Errorf("unsupported value type %q", value.Type)
	}
}

func writeSQLiteResult(w http.ResponseWriter, result sql.Result) {
	response := sqliteResponse{}
	if count, err := result.RowsAffected(); err == nil {
		response.RowsAffected = count
	}
	if id, err := result.LastInsertId(); err == nil {
		response.LastInsertID = id
	}
	writeSQLiteJSON(w, http.StatusOK, response)
}

func writeSQLiteError(w http.ResponseWriter, status int, message string) {
	writeSQLiteJSON(w, status, sqliteResponse{Error: message})
}

func writeSQLiteJSON(w http.ResponseWriter, status int, value sqliteResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func sqliteServerHealthAlive(port int, timeout time.Duration) bool {
	client := http.Client{Timeout: timeout}
	response, err := client.Get("http://127.0.0.1:" + strconv.Itoa(port) + sqliteServerHealthPath)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode >= 200 && response.StatusCode < 300
}

func stopManagedServiceByPIDFile(ctx context.Context, paths RuntimePaths, service, pidFile string) error {
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || pid <= 0 {
		_ = os.Remove(pidFile)
		return nil
	}
	_ = interruptProcess(pid)
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = forceStopManagedProcess(paths, service, pid)
			return ctx.Err()
		case <-deadline.C:
			_ = forceStopManagedProcess(paths, service, pid)
			_ = os.Remove(pidFile)
			return nil
		case <-ticker.C:
			if !processAlive(pid) {
				_ = os.Remove(pidFile)
				return nil
			}
		}
	}
}
