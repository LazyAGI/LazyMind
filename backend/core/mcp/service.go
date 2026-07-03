package mcp

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common"
	"lazymind/core/common/orm"
)

var (
	errBadRequest = errors.New("bad request")
	errForbidden  = errors.New("forbidden")
	errNotFound   = errors.New("not found")
)

func newServerID() string {
	return "msp_" + common.GenerateID()
}

func newToolID() string {
	return "mst_" + common.GenerateID()
}

func ListServers(ctx context.Context, db *gorm.DB, userID string, req ListServersRequest) (*ListServersResponse, error) {
	if db == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	userID = strings.TrimSpace(userID)
	req = normalizeListServersRequest(req)
	var rows []orm.MCPServer
	query := applyListServersKeyword(visibleServerQuery(db.WithContext(ctx), userID), req.Keyword)
	if err := query.Order("enabled DESC, created_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}

	counts, err := toolCounts(ctx, db, serverIDs(rows))
	if err != nil {
		return nil, err
	}

	out := make([]ServerResponse, 0, len(rows))
	for i := range rows {
		out = append(out, serverResponse(rows[i], counts[rows[i].ID], nil))
	}
	return &ListServersResponse{MCPServers: out, Total: int64(len(out)), Page: 1, PageSize: len(out)}, nil
}

func CreateServer(ctx context.Context, db *gorm.DB, req CreateServerRequest, userID, userName string) (*ServerResponse, error) {
	if db == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	name, transport, serverURL, timeout, allowed, err := normalizeCreate(req)
	if err != nil {
		return nil, err
	}
	headersJSON, err := headersJSONFromAPIKey(req.APIKey)
	if err != nil {
		return nil, err
	}
	allowedJSON, err := json.Marshal(allowed)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	row := orm.MCPServer{
		ID:               newServerID(),
		Name:             name,
		Transport:        transport,
		URL:              serverURL,
		HeadersJSON:      headersJSON,
		AllowedToolsJSON: allowedJSON,
		Enabled:          false,
		Timeout:          timeout,
		BaseModel: orm.BaseModel{
			CreateUserID:   strings.TrimSpace(userID),
			CreateUserName: strings.TrimSpace(userName),
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	if err := db.WithContext(ctx).Select("*").Create(&row).Error; err != nil {
		return nil, err
	}
	resp := serverResponse(row, 0, nil)
	return &resp, nil
}

func GetServer(ctx context.Context, db *gorm.DB, userID, id string) (*ServerResponse, error) {
	row, err := getVisibleServer(ctx, db, userID, id)
	if err != nil {
		return nil, err
	}
	tools, err := listToolsForServer(ctx, db, row.ID)
	if err != nil {
		return nil, err
	}
	resp := serverResponse(*row, int64(len(tools)), tools)
	return &resp, nil
}

func UpdateServer(ctx context.Context, db *gorm.DB, userID, id string, req UpdateServerRequest) (*ServerResponse, error) {
	row, err := getOwnedServer(ctx, db, userID, id)
	if err != nil {
		return nil, err
	}
	updates := map[string]any{"updated_at": time.Now()}
	if req.Name != nil {
		name := strings.TrimSpace(*req.Name)
		if name == "" || len([]rune(name)) > 255 {
			return nil, fmt.Errorf("%w: invalid name", errBadRequest)
		}
		updates["name"] = name
	}
	if req.URL != nil {
		serverURL, err := validateHTTPURL(*req.URL)
		if err != nil {
			return nil, err
		}
		updates["url"] = serverURL
	}
	if req.APIKey != nil {
		headersJSON, err := headersJSONFromAPIKey(*req.APIKey)
		if err != nil {
			return nil, err
		}
		updates["headers_json"] = headersJSON
	}
	if req.AllowedTools != nil {
		allowedJSON, err := json.Marshal(normalizeStringList(req.AllowedTools))
		if err != nil {
			return nil, err
		}
		updates["allowed_tools_json"] = allowedJSON
	}
	if req.Enabled != nil {
		if *req.Enabled && !row.IsVerified {
			return nil, fmt.Errorf("%w: mcp server must be verified before enabling", errBadRequest)
		}
		updates["enabled"] = *req.Enabled
	}
	if req.Timeout != nil {
		if *req.Timeout <= 0 {
			return nil, fmt.Errorf("%w: timeout must be positive", errBadRequest)
		}
		updates["timeout"] = *req.Timeout
	}
	if err := db.WithContext(ctx).Model(&orm.MCPServer{}).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, strings.TrimSpace(userID)).
		Updates(updates).Error; err != nil {
		return nil, err
	}
	return GetServer(ctx, db, userID, row.ID)
}

func DeleteServer(ctx context.Context, db *gorm.DB, userID, id string) error {
	row, err := getOwnedServer(ctx, db, userID, id)
	if err != nil {
		return err
	}
	now := time.Now()
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&orm.MCPServer{}).
			Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, strings.TrimSpace(userID)).
			Updates(map[string]any{"deleted_at": &now, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&orm.MCPServerTool{}).
			Where("mcp_server_id = ? AND deleted_at IS NULL", row.ID).
			Updates(map[string]any{"deleted_at": &now, "updated_at": now}).Error
	})
}

func CheckServer(ctx context.Context, db *gorm.DB, userID, id string) (*CheckResponse, error) {
	row, err := getOwnedServer(ctx, db, userID, id)
	if err != nil {
		return nil, err
	}
	tools, err := listRemoteTools(ctx, *row)
	if err != nil {
		return &CheckResponse{Success: false, Message: sanitizeError(err.Error(), row.HeadersJSON), ToolCount: 0}, nil
	}
	now := time.Now()
	if err := db.WithContext(ctx).Model(&orm.MCPServer{}).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, strings.TrimSpace(userID)).
		Updates(map[string]any{"is_verified": true, "updated_at": now}).Error; err != nil {
		return nil, err
	}
	return &CheckResponse{Success: true, Message: "连接成功", ToolCount: len(tools)}, nil
}

func DiscoverServer(ctx context.Context, db *gorm.DB, userID, id string) (*DiscoverResponse, error) {
	row, err := getOwnedServer(ctx, db, userID, id)
	if err != nil {
		return nil, err
	}
	tools, err := listRemoteTools(ctx, *row)
	if err != nil {
		return &DiscoverResponse{Success: false, Tools: nil}, err
	}
	responses, err := replaceDiscoveredTools(ctx, db, row.ID, tools)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if err := db.WithContext(ctx).Model(&orm.MCPServer{}).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, strings.TrimSpace(userID)).
		Updates(map[string]any{"is_verified": true, "updated_at": now}).Error; err != nil {
		return nil, err
	}
	return &DiscoverResponse{Success: true, Tools: responses}, nil
}

func UpdateServerTools(ctx context.Context, db *gorm.DB, userID, id string, req UpdateToolsRequest) (*ServerResponse, error) {
	row, err := getOwnedServer(ctx, db, userID, id)
	if err != nil {
		return nil, err
	}
	allowedJSON, err := json.Marshal(normalizeStringList(req.AllowedTools))
	if err != nil {
		return nil, err
	}
	if err := db.WithContext(ctx).Model(&orm.MCPServer{}).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", row.ID, strings.TrimSpace(userID)).
		Updates(map[string]any{"allowed_tools_json": allowedJSON, "updated_at": time.Now()}).Error; err != nil {
		return nil, err
	}
	return GetServer(ctx, db, userID, row.ID)
}

func LoadRuntimeConfig(ctx context.Context, db *gorm.DB, userID string) ([]RuntimeConfig, error) {
	if db == nil {
		return nil, nil
	}
	userID = strings.TrimSpace(userID)
	var rows []orm.MCPServer
	q := db.WithContext(ctx).Where("enabled = ? AND deleted_at IS NULL AND transport IN ?", true, []string{transportSSE, transportHTTP})
	if userID == "" {
		q = q.Where("share = ?", true)
	} else {
		q = q.Where("(create_user_id = ? OR share = ?)", userID, true)
	}
	if err := q.Order("share ASC, updated_at DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]RuntimeConfig, 0, len(rows))
	for _, row := range dedupeServers(rows) {
		headers, err := decodeHeaders(row.HeadersJSON)
		if err != nil {
			return nil, err
		}
		out = append(out, RuntimeConfig{
			ID:           row.ID,
			Name:         row.Name,
			Transport:    row.Transport,
			URL:          row.URL,
			Headers:      headers,
			AllowedTools: parseStringJSON(row.AllowedToolsJSON),
			Timeout:      normalizedTimeout(row.Timeout),
		})
	}
	return out, nil
}

func normalizeCreate(req CreateServerRequest) (string, string, string, int, []string, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" || len([]rune(name)) > 255 {
		return "", "", "", 0, nil, fmt.Errorf("%w: invalid name", errBadRequest)
	}
	transport := strings.ToLower(strings.TrimSpace(req.Transport))
	if transport != transportSSE && transport != transportHTTP {
		return "", "", "", 0, nil, fmt.Errorf("%w: transport must be sse or http", errBadRequest)
	}
	serverURL, err := validateHTTPURL(req.URL)
	if err != nil {
		return "", "", "", 0, nil, err
	}
	timeout := req.Timeout
	if timeout == 0 {
		timeout = defaultTimeoutSeconds
	}
	if timeout < 0 {
		return "", "", "", 0, nil, fmt.Errorf("%w: timeout must be positive", errBadRequest)
	}
	return name, transport, serverURL, timeout, normalizeStringList(req.AllowedTools), nil
}

func validateHTTPURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Host == "" {
		return "", fmt.Errorf("%w: invalid url", errBadRequest)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("%w: url must be http or https", errBadRequest)
	}
	return parsed.String(), nil
}

func normalizeStringList(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func headersJSONFromAPIKey(apiKey string) (json.RawMessage, error) {
	apiKey = strings.TrimSpace(apiKey)
	headers := map[string]any{}
	if apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
	}
	return encodeHeaders(headers)
}

func encodeHeaders(headers map[string]any) (json.RawMessage, error) {
	raw, err := json.Marshal(headers)
	if err != nil {
		return nil, err
	}
	nonce, ciphertext, err := encryptHeaderBytes(raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"enc":   "aes-gcm",
		"nonce": base64.StdEncoding.EncodeToString(nonce),
		"v":     base64.StdEncoding.EncodeToString(ciphertext),
	})
}

func decodeHeaders(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var wrapper struct {
		Enc   string `json:"enc"`
		Nonce string `json:"nonce"`
		V     string `json:"v"`
	}
	if json.Unmarshal(raw, &wrapper) == nil && wrapper.Enc == "aes-gcm" && wrapper.V != "" {
		nonce, err := base64.StdEncoding.DecodeString(wrapper.Nonce)
		if err != nil {
			return nil, err
		}
		ciphertext, err := base64.StdEncoding.DecodeString(wrapper.V)
		if err != nil {
			return nil, err
		}
		raw, err = decryptHeaderBytes(nonce, ciphertext)
		if err != nil {
			return nil, err
		}
	} else if json.Unmarshal(raw, &wrapper) == nil && wrapper.Enc == "base64" && wrapper.V != "" {
		decoded, err := base64.StdEncoding.DecodeString(wrapper.V)
		if err != nil {
			return nil, err
		}
		raw = decoded
	}
	var headers map[string]any
	if err := json.Unmarshal(raw, &headers); err != nil {
		return nil, err
	}
	if headers == nil {
		headers = map[string]any{}
	}
	return headers, nil
}

func encryptionKey() string {
	if key := strings.TrimSpace(os.Getenv("LAZYMIND_MCP_SECRET_KEY")); key != "" {
		return key
	}
	return "lazymind-core-mcp-default-secret"
}

func headerAEAD() (cipher.AEAD, error) {
	sum := sha256.Sum256([]byte(encryptionKey()))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func encryptHeaderBytes(plaintext []byte) ([]byte, []byte, error) {
	aead, err := headerAEAD()
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, err
	}
	return nonce, aead.Seal(nil, nonce, plaintext, nil), nil
}

func decryptHeaderBytes(nonce, ciphertext []byte) ([]byte, error) {
	aead, err := headerAEAD()
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, nonce, ciphertext, nil)
}

func apiKeyPreview(raw json.RawMessage) string {
	headers, err := decodeHeaders(raw)
	if err != nil {
		return ""
	}
	auth, _ := headers["Authorization"].(string)
	const prefix = "Bearer "
	if strings.HasPrefix(auth, prefix) {
		return maskAPIKey(strings.TrimSpace(strings.TrimPrefix(auth, prefix)))
	}
	return ""
}

func maskAPIKey(apiKey string) string {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return ""
	}
	if strings.HasPrefix(apiKey, "sk-") && len([]rune(apiKey)) > 6 {
		runes := []rune(apiKey)
		return "sk-***" + string(runes[len(runes)-3:])
	}
	runes := []rune(apiKey)
	if len(runes) <= 6 {
		return string(runes[:1]) + "-***"
	}
	if len(runes) <= 10 {
		return string(runes[:2]) + "***" + string(runes[len(runes)-2:])
	}
	return string(runes[:3]) + "-***" + string(runes[len(runes)-3:])
}

func parseStringJSON(raw json.RawMessage) []string {
	var values []string
	if len(raw) == 0 || json.Unmarshal(raw, &values) != nil {
		return []string{}
	}
	return normalizeStringList(values)
}

func normalizedTimeout(timeout int) int {
	if timeout <= 0 {
		return defaultTimeoutSeconds
	}
	return timeout
}

func serverResponse(row orm.MCPServer, toolCount int64, tools []ToolResponse) ServerResponse {
	return ServerResponse{
		ID:            row.ID,
		Name:          row.Name,
		Transport:     row.Transport,
		URL:           row.URL,
		APIKeyPreview: apiKeyPreview(row.HeadersJSON),
		AllowedTools:  parseStringJSON(row.AllowedToolsJSON),
		Enabled:       row.Enabled,
		IsVerified:    row.IsVerified,
		Share:         row.Share,
		Timeout:       normalizedTimeout(row.Timeout),
		ToolCount:     toolCount,
		Tools:         tools,
		CreateTime:    row.CreatedAt,
		UpdateTime:    row.UpdatedAt,
	}
}

func visibleServerQuery(q *gorm.DB, userID string) *gorm.DB {
	q = q.Model(&orm.MCPServer{}).Where("deleted_at IS NULL")
	if strings.TrimSpace(userID) == "" {
		return q.Where("share = ? AND enabled = ?", true, true)
	}
	return q.Where("(create_user_id = ? OR (share = ? AND enabled = ?))", userID, true, true)
}

func normalizeListServersRequest(req ListServersRequest) ListServersRequest {
	req.Keyword = strings.TrimSpace(req.Keyword)
	if req.Page <= 0 {
		req.Page = 1
	}
	if req.PageSize < 0 {
		req.PageSize = 0
	}
	return req
}

func applyListServersKeyword(q *gorm.DB, keyword string) *gorm.DB {
	keyword = strings.ToLower(strings.TrimSpace(keyword))
	if keyword == "" {
		return q
	}
	pattern := "%" + escapeListServersLikePattern(keyword) + "%"
	return q.Where(
		"(LOWER(name) LIKE ? ESCAPE '!' OR LOWER(url) LIKE ? ESCAPE '!' OR LOWER(transport) LIKE ? ESCAPE '!')",
		pattern,
		pattern,
		pattern,
	)
}

func escapeListServersLikePattern(keyword string) string {
	var b strings.Builder
	for _, r := range keyword {
		if r == '!' || r == '%' || r == '_' {
			b.WriteRune('!')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func getVisibleServer(ctx context.Context, db *gorm.DB, userID, id string) (*orm.MCPServer, error) {
	if db == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	var row orm.MCPServer
	err := visibleServerQuery(db.WithContext(ctx), strings.TrimSpace(userID)).
		Where("id = ?", strings.TrimSpace(id)).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func getOwnedServer(ctx context.Context, db *gorm.DB, userID, id string) (*orm.MCPServer, error) {
	if db == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("%w: missing user", errBadRequest)
	}
	var row orm.MCPServer
	err := db.WithContext(ctx).
		Where("id = ? AND create_user_id = ? AND deleted_at IS NULL", strings.TrimSpace(id), userID).
		Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func serverIDs(rows []orm.MCPServer) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func toolCounts(ctx context.Context, db *gorm.DB, ids []string) (map[string]int64, error) {
	out := make(map[string]int64, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []struct {
		MCPServerID string `gorm:"column:mcp_server_id"`
		Count       int64  `gorm:"column:count"`
	}
	if err := db.WithContext(ctx).Model(&orm.MCPServerTool{}).
		Select("mcp_server_id, count(*) AS count").
		Where("mcp_server_id IN ? AND deleted_at IS NULL", ids).
		Group("mcp_server_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.MCPServerID] = row.Count
	}
	return out, nil
}

func listToolsForServer(ctx context.Context, db *gorm.DB, serverID string) ([]ToolResponse, error) {
	var rows []orm.MCPServerTool
	if err := db.WithContext(ctx).
		Where("mcp_server_id = ? AND deleted_at IS NULL", serverID).
		Order("tool_name ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]ToolResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, toolResponse(row))
	}
	return out, nil
}

func toolResponse(row orm.MCPServerTool) ToolResponse {
	schema := row.InputSchemaJSON
	if len(schema) == 0 {
		schema = json.RawMessage(`{}`)
	}
	return ToolResponse{
		ID:          row.ID,
		ToolName:    row.ToolName,
		Description: row.Description,
		InputSchema: schema,
	}
}

func replaceDiscoveredTools(ctx context.Context, db *gorm.DB, serverID string, tools []discoveredTool) ([]ToolResponse, error) {
	now := time.Now()
	responses := make([]ToolResponse, 0, len(tools))
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing []orm.MCPServerTool
		if err := tx.Where("mcp_server_id = ?", serverID).Find(&existing).Error; err != nil {
			return err
		}
		byName := map[string]orm.MCPServerTool{}
		for _, row := range existing {
			byName[row.ToolName] = row
		}
		seen := map[string]struct{}{}
		for _, tool := range tools {
			if strings.TrimSpace(tool.Name) == "" {
				continue
			}
			schema := tool.InputSchema
			if len(schema) == 0 {
				schema = json.RawMessage(`{}`)
			}
			seen[tool.Name] = struct{}{}
			row, exists := byName[tool.Name]
			if exists {
				updates := map[string]any{
					"description":        tool.Description,
					"input_schema_json":  schema,
					"last_discovered_at": now,
					"updated_at":         now,
					"deleted_at":         nil,
				}
				if err := tx.Model(&orm.MCPServerTool{}).Where("id = ?", row.ID).Updates(updates).Error; err != nil {
					return err
				}
				row.Description = tool.Description
				row.InputSchemaJSON = schema
				row.LastDiscoveredAt = now
				row.UpdatedAt = now
				row.DeletedAt = nil
				responses = append(responses, toolResponse(row))
				continue
			}
			row = orm.MCPServerTool{
				ID:               newToolID(),
				MCPServerID:      serverID,
				ToolName:         tool.Name,
				Description:      tool.Description,
				InputSchemaJSON:  schema,
				LastDiscoveredAt: now,
				CreatedAt:        now,
				UpdatedAt:        now,
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			responses = append(responses, toolResponse(row))
		}
		for _, row := range existing {
			if _, ok := seen[row.ToolName]; ok || row.DeletedAt != nil {
				continue
			}
			if err := tx.Model(&orm.MCPServerTool{}).
				Where("id = ?", row.ID).
				Updates(map[string]any{"deleted_at": &now, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(responses, func(i, j int) bool {
		return responses[i].ToolName < responses[j].ToolName
	})
	return responses, nil
}

func dedupeServers(rows []orm.MCPServer) []orm.MCPServer {
	out := make([]orm.MCPServer, 0, len(rows))
	seen := map[string]struct{}{}
	for _, row := range rows {
		if _, ok := seen[row.ID]; ok {
			continue
		}
		seen[row.ID] = struct{}{}
		out = append(out, row)
	}
	return out
}

func sanitizeError(message string, rawHeaders json.RawMessage) string {
	headers, err := decodeHeaders(rawHeaders)
	if err != nil {
		return message
	}
	for _, value := range headers {
		s, _ := value.(string)
		if strings.TrimSpace(s) != "" {
			message = strings.ReplaceAll(message, s, "[credential]")
		}
	}
	return message
}
