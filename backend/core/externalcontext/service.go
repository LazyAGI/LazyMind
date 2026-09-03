// Package externalcontext owns the correspondence between provider-native
// Agent threads and LazyMind conversations, including the normalized
// user/assistant transcript projection imported from the native Agent store.
package externalcontext

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"lazymind/core/common/orm"
	"lazymind/core/doc"
	"lazymind/core/settings"
)

var (
	codexRequestMarker = regexp.MustCompile(`(?im)^#+\s*My request for Codex:\s*$`)
	codexImageTag      = regexp.MustCompile(`(?i)<image\b[^>]*\bpath="([^"]+)"[^>]*>`)
	codexAnyImageTag   = regexp.MustCompile(`(?i)</?image\b[^>]*>`)
)

func normalizeCodexUserMessage(owner, historyID, raw string, transferred []NativeImage) (string, []map[string]any) {
	const maxImportedImageBytes = 25 << 20
	marker := codexRequestMarker.FindStringIndex(raw)
	if marker == nil {
		return raw, nil
	}
	request := strings.TrimSpace(raw[marker[1]:])
	inputs := []map[string]any{{"input_type": "text", "text": strings.TrimSpace(codexAnyImageTag.ReplaceAllString(request, ""))}}
	attachmentRoot := filepath.Join(doc.UploadRoot(), "external-agent-attachments", deterministicID("owner", owner), historyID)
	transferredByName := make(map[string]string, len(transferred))
	for _, image := range transferred {
		if name := filepath.Base(strings.TrimSpace(image.Name)); name != "." && name != "" {
			transferredByName[name] = image.Base64
		}
	}
	for _, match := range codexImageTag.FindAllStringSubmatch(request, -1) {
		if len(match) < 2 {
			continue
		}
		sourcePath := filepath.Clean(strings.TrimSpace(match[1]))
		switch strings.ToLower(filepath.Ext(sourcePath)) {
		case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp":
		default:
			continue
		}
		filename := filepath.Base(sourcePath)
		var source io.Reader
		closeSource := func() {}
		if encoded := transferredByName[filename]; encoded != "" {
			content, decodeErr := base64.StdEncoding.DecodeString(encoded)
			if decodeErr != nil || len(content) > maxImportedImageBytes {
				continue
			}
			source = bytes.NewReader(content)
		} else {
			local, openErr := os.Open(sourcePath)
			if openErr != nil {
				continue
			}
			info, statErr := local.Stat()
			if statErr != nil || !info.Mode().IsRegular() || info.Size() > maxImportedImageBytes {
				local.Close()
				continue
			}
			source = local
			closeSource = func() { _ = local.Close() }
		}
		if mkdirErr := os.MkdirAll(attachmentRoot, 0o700); mkdirErr != nil {
			closeSource()
			continue
		}
		targetPath := filepath.Join(attachmentRoot, filename)
		target, createErr := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if createErr == nil {
			_, createErr = io.Copy(target, source)
			target.Close()
		}
		closeSource()
		if createErr != nil {
			continue
		}
		if reference := doc.StaticFileReferenceFromAnyStoragePath(targetPath); reference != "" {
			inputs = append(inputs, map[string]any{
				"input_type": "image", "uri": reference, "file_id": filename,
			})
		}
	}
	return inputs[0]["text"].(string), inputs
}

var (
	ErrInvalidSource = errors.New("invalid external Agent source")
	ErrThreadOwned   = errors.New("external Agent thread belongs to another user or conversation")
)

// Source is the provider-neutral thread/turn identity extracted by an outer
// Agent adapter. Provider is never inferred from user-supplied tool arguments.
type Source struct {
	Provider     string `json:"provider"`
	HostID       string `json:"host_id"`
	ThreadID     string `json:"thread_id"`
	TurnID       string `json:"turn_id,omitempty"`
	ThreadSource string `json:"thread_source,omitempty"`
	ProjectKey   string `json:"project_key,omitempty"`
	ProjectName  string `json:"project_name,omitempty"`
	Message      string `json:"message,omitempty"`
}

// NativeSession is the provider-owned catalog and normalized transcript for
// one native Agent session.
type NativeSession struct {
	ThreadID      string       `json:"thread_id"`
	ProjectKey    string       `json:"project_key"`
	ProjectName   string       `json:"project_name"`
	DisplayName   string       `json:"display_name"`
	NativeUpdated time.Time    `json:"native_updated_at"`
	TurnCount     int          `json:"turn_count"`
	Turns         []NativeTurn `json:"turns,omitempty"`
}

type NativeTurn struct {
	ID        string        `json:"turn_id"`
	User      string        `json:"user"`
	Assistant string        `json:"assistant"`
	CreatedAt time.Time     `json:"created_at"`
	Managed   bool          `json:"managed,omitempty"`
	Images    []NativeImage `json:"images,omitempty"`
}

type NativeImage struct {
	Name   string `json:"name"`
	Base64 string `json:"base64"`
}

// Link is the resolved LazyMind authority for one provider-native turn.
type Link struct {
	ConversationID string `json:"conversation_id"`
	ExternalRef    string `json:"external_ref"`
	HistoryID      string `json:"history_id"`
}

type SessionBinding struct {
	ConversationID string `json:"conversation_id"`
}

type NativeSessionSummary struct {
	HostID           string `json:"host_id"`
	ProviderThreadID string `json:"provider_thread_id"`
	ConversationID   string `json:"conversation_id"`
	Bound            bool   `json:"bound"`
	DisplayName      string `json:"display_name"`
	ProjectKey       string `json:"project_key"`
	ProjectName      string `json:"project_name"`
	TurnCount        int    `json:"turn_count"`
	UpdateTime       string `json:"update_time"`
}

type NativeSessionPage struct {
	Items []NativeSessionSummary
	Total int64
}

type Service struct {
	db  *gorm.DB
	now func() time.Time
}

func New(db *gorm.DB) *Service {
	return &Service{db: db, now: func() time.Time { return time.Now().UTC() }}
}

// ResolveInvocation binds an observed external thread and returns the context
// inherited by MCP tools. MCP activity is recorded only in AgentInvocation; it
// never creates a synthetic user message or a second transcript.
func (s *Service) ResolveInvocation(
	ctx context.Context,
	owner, invocationID string,
	source Source,
) (Link, error) {
	owner, invocationID = strings.TrimSpace(owner), strings.TrimSpace(invocationID)
	source = normalizedSource(source, invocationID)
	if s == nil || s.db == nil || !validIdentity(owner, 255) || !validIdentity(invocationID, 80) || !validSource(source) {
		return Link{}, ErrInvalidSource
	}

	binding, err := s.resolveBinding(ctx, owner, source)
	if err != nil {
		return Link{}, err
	}
	if err := s.ensureConversation(ctx, owner, binding, source); err != nil {
		return Link{}, err
	}
	link := Link{ConversationID: binding.ConversationID}
	var run orm.ExternalChatRun
	err = s.db.WithContext(ctx).
		Where("conversation_id = ? AND actor_user_id = ? AND provider = ? AND host_id = ? AND provider_thread_id = ? AND status = ?",
			binding.ConversationID, owner, source.Provider, source.HostID, source.ThreadID, "running").
		Order("created_at DESC").Take(&run).Error
	if err == nil {
		link.ExternalRef, link.HistoryID = run.ID, run.HistoryID
		return link, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return Link{}, err
	}
	return link, nil
}

// BindManagedThread records the same correspondence when LazyMind launched the
// provider turn and learned the native thread ID from a thread_started event.
func (s *Service) BindManagedThread(
	ctx context.Context,
	owner, provider, hostID, threadID, conversationID string,
) error {
	source := normalizedSource(Source{Provider: provider, HostID: hostID, ThreadID: threadID}, "")
	owner, conversationID = strings.TrimSpace(owner), strings.TrimSpace(conversationID)
	if s == nil || s.db == nil || !validIdentity(owner, 255) || !validIdentity(conversationID, 36) ||
		!validProvider(source.Provider) || !validIdentity(source.HostID, 128) || !validIdentity(source.ThreadID, 128) {
		return ErrInvalidSource
	}
	var conversation orm.Conversation
	if err := s.db.WithContext(ctx).Where("id = ? AND create_user_id = ?", conversationID, owner).Take(&conversation).Error; err != nil {
		return err
	}

	var existing orm.ExternalAgentBinding
	err := s.db.WithContext(ctx).
		Where("provider = ? AND host_id = ? AND provider_thread_id = ?", source.Provider, source.HostID, source.ThreadID).
		Take(&existing).Error
	if err == nil {
		if existing.CreatedByUserID != owner || existing.ConversationID != conversationID {
			return ErrThreadOwned
		}
		// The binding origin is immutable. Continuing an externally-originated
		// thread through LazyMind makes this turn managed, not the thread itself.
		// Reclassifying the binding here would move the conversation from its
		// provider's assistant list into LazyMind after the first continuation.
		return s.db.WithContext(ctx).Model(&orm.ExternalAgentBinding{}).Where("id = ?", existing.ID).
			Update("updated_at", s.now()).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	err = s.db.WithContext(ctx).
		Where("conversation_id = ? AND provider = ?", conversationID, source.Provider).
		Take(&existing).Error
	if err == nil {
		return ErrThreadOwned
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	now := s.now()
	binding := orm.ExternalAgentBinding{
		ID:             deterministicID("binding", source.Provider+"\x00"+source.HostID+"\x00"+source.ThreadID),
		ConversationID: conversationID, Provider: source.Provider, ProviderThreadID: source.ThreadID,
		HostID: source.HostID, ManagedByLazyMind: true,
		CreatedByUserID: owner, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&binding).Error; err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).
		Where("provider = ? AND host_id = ? AND provider_thread_id = ?", source.Provider, source.HostID, source.ThreadID).
		Take(&existing).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		// A concurrent first turn may have won the per-provider binding with
		// another thread between the pre-check and insert.
		if conflictErr := s.db.WithContext(ctx).
			Where("conversation_id = ? AND provider = ?", conversationID, source.Provider).
			Take(&existing).Error; conflictErr == nil {
			return ErrThreadOwned
		} else if !errors.Is(conflictErr, gorm.ErrRecordNotFound) {
			return conflictErr
		}
		return err
	}
	if existing.CreatedByUserID != owner || existing.ConversationID != conversationID {
		return ErrThreadOwned
	}
	return nil
}

func (s *Service) BindNativeSession(ctx context.Context, owner, provider, hostID, threadID string) (SessionBinding, error) {
	owner = strings.TrimSpace(owner)
	provider = strings.ToLower(strings.TrimSpace(provider))
	hostID = strings.TrimSpace(hostID)
	threadID = strings.TrimSpace(threadID)
	if s == nil || s.db == nil || !validIdentity(owner, 255) || !validProvider(provider) ||
		!validIdentity(hostID, 128) || !validIdentity(threadID, 128) {
		return SessionBinding{}, ErrInvalidSource
	}
	var result SessionBinding
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var session orm.ExternalAgentSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("owner_user_id = ? AND provider = ? AND host_id = ? AND provider_thread_id = ? AND active = ?", owner, provider, hostID, threadID, true).
			Take(&session).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrInvalidSource
			}
			return err
		}
		service := New(tx)
		service.now = s.now
		source := normalizedSource(Source{
			Provider: provider, HostID: hostID, ThreadID: threadID,
			ProjectKey: session.ProjectKey, ProjectName: session.ProjectName,
			Message: session.DisplayName,
		}, "")
		binding, err := service.resolveBinding(ctx, owner, source)
		if err != nil {
			return err
		}
		if err := service.ensureConversation(ctx, owner, binding, source); err != nil {
			return err
		}
		result = SessionBinding{ConversationID: binding.ConversationID}
		return nil
	})
	return result, err
}

func (s *Service) ListNativeSessions(
	ctx context.Context,
	owner, provider string,
	offset, limit int,
) (NativeSessionPage, error) {
	owner = strings.TrimSpace(owner)
	provider = strings.ToLower(strings.TrimSpace(provider))
	if s == nil || s.db == nil || !validIdentity(owner, 255) ||
		!validProvider(provider) || offset < 0 || limit < 1 || limit > 100 {
		return NativeSessionPage{}, ErrInvalidSource
	}
	type catalogRow struct {
		HostID           string         `gorm:"column:host_id"`
		ProviderThreadID string         `gorm:"column:provider_thread_id"`
		ProjectKey       string         `gorm:"column:project_key"`
		ProjectName      string         `gorm:"column:project_name"`
		DisplayName      string         `gorm:"column:display_name"`
		TurnCount        int            `gorm:"column:turn_count"`
		NativeUpdatedAt  *time.Time     `gorm:"column:native_updated_at"`
		BindingID        sql.NullString `gorm:"column:binding_id"`
		ConversationID   sql.NullString `gorm:"column:conversation_id"`
	}
	query := s.db.WithContext(ctx).Table("external_agent_sessions AS sessions").
		Select("sessions.host_id, sessions.provider_thread_id, sessions.project_key, sessions.project_name, sessions.display_name, sessions.turn_count, sessions.native_updated_at, bindings.id AS binding_id, conversations.id AS conversation_id").
		Joins("LEFT JOIN external_agent_bindings AS bindings ON bindings.created_by_user_id = sessions.owner_user_id AND bindings.provider = sessions.provider AND bindings.host_id = sessions.host_id AND bindings.provider_thread_id = sessions.provider_thread_id").
		Joins("LEFT JOIN conversations ON conversations.id = bindings.conversation_id AND conversations.create_user_id = sessions.owner_user_id AND conversations.deleted_at IS NULL AND conversations.archived_at IS NULL").
		Where("sessions.owner_user_id = ? AND sessions.provider = ? AND sessions.active = ?", owner, provider, true)
	var page NativeSessionPage
	if err := query.Count(&page.Total).Error; err != nil {
		return NativeSessionPage{}, err
	}
	var rows []catalogRow
	if err := query.Order("sessions.native_updated_at DESC, sessions.provider_thread_id ASC").
		Offset(offset).Limit(limit).Scan(&rows).Error; err != nil {
		return NativeSessionPage{}, err
	}
	page.Items = make([]NativeSessionSummary, 0, len(rows))
	for _, row := range rows {
		updatedAt := ""
		if row.NativeUpdatedAt != nil {
			updatedAt = row.NativeUpdatedAt.UTC().Format(time.RFC3339)
		}
		page.Items = append(page.Items, NativeSessionSummary{
			HostID:           row.HostID,
			ProviderThreadID: row.ProviderThreadID,
			ConversationID:   row.ConversationID.String,
			Bound:            row.BindingID.Valid,
			DisplayName:      row.DisplayName,
			ProjectKey:       row.ProjectKey,
			ProjectName:      row.ProjectName,
			TurnCount:        row.TurnCount,
			UpdateTime:       updatedAt,
		})
	}
	return page, nil
}

// DisableSessionCatalog stops exposing every locally discovered session for a
// provider. Existing imported conversations remain stored, but conversation
// listings hide them while their source session is inactive.
func (s *Service) DisableSessionCatalog(ctx context.Context, owner, provider string) error {
	owner = strings.TrimSpace(owner)
	provider = strings.ToLower(strings.TrimSpace(provider))
	if s == nil || s.db == nil || !validIdentity(owner, 255) || !validProvider(provider) {
		return ErrInvalidSource
	}
	return s.db.WithContext(ctx).Model(&orm.ExternalAgentSession{}).
		Where("owner_user_id = ? AND provider = ? AND active = ?", owner, provider, true).
		Updates(map[string]any{"active": false, "updated_at": s.now()}).Error
}

func (s *Service) SyncSessionCatalog(
	ctx context.Context,
	owner, provider, hostID string,
	sessions []NativeSession,
	reset bool,
) (int, error) {
	owner = strings.TrimSpace(owner)
	provider = strings.ToLower(strings.TrimSpace(provider))
	hostID = strings.TrimSpace(hostID)
	if s == nil || s.db == nil || !validIdentity(owner, 255) ||
		!validProvider(provider) || !validIdentity(hostID, 128) || len(sessions) > 5000 {
		return 0, ErrInvalidSource
	}
	projects := make(map[string]NativeSession, len(sessions))
	totalTurns := 0
	accepted := 0
	for _, session := range sessions {
		session.ThreadID = strings.TrimSpace(session.ThreadID)
		session.ProjectKey = strings.TrimSpace(session.ProjectKey)
		session.ProjectName = cleanImportedLabel(session.ProjectName, 200)
		session.DisplayName = cleanImportedLabel(session.DisplayName, 255)
		if !validIdentity(session.ThreadID, 128) ||
			!validIdentity(session.ProjectKey, 128) ||
			!validIdentity(session.ProjectName, 200) ||
			!validIdentity(session.DisplayName, 255) || len(session.Turns) > 5000 {
			continue
		}
		validTurns := make([]NativeTurn, 0, len(session.Turns))
		for index := range session.Turns {
			turn := session.Turns[index]
			turn.ID = strings.TrimSpace(turn.ID)
			turn.User = cleanImportedText(turn.User, 1<<20)
			turn.Assistant = cleanImportedText(turn.Assistant, 1<<20)
			if !validIdentity(turn.ID, 128) || turn.User == "" ||
				len([]rune(turn.User)) > 1<<20 || len([]rune(turn.Assistant)) > 1<<20 {
				continue
			}
			if len(turn.Images) > 4 {
				turn.Images = turn.Images[:4]
			}
			validImages := make([]NativeImage, 0, len(turn.Images))
			for _, image := range turn.Images {
				image.Name = filepath.Base(strings.TrimSpace(image.Name))
				if validIdentity(image.Name, 255) && len(image.Base64) <= 7<<20 {
					validImages = append(validImages, image)
				}
			}
			turn.Images = validImages
			validTurns = append(validTurns, turn)
		}
		session.Turns = validTurns
		accepted++
		totalTurns += len(validTurns)
		if totalTurns > 100000 {
			return 0, ErrInvalidSource
		}
		if existing, ok := projects[session.ThreadID]; ok {
			session.Turns = append(existing.Turns, session.Turns...)
			if session.TurnCount == 0 {
				session.TurnCount = existing.TurnCount
			}
		}
		projects[session.ThreadID] = session
	}
	now := s.now()
	updated := accepted
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if reset {
			if err := tx.Model(&orm.ExternalAgentSession{}).
				Where("owner_user_id = ? AND provider = ? AND host_id = ?", owner, provider, hostID).
				Updates(map[string]any{"active": false, "updated_at": now}).Error; err != nil {
				return err
			}
		}
		service := New(tx)
		service.now = s.now
		for _, session := range projects {
			if err := service.adoptLegacyHost(ctx, owner, provider, hostID, session.ThreadID); err != nil {
				return err
			}
			var current orm.ExternalAgentSession
			currentErr := tx.Where("owner_user_id = ? AND provider = ? AND host_id = ? AND provider_thread_id = ?", owner, provider, hostID, session.ThreadID).
				Take(&current).Error
			if currentErr != nil && !errors.Is(currentErr, gorm.ErrRecordNotFound) {
				return currentErr
			}
			turnCount := session.TurnCount
			if turnCount == 0 {
				turnCount = len(session.Turns)
			}
			if currentErr == nil && turnCount == 0 {
				turnCount = current.TurnCount
			}
			if err := service.importNativeSession(ctx, owner, provider, hostID, session); err != nil {
				return err
			}
			var nativeUpdated *time.Time
			if !session.NativeUpdated.IsZero() {
				value := session.NativeUpdated.UTC()
				nativeUpdated = &value
			}
			row := orm.ExternalAgentSession{
				ID:          deterministicID("native-session", owner+"\x00"+provider+"\x00"+hostID+"\x00"+session.ThreadID),
				OwnerUserID: owner, Provider: provider, HostID: hostID, ProviderThreadID: session.ThreadID,
				ProjectKey: session.ProjectKey, ProjectName: session.ProjectName,
				DisplayName: session.DisplayName, TurnCount: turnCount, Active: true,
				NativeUpdatedAt: nativeUpdated, LastSeenAt: now, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "owner_user_id"}, {Name: "provider"}, {Name: "host_id"}, {Name: "provider_thread_id"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"project_key", "project_name", "display_name",
					"turn_count", "active", "native_updated_at", "last_seen_at", "updated_at",
				}),
			}).Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
	return updated, err
}

func (s *Service) adoptLegacyHost(ctx context.Context, owner, provider, hostID, threadID string) error {
	if hostID == "host-legacy" {
		return nil
	}
	var currentBindings int64
	if err := s.db.WithContext(ctx).Model(&orm.ExternalAgentBinding{}).
		Where("provider = ? AND host_id = ? AND provider_thread_id = ?", provider, hostID, threadID).
		Count(&currentBindings).Error; err != nil {
		return err
	}
	if currentBindings == 0 {
		var legacy orm.ExternalAgentBinding
		err := s.db.WithContext(ctx).
			Where("created_by_user_id = ? AND provider = ? AND host_id = ? AND provider_thread_id = ?", owner, provider, "host-legacy", threadID).
			Take(&legacy).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil {
			if err := s.db.WithContext(ctx).Model(&orm.ExternalAgentBinding{}).Where("id = ?", legacy.ID).
				Updates(map[string]any{"host_id": hostID, "updated_at": s.now()}).Error; err != nil {
				return err
			}
			if err := s.db.WithContext(ctx).Exec(`
				DELETE FROM chat_histories
				WHERE conversation_id = ? AND algorithm_id = ?
				  AND NOT EXISTS (
				      SELECT 1 FROM external_agent_runs
				      WHERE external_agent_runs.history_id = chat_histories.id
				  )
			`, legacy.ConversationID, "external:"+provider).Error; err != nil {
				return err
			}
		}
	}
	var currentSessions int64
	if err := s.db.WithContext(ctx).Model(&orm.ExternalAgentSession{}).
		Where("owner_user_id = ? AND provider = ? AND host_id = ? AND provider_thread_id = ?", owner, provider, hostID, threadID).
		Count(&currentSessions).Error; err != nil {
		return err
	}
	if currentSessions == 0 {
		return s.db.WithContext(ctx).Model(&orm.ExternalAgentSession{}).
			Where("owner_user_id = ? AND provider = ? AND host_id = ? AND provider_thread_id = ?", owner, provider, "host-legacy", threadID).
			Updates(map[string]any{"host_id": hostID, "updated_at": s.now()}).Error
	}
	return nil
}

func (s *Service) importNativeSession(
	ctx context.Context,
	owner, provider, hostID string,
	session NativeSession,
) error {
	source := Source{
		Provider: provider, HostID: hostID, ThreadID: session.ThreadID,
		ProjectKey: session.ProjectKey, ProjectName: session.ProjectName,
		Message: session.DisplayName,
	}
	binding, err := s.resolveBinding(ctx, owner, normalizedSource(source, ""))
	if err != nil {
		return err
	}
	if err := s.ensureConversation(ctx, owner, binding, source); err != nil {
		return err
	}
	var maxSequence int
	if err := s.db.WithContext(ctx).Model(&orm.ChatHistory{}).
		Where("conversation_id = ?", binding.ConversationID).
		Select("COALESCE(MAX(seq), 0)").Scan(&maxSequence).Error; err != nil {
		return err
	}
	now := s.now()
	conversationUpdatedAt := session.NativeUpdated.UTC()
	for _, turn := range session.Turns {
		createdAt := turn.CreatedAt.UTC()
		if createdAt.IsZero() {
			createdAt = now
		}
		if conversationUpdatedAt.IsZero() || createdAt.After(conversationUpdatedAt) {
			conversationUpdatedAt = createdAt
		}
		identity := owner + "\x00" + provider + "\x00" + hostID + "\x00" + session.ThreadID + "\x00" + turn.ID
		historyID := deterministicID("mcp-history", identity)
		userContent := turn.User
		var importedInputs []map[string]any
		if provider == "codex" {
			userContent, importedInputs = normalizeCodexUserMessage(owner, historyID, turn.User, turn.Images)
		}
		managedHistoryID, err := s.managedHistoryID(ctx, owner, provider, session.ThreadID, binding.ConversationID, turn, createdAt)
		if err != nil {
			return err
		}
		if managedHistoryID != "" {
			if err := s.db.WithContext(ctx).Model(&orm.ChatHistory{}).
				Where("id = ? AND conversation_id = ?", managedHistoryID, binding.ConversationID).
				Updates(map[string]any{
					"raw_content": userContent, "content": userContent, "result": turn.Assistant,
					"update_time": createdAt,
				}).Error; err != nil {
				return err
			}
			if err := s.db.WithContext(ctx).
				Where("conversation_id = ? AND algorithm_id = ? AND id <> ? AND raw_content = ? AND result = ? AND create_time BETWEEN ? AND ? AND NOT EXISTS (SELECT 1 FROM external_agent_runs WHERE external_agent_runs.history_id = chat_histories.id)",
					binding.ConversationID, "external:"+provider, managedHistoryID, turn.User, turn.Assistant,
					createdAt.Add(-5*time.Minute), createdAt.Add(5*time.Minute)).
				Delete(&orm.ChatHistory{}).Error; err != nil {
				return err
			}
			continue
		}
		if turn.Managed {
			continue
		}
		var existing int64
		if err := s.db.WithContext(ctx).Model(&orm.ChatHistory{}).
			Where("id = ?", historyID).Count(&existing).Error; err != nil {
			return err
		}
		if existing == 0 {
			maxSequence++
			extPayload := map[string]any{"external_agent_activity": map[string]any{
				"provider": provider, "host_id": hostID,
				"thread_id": session.ThreadID, "turn_id": turn.ID,
				"source": "native_transcript",
			}}
			if len(importedInputs) > 0 {
				extPayload["input"] = importedInputs
			}
			ext, _ := json.Marshal(extPayload)
			history := orm.ChatHistory{
				ID: historyID, Seq: maxSequence, ConversationID: binding.ConversationID,
				AlgorithmID: "external:" + provider, RawContent: userContent, Content: userContent,
				Result: turn.Assistant, Ext: ext,
				TimeMixin: orm.TimeMixin{CreateTime: createdAt, UpdateTime: createdAt},
			}
			if err := s.db.WithContext(ctx).Create(&history).Error; err != nil {
				return err
			}
		} else {
			updates := map[string]any{
				"raw_content": userContent, "content": userContent, "result": turn.Assistant,
				"update_time": createdAt,
			}
			if len(importedInputs) > 0 {
				ext, _ := json.Marshal(map[string]any{
					"external_agent_activity": map[string]any{
						"provider": provider, "host_id": hostID, "thread_id": session.ThreadID,
						"turn_id": turn.ID, "source": "native_transcript",
					},
					"input": importedInputs,
				})
				updates["ext"] = ext
			}
			if err := s.db.WithContext(ctx).Model(&orm.ChatHistory{}).Where("id = ?", historyID).
				Updates(updates).Error; err != nil {
				return err
			}
		}
	}
	var historyCount int64
	if err := s.db.WithContext(ctx).Model(&orm.ChatHistory{}).
		Where("conversation_id = ?", binding.ConversationID).Count(&historyCount).Error; err != nil {
		return err
	}
	if conversationUpdatedAt.IsZero() {
		var conversation orm.Conversation
		if err := s.db.WithContext(ctx).Select("updated_at").
			Where("id = ?", binding.ConversationID).Take(&conversation).Error; err != nil {
			return err
		}
		conversationUpdatedAt = conversation.UpdatedAt
	}
	return s.db.WithContext(ctx).Model(&orm.Conversation{}).Where("id = ?", binding.ConversationID).
		UpdateColumns(map[string]any{
			"display_name": session.DisplayName, "chat_times": historyCount,
			"chat_executor": chatExecutor(provider), "updated_at": conversationUpdatedAt,
		}).Error
}

func (s *Service) managedHistoryID(
	ctx context.Context,
	owner, provider, threadID, conversationID string,
	turn NativeTurn,
	createdAt time.Time,
) (string, error) {
	var direct int64
	if err := s.db.WithContext(ctx).Model(&orm.ExternalChatRun{}).
		Where("history_id = ? AND conversation_id = ? AND actor_user_id = ? AND provider = ? AND provider_thread_id = ?",
			turn.ID, conversationID, owner, provider, threadID).
		Count(&direct).Error; err != nil {
		return "", err
	}
	if direct > 0 {
		return turn.ID, nil
	}
	if !turn.Managed {
		return "", nil
	}
	var candidates []orm.ExternalChatRun
	if err := s.db.WithContext(ctx).
		Where("conversation_id = ? AND actor_user_id = ? AND provider = ? AND provider_thread_id = ? AND query = ?",
			conversationID, owner, provider, threadID, turn.User).
		Order("created_at DESC").Limit(20).Find(&candidates).Error; err != nil {
		return "", err
	}
	bestID, bestDistance := "", 5*time.Minute
	for _, candidate := range candidates {
		distance := candidate.CreatedAt.Sub(createdAt)
		if distance < 0 {
			distance = -distance
		}
		if distance <= bestDistance {
			bestID, bestDistance = candidate.HistoryID, distance
		}
	}
	return bestID, nil
}

func (s *Service) resolveBinding(ctx context.Context, owner string, source Source) (orm.ExternalAgentBinding, error) {
	var binding orm.ExternalAgentBinding
	err := s.db.WithContext(ctx).
		Where("provider = ? AND host_id = ? AND provider_thread_id = ?", source.Provider, source.HostID, source.ThreadID).
		Take(&binding).Error
	if err == nil {
		if binding.CreatedByUserID != owner {
			return orm.ExternalAgentBinding{}, ErrThreadOwned
		}
		return binding, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return orm.ExternalAgentBinding{}, err
	}
	now := s.now()
	identity := source.Provider + "\x00" + source.HostID + "\x00" + source.ThreadID
	binding = orm.ExternalAgentBinding{
		ID: deterministicID("binding", identity), ConversationID: deterministicID("conversation", identity),
		Provider: source.Provider, HostID: source.HostID, ProviderThreadID: source.ThreadID,
		CreatedByUserID: owner, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&binding).Error; err != nil {
		return orm.ExternalAgentBinding{}, err
	}
	if err := s.db.WithContext(ctx).
		Where("provider = ? AND host_id = ? AND provider_thread_id = ?", source.Provider, source.HostID, source.ThreadID).
		Take(&binding).Error; err != nil {
		return orm.ExternalAgentBinding{}, err
	}
	if binding.CreatedByUserID != owner {
		return orm.ExternalAgentBinding{}, ErrThreadOwned
	}
	return binding, nil
}

func (s *Service) ensureConversation(
	ctx context.Context,
	owner string,
	binding orm.ExternalAgentBinding,
	source Source,
) error {
	now := s.now()
	label := source.Message
	if label == "" {
		label = activityLabel(source.Provider, source.ThreadID)
	}
	policy := settings.LoadConversationExecutionPolicy(ctx, s.db, owner)
	var conversation orm.Conversation
	err := s.db.WithContext(ctx).Where("id = ?", binding.ConversationID).Take(&conversation).Error
	if err == nil {
		if conversation.CreateUserID != owner {
			return ErrThreadOwned
		}
		updates := map[string]any{
			"deleted_at": nil, "archived_at": nil, "archive_folder_id": nil,
			"chat_executor": chatExecutor(source.Provider), "updated_at": now,
		}
		if conversation.EnableWorkflow == nil {
			updates["enable_plugin"] = policy.EnableWorkflow
		}
		if conversation.WorkflowMode == nil {
			updates["plugin_mode"] = policy.WorkflowMode // workflow-naming: persistence
		}
		if conversation.EnableSubagent == nil {
			updates["enable_subagent"] = policy.EnableSubagent
		}
		if strings.HasPrefix(conversation.DisplayName, activityLabel(source.Provider, source.ThreadID)) ||
			(source.Message != "" && conversation.ChatTimes <= 1) {
			updates["display_name"] = label
		}
		return s.db.WithContext(ctx).Model(&orm.Conversation{}).Where("id = ?", conversation.ID).Updates(updates).Error
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	ext, _ := json.Marshal(map[string]any{"external_agent": map[string]any{
		"provider": source.Provider, "host_id": source.HostID,
		"thread_id": source.ThreadID, "thread_source": source.ThreadSource,
		"project_key": source.ProjectKey, "project_name": source.ProjectName,
		"sync_mode": "native_transcript",
	}})
	conversation = orm.Conversation{
		ID: binding.ConversationID, DisplayName: label,
		ChannelID: "default", SearchConfig: json.RawMessage(`{}`), Ext: ext,
		Models: json.RawMessage(`[]`), ChatExecutor: chatExecutor(source.Provider),
		EnableWorkflow: &policy.EnableWorkflow, WorkflowMode: &policy.WorkflowMode,
		EnableSubagent: &policy.EnableSubagent,
		BaseModel:      orm.BaseModel{CreateUserID: owner, CreateUserName: owner, CreatedAt: now, UpdatedAt: now},
	}
	return s.db.WithContext(ctx).Create(&conversation).Error
}

func normalizedSource(source Source, fallbackTurnID string) Source {
	source.Provider = strings.ToLower(strings.TrimSpace(source.Provider))
	source.HostID = strings.TrimSpace(source.HostID)
	source.ThreadID = strings.TrimSpace(source.ThreadID)
	source.TurnID = strings.TrimSpace(source.TurnID)
	source.ThreadSource = strings.ToLower(strings.TrimSpace(source.ThreadSource))
	source.ProjectKey = strings.TrimSpace(source.ProjectKey)
	source.ProjectName = strings.TrimSpace(source.ProjectName)
	source.Message = strings.TrimSpace(source.Message)
	if len([]rune(source.Message)) > 8192 {
		source.Message = string([]rune(source.Message)[:8192])
	}
	if source.TurnID == "" {
		source.TurnID = strings.TrimSpace(fallbackTurnID)
	}
	return source
}

func validSource(source Source) bool {
	return validProvider(source.Provider) && validIdentity(source.HostID, 128) &&
		validIdentity(source.ThreadID, 128) && validIdentity(source.TurnID, 128) &&
		(source.ThreadSource == "" || validIdentity(source.ThreadSource, 32)) &&
		(source.ProjectKey == "" && source.ProjectName == "" ||
			validIdentity(source.ProjectKey, 128) && validIdentity(source.ProjectName, 200))
}

func validProvider(provider string) bool {
	switch provider {
	case "codex", "cursor", "workbuddy", "trae-work", "deepseek-harness":
		return true
	default:
		return false
	}
}

func validIdentity(value string, limit int) bool {
	value = strings.TrimSpace(value)
	if value == "" || len([]rune(value)) > limit {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func cleanImportedText(value string, limit int) string {
	value = strings.Map(func(character rune) rune {
		if character == 0 || unicode.IsControl(character) && character != '\n' && character != '\t' {
			return -1
		}
		return character
	}, value)
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit])
	}
	return value
}

func cleanImportedLabel(value string, limit int) string {
	return cleanImportedText(strings.Join(strings.Fields(value), " "), limit)
}

func deterministicID(kind, identity string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte("lazymind:"+kind+":"+identity)).String()
}

func activityLabel(provider, threadID string) string {
	label := map[string]string{
		"codex": "Codex", "cursor": "Cursor", "workbuddy": "WorkBuddy",
		"trae-work": "TRAE Work", "deepseek-harness": "DeepSeek Harness",
	}[provider]
	if label == "" {
		label = provider
	}
	runes := []rune(threadID)
	if len(runes) > 20 {
		threadID = string(runes[:12]) + "…" + string(runes[len(runes)-4:])
	}
	return label + " · " + threadID
}

func chatExecutor(provider string) string {
	switch provider {
	case "codex", "cursor", "workbuddy":
		return provider
	default:
		return "lazymind"
	}
}
