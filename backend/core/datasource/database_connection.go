package datasource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
	"lazymind/core/common/secretcrypto"
	"lazymind/core/store"
)

type DatabaseConnectionRequest struct {
	DisplayName  string            `json:"display_name"`
	Description  string            `json:"description,omitempty"`
	DBType       string            `json:"db_type"`
	Host         string            `json:"host"`
	Port         int               `json:"port"`
	DatabaseName string            `json:"database_name"`
	Username     string            `json:"username"`
	Password     string            `json:"password,omitempty"`
	Options      map[string]string `json:"options,omitempty"`
}

type UpdateDatabaseConnectionRequest struct {
	DisplayName  *string            `json:"display_name,omitempty"`
	Description  *string            `json:"description,omitempty"`
	DBType       *string            `json:"db_type,omitempty"`
	Host         *string            `json:"host,omitempty"`
	Port         *int               `json:"port,omitempty"`
	DatabaseName *string            `json:"database_name,omitempty"`
	Username     *string            `json:"username,omitempty"`
	Password     *string            `json:"password,omitempty"`
	Options      *map[string]string `json:"options,omitempty"`
}

type DatabaseConnectionResponse struct {
	ID             string            `json:"id"`
	DisplayName    string            `json:"display_name"`
	Description    string            `json:"description"`
	DBType         string            `json:"db_type"`
	Host           string            `json:"host"`
	Port           int               `json:"port"`
	DatabaseName   string            `json:"database_name"`
	Username       string            `json:"username"`
	Options        map[string]string `json:"options"`
	IsVerified     bool              `json:"is_verified"`
	LastCheckedAt  *time.Time        `json:"last_checked_at,omitempty"`
	LastCheckError string            `json:"last_check_error,omitempty"`
	CreateTime     time.Time         `json:"create_time"`
	UpdateTime     time.Time         `json:"update_time"`
}

type ListDatabaseConnectionsResponse struct {
	Connections []DatabaseConnectionResponse `json:"connections"`
}

type CheckDatabaseConnectionResponse struct {
	Success    bool     `json:"success"`
	Message    string   `json:"message"`
	TableCount int      `json:"table_count"`
	Tables     []string `json:"tables,omitempty"`
}

type DatabaseConnectionSecretResponse struct {
	DatabaseConnectionResponse
	Password string `json:"password"`
}

func ListDatabaseConnections(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	var rows []orm.ExternalDatabaseConnection
	if err := store.DB().WithContext(r.Context()).
		Where("create_user_id = ? AND deleted_at IS NULL", userID).
		Order("updated_at DESC").
		Find(&rows).Error; err != nil {
		common.ReplyErr(w, "query database connections failed", http.StatusInternalServerError)
		return
	}
	out := make([]DatabaseConnectionResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, databaseConnectionResponse(row))
	}
	common.ReplyOK(w, ListDatabaseConnectionsResponse{Connections: out})
}

func CreateDatabaseConnection(w http.ResponseWriter, r *http.Request) {
	userID := strings.TrimSpace(store.UserID(r))
	userName := strings.TrimSpace(store.UserName(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return
	}
	var req DatabaseConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	row, err := rowFromDatabaseConnectionRequest(req, userID, userName)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := store.DB().WithContext(r.Context()).Select("*").Create(&row).Error; err != nil {
		common.ReplyErr(w, "create database connection failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, databaseConnectionResponse(row))
}

func GetDatabaseConnection(w http.ResponseWriter, r *http.Request) {
	row, ok := ownedDatabaseConnection(w, r)
	if !ok {
		return
	}
	common.ReplyOK(w, databaseConnectionResponse(*row))
}

func GetDatabaseConnectionSecret(w http.ResponseWriter, r *http.Request) {
	if !requireInternalToken(w, r) {
		return
	}
	row, ok := ownedDatabaseConnection(w, r)
	if !ok {
		return
	}
	password, err := decryptDatabasePassword(row.PasswordJSON)
	if err != nil {
		common.ReplyErr(w, "decrypt database password failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, DatabaseConnectionSecretResponse{DatabaseConnectionResponse: databaseConnectionResponse(*row), Password: password})
}

func UpdateDatabaseConnection(w http.ResponseWriter, r *http.Request) {
	row, ok := ownedDatabaseConnection(w, r)
	if !ok {
		return
	}
	var req UpdateDatabaseConnectionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		common.ReplyErr(w, "invalid body", http.StatusBadRequest)
		return
	}
	updates, err := databaseConnectionUpdates(req)
	if err != nil {
		common.ReplyErr(w, err.Error(), http.StatusBadRequest)
		return
	}
	if len(updates) == 0 {
		common.ReplyOK(w, databaseConnectionResponse(*row))
		return
	}
	updates["is_verified"] = false
	updates["updated_at"] = time.Now()
	if err := store.DB().WithContext(r.Context()).Model(&orm.ExternalDatabaseConnection{}).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, strings.TrimSpace(store.UserID(r))).
		Updates(updates).Error; err != nil {
		common.ReplyErr(w, "update database connection failed", http.StatusInternalServerError)
		return
	}
	GetDatabaseConnection(w, r)
}

func DeleteDatabaseConnection(w http.ResponseWriter, r *http.Request) {
	row, ok := ownedDatabaseConnection(w, r)
	if !ok {
		return
	}
	now := time.Now()
	if err := store.DB().WithContext(r.Context()).Model(&orm.ExternalDatabaseConnection{}).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, strings.TrimSpace(store.UserID(r))).
		Updates(map[string]any{"deleted_at": &now, "updated_at": now}).Error; err != nil {
		common.ReplyErr(w, "delete database connection failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, map[string]any{"deleted": true})
}

func CheckDatabaseConnection(w http.ResponseWriter, r *http.Request) {
	row, ok := ownedDatabaseConnection(w, r)
	if !ok {
		return
	}
	result := checkDatabaseConnection(r.Context(), *row)
	now := time.Now()
	updates := map[string]any{"is_verified": result.Success, "last_checked_at": &now, "last_check_error": "", "updated_at": now}
	if !result.Success {
		updates["last_check_error"] = result.Message
	}
	if err := store.DB().WithContext(r.Context()).Model(&orm.ExternalDatabaseConnection{}).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, strings.TrimSpace(store.UserID(r))).
		Updates(updates).Error; err != nil {
		common.ReplyErr(w, "update database connection check result failed", http.StatusInternalServerError)
		return
	}
	common.ReplyOK(w, result)
}

func rowFromDatabaseConnectionRequest(req DatabaseConnectionRequest, userID, userName string) (orm.ExternalDatabaseConnection, error) {
	dbType, host, databaseName, username, port, err := normalizeDatabaseConnectionFields(req.DBType, req.Host, req.DatabaseName, req.Username, req.Port)
	if err != nil {
		return orm.ExternalDatabaseConnection{}, err
	}
	passwordJSON, err := encryptDatabasePassword(req.Password)
	if err != nil {
		return orm.ExternalDatabaseConnection{}, err
	}
	optionsJSON, err := json.Marshal(normalizeOptions(req.Options))
	if err != nil {
		return orm.ExternalDatabaseConnection{}, err
	}
	now := time.Now()
	return orm.ExternalDatabaseConnection{
		ID:           "edb_" + common.GenerateID(),
		DisplayName:  firstNonEmpty(strings.TrimSpace(req.DisplayName), databaseName),
		Description:  strings.TrimSpace(req.Description),
		DBType:       dbType,
		Host:         host,
		Port:         port,
		DatabaseName: databaseName,
		Username:     username,
		PasswordJSON: passwordJSON,
		OptionsJSON:  optionsJSON,
		BaseModel:    orm.BaseModel{CreateUserID: strings.TrimSpace(userID), CreateUserName: strings.TrimSpace(userName), CreatedAt: now, UpdatedAt: now},
	}, nil
}

func databaseConnectionUpdates(req UpdateDatabaseConnectionRequest) (map[string]any, error) {
	updates := map[string]any{}
	if req.DisplayName != nil {
		updates["display_name"] = strings.TrimSpace(*req.DisplayName)
	}
	if req.Description != nil {
		updates["description"] = strings.TrimSpace(*req.Description)
	}
	if req.DBType != nil {
		dbType, _, _, _, _, err := normalizeDatabaseConnectionFields(*req.DBType, "host", "database", "username", defaultDatabasePort(*req.DBType))
		if err != nil {
			return nil, err
		}
		updates["db_type"] = dbType
	}
	if req.Host != nil {
		host := strings.TrimSpace(*req.Host)
		if host == "" {
			return nil, fmt.Errorf("host required")
		}
		updates["host"] = host
	}
	if req.Port != nil {
		if *req.Port <= 0 || *req.Port > 65535 {
			return nil, fmt.Errorf("invalid port")
		}
		updates["port"] = *req.Port
	}
	if req.DatabaseName != nil {
		databaseName := strings.TrimSpace(*req.DatabaseName)
		if databaseName == "" {
			return nil, fmt.Errorf("database_name required")
		}
		updates["database_name"] = databaseName
	}
	if req.Username != nil {
		username := strings.TrimSpace(*req.Username)
		if username == "" {
			return nil, fmt.Errorf("username required")
		}
		updates["username"] = username
	}
	if req.Password != nil {
		passwordJSON, err := encryptDatabasePassword(*req.Password)
		if err != nil {
			return nil, err
		}
		updates["password_json"] = passwordJSON
	}
	if req.Options != nil {
		optionsJSON, err := json.Marshal(normalizeOptions(*req.Options))
		if err != nil {
			return nil, err
		}
		updates["options_json"] = optionsJSON
	}
	return updates, nil
}

func normalizeDatabaseConnectionFields(dbType, host, databaseName, username string, port int) (string, string, string, string, int, error) {
	dbType = strings.ToLower(strings.TrimSpace(dbType))
	if dbType != "mysql" && dbType != "postgresql" && dbType != "postgres" {
		return "", "", "", "", 0, fmt.Errorf("db_type must be mysql or postgresql")
	}
	if dbType == "postgres" {
		dbType = "postgresql"
	}
	host = strings.TrimSpace(host)
	databaseName = strings.TrimSpace(databaseName)
	username = strings.TrimSpace(username)
	if host == "" || databaseName == "" || username == "" {
		return "", "", "", "", 0, fmt.Errorf("host, database_name and username are required")
	}
	if port == 0 {
		port = defaultDatabasePort(dbType)
	}
	if port < 0 || port > 65535 {
		return "", "", "", "", 0, fmt.Errorf("invalid port")
	}
	return dbType, host, databaseName, username, port, nil
}

func defaultDatabasePort(dbType string) int {
	if strings.EqualFold(strings.TrimSpace(dbType), "mysql") {
		return 3306
	}
	return 5432
}

func normalizeOptions(options map[string]string) map[string]string {
	out := map[string]string{}
	for key, value := range options {
		key = strings.TrimSpace(key)
		if key != "" {
			out[key] = strings.TrimSpace(value)
		}
	}
	return out
}

func databaseConnectionResponse(row orm.ExternalDatabaseConnection) DatabaseConnectionResponse {
	options := map[string]string{}
	_ = json.Unmarshal(row.OptionsJSON, &options)
	return DatabaseConnectionResponse{
		ID:             row.ID,
		DisplayName:    row.DisplayName,
		Description:    row.Description,
		DBType:         row.DBType,
		Host:           row.Host,
		Port:           row.Port,
		DatabaseName:   row.DatabaseName,
		Username:       row.Username,
		Options:        options,
		IsVerified:     row.IsVerified,
		LastCheckedAt:  row.LastCheckedAt,
		LastCheckError: row.LastCheckError,
		CreateTime:     row.CreatedAt,
		UpdateTime:     row.UpdatedAt,
	}
}

func ownedDatabaseConnection(w http.ResponseWriter, r *http.Request) (*orm.ExternalDatabaseConnection, bool) {
	userID := strings.TrimSpace(store.UserID(r))
	if userID == "" {
		common.ReplyErr(w, "missing X-User-Id", http.StatusBadRequest)
		return nil, false
	}
	id := strings.TrimSpace(common.PathVar(r, "connection"))
	if id == "" {
		common.ReplyErr(w, "missing connection", http.StatusBadRequest)
		return nil, false
	}
	var row orm.ExternalDatabaseConnection
	err := store.DB().WithContext(r.Context()).Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", id, userID).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		common.ReplyErr(w, "database connection not found", http.StatusNotFound)
		return nil, false
	}
	if err != nil {
		common.ReplyErr(w, "query database connection failed", http.StatusInternalServerError)
		return nil, false
	}
	return &row, true
}

func databaseSecretKey() string {
	if key := strings.TrimSpace(os.Getenv("LAZYMIND_DATABASE_SECRET_KEY")); key != "" {
		return key
	}
	if key := strings.TrimSpace(os.Getenv("LAZYMIND_MCP_SECRET_KEY")); key != "" {
		return key
	}
	return "lazymind-core-mcp-default-secret"
}

func requireInternalToken(w http.ResponseWriter, r *http.Request) bool {
	expected := strings.TrimSpace(os.Getenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN"))
	if expected == "" {
		return true
	}
	if strings.TrimSpace(r.Header.Get("X-LazyMind-Internal-Token")) != expected {
		common.ReplyErr(w, "internal token required", http.StatusUnauthorized)
		return false
	}
	return true
}

func encryptDatabasePassword(password string) (json.RawMessage, error) {
	return secretcrypto.EncodeAESGCM([]byte(password), databaseSecretKey())
}

func decryptDatabasePassword(raw json.RawMessage) (string, error) {
	decoded, ok, err := secretcrypto.DecodeAESGCM(raw, databaseSecretKey())
	if err != nil {
		return "", err
	}
	if ok {
		return string(decoded), nil
	}
	var plain string
	if err := json.Unmarshal(raw, &plain); err == nil {
		return plain, nil
	}
	return "", nil
}

func checkDatabaseConnection(ctx context.Context, row orm.ExternalDatabaseConnection) CheckDatabaseConnectionResponse {
	db, err := openExternalDatabase(row)
	if err != nil {
		return CheckDatabaseConnectionResponse{Success: false, Message: err.Error()}
	}
	sqlDB, err := db.DB()
	if err != nil {
		return CheckDatabaseConnectionResponse{Success: false, Message: err.Error()}
	}
	defer sqlDB.Close()
	if err := sqlDB.PingContext(ctx); err != nil {
		return CheckDatabaseConnectionResponse{Success: false, Message: err.Error()}
	}
	tables, err := db.Migrator().GetTables()
	if err != nil {
		return CheckDatabaseConnectionResponse{Success: true, Message: "连接成功", TableCount: 0}
	}
	sort.Strings(tables)
	if len(tables) > 20 {
		tables = tables[:20]
	}
	return CheckDatabaseConnectionResponse{Success: true, Message: "连接成功", TableCount: len(tables), Tables: tables}
}

func openExternalDatabase(row orm.ExternalDatabaseConnection) (*gorm.DB, error) {
	password, err := decryptDatabasePassword(row.PasswordJSON)
	if err != nil {
		return nil, err
	}
	options := map[string]string{}
	_ = json.Unmarshal(row.OptionsJSON, &options)
	switch strings.ToLower(strings.TrimSpace(row.DBType)) {
	case "mysql":
		dsn := mysqlDSN(row, password, options)
		return gorm.Open(gormmysql.Open(dsn), &gorm.Config{})
	case "postgresql", "postgres":
		dsn := postgresDSN(row, password, options)
		return gorm.Open(postgres.Open(dsn), &gorm.Config{})
	default:
		return nil, fmt.Errorf("unsupported db_type: %s", row.DBType)
	}
}

func mysqlDSN(row orm.ExternalDatabaseConnection, password string, options map[string]string) string {
	params := map[string]string{"parseTime": firstNonEmpty(options["parseTime"], "true")}
	for key, value := range options {
		if key != "parseTime" && key != "" {
			params[key] = value
		}
	}
	return (&mysqldriver.Config{
		User:                 row.Username,
		Passwd:               password,
		Net:                  "tcp",
		Addr:                 fmt.Sprintf("%s:%d", row.Host, row.Port),
		DBName:               row.DatabaseName,
		Params:               params,
		AllowNativePasswords: true,
	}).FormatDSN()
}

func postgresDSN(row orm.ExternalDatabaseConnection, password string, options map[string]string) string {
	values := url.Values{}
	values.Set("sslmode", firstNonEmpty(options["sslmode"], "disable"))
	for key, value := range options {
		if key != "sslmode" && key != "" {
			values.Set(key, value)
		}
	}
	u := url.URL{Scheme: "postgres", User: url.UserPassword(row.Username, password), Host: row.Host + ":" + strconv.Itoa(row.Port), Path: row.DatabaseName, RawQuery: values.Encode()}
	return u.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
