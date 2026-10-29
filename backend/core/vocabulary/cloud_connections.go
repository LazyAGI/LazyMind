package vocabulary

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"strings"
	"time"
)

func validCloudProvider(provider string) bool { return provider == "eudic" || provider == "maimemo" }
func (s *Service) cloudConnection(ctx context.Context, owner, provider string) (CloudConnection, error) {
	var c CloudConnection
	if !validCloudProvider(provider) {
		return c, errors.New("invalid vocabulary cloud provider")
	}
	err := s.db.WithContext(ctx).Where("owner_id = ? AND provider = ?", owner, provider).First(&c).Error
	return c, err
}
func (s *Service) cloudClientFor(ctx context.Context, owner, provider, token string) (cloudRemote, error) {
	if !validCloudProvider(provider) {
		return nil, errors.New("invalid vocabulary cloud provider")
	}
	token = strings.TrimSpace(token)
	if token == "" {
		c, err := s.cloudConnection(ctx, owner, provider)
		if err != nil {
			return nil, errors.New("vocabulary cloud connection not found")
		}
		token, err = decryptCloudToken(ctx, c)
		if err != nil {
			return nil, err
		}
	}
	return s.makeCloudClient(provider, token)
}
func (s *Service) SaveCloudConnection(ctx context.Context, owner, provider string, in CloudConnectionInput) (CloudConnection, error) {
	if !validCloudProvider(provider) || owner == "" {
		return CloudConnection{}, errors.New("invalid vocabulary cloud provider")
	}
	if len(in.Token) > 8192 || strings.ContainsAny(in.Token, "\r\n") {
		return CloudConnection{}, errors.New("invalid vocabulary cloud token")
	}
	c, err := s.cloudConnection(ctx, owner, provider)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return c, err
	}
	oldID := c.ID
	replace := strings.TrimSpace(in.Token) != ""
	if c.ID == "" || replace {
		c = CloudConnection{ID: uuid.NewString(), OwnerID: owner, Provider: provider, CreatedAt: time.Now().UTC()}
	}
	if replace {
		c.AccountKey = cloudAccountKey(provider, in.Token)
		c.TokenCipher, err = encryptCloudToken(ctx, c, strings.TrimSpace(in.Token))
		if err != nil {
			return c, err
		}
	}
	if c.TokenCipher == "" {
		return c, errors.New("invalid vocabulary cloud token")
	}
	c.Enabled = in.Enabled
	c.TargetID = strings.TrimSpace(in.TargetID)
	c.TargetName = strings.TrimSpace(in.TargetName)
	c.AddToStudy = provider == "maimemo" && in.AddToStudy
	c.UpdatedAt = time.Now().UTC()
	if len(c.TargetID) > 512 || len(c.TargetName) > 255 {
		return c, errors.New("invalid vocabulary cloud target")
	}
	if c.Enabled && c.TargetID == "" {
		return c, errors.New("invalid vocabulary cloud target")
	}
	// Validate target ownership before enabling, but allow disabling offline.
	if c.Enabled {
		token, e := decryptCloudToken(ctx, c)
		if e != nil {
			return c, e
		}
		client, e := s.makeCloudClient(provider, token)
		if e != nil {
			return c, e
		}
		books, e := client.ListWordbooks(ctx)
		if e != nil {
			return c, e
		}
		found := false
		for _, book := range books {
			if book.ID == c.TargetID {
				found = true
				c.TargetName = book.Name
				break
			}
		}
		if !found {
			return c, errors.New("invalid vocabulary cloud target")
		}
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if oldID != "" && oldID != c.ID {
			if e := tx.Where("id = ? AND owner_id = ?", oldID, owner).Delete(&CloudConnection{}).Error; e != nil {
				return e
			}
			if e := tx.Model(&CloudOperation{}).Where("connection_id = ? AND status IN ?", oldID, []string{"pending", "failed"}).Updates(map[string]any{"status": "canceled", "last_error": "", "updated_at": c.UpdatedAt}).Error; e != nil {
				return e
			}
		}
		if e := tx.Save(&c).Error; e != nil {
			return e
		}
		// Old destinations never silently receive words after target/consent changes.
		q := tx.Model(&CloudOperation{}).Where("connection_id = ? AND status IN ?", c.ID, []string{"pending", "failed"})
		if c.Enabled {
			q = q.Where("target_id <> ? OR add_to_study <> ?", c.TargetID, c.AddToStudy)
		}
		return q.Updates(map[string]any{"status": "canceled", "last_error": "", "updated_at": c.UpdatedAt}).Error
	})
	c.Configured = true
	return c, err
}
func (s *Service) CloudConnections(ctx context.Context, owner string) ([]CloudConnection, error) {
	if err := s.reconcileCloudOperations(ctx, owner); err != nil {
		return nil, err
	}
	var rows []CloudConnection
	if err := s.db.WithContext(ctx).Where("owner_id = ?", owner).Find(&rows).Error; err != nil {
		return nil, err
	}
	result := make([]CloudConnection, 0, 2)
	for _, provider := range []string{"eudic", "maimemo"} {
		c := CloudConnection{Provider: provider}
		for _, row := range rows {
			if row.Provider == provider {
				c = row
				c.Configured = c.TokenCipher != ""
				break
			}
		}
		if c.ID != "" {
			var counts []struct {
				Status string
				Count  int64
			}
			if err := s.db.WithContext(ctx).Model(&CloudOperation{}).Select("status, count(*) AS count").Where("owner_id = ? AND connection_id = ?", owner, c.ID).Group("status").Scan(&counts).Error; err != nil {
				return nil, err
			}
			for _, n := range counts {
				switch n.Status {
				case "pending":
					c.Pending = n.Count
				case "failed":
					c.Failed = n.Count
				case "succeeded":
					c.Succeeded = n.Count
				}
			}
			var latest CloudOperation
			if s.db.WithContext(ctx).Where("owner_id = ? AND connection_id = ? AND last_error <> '' AND status IN ?", owner, c.ID, []string{"pending", "failed"}).Order("updated_at DESC").First(&latest).Error == nil {
				c.LastError = latest.LastError
			}
		}
		result = append(result, c)
	}
	return result, nil
}
func (s *Service) DisconnectCloud(ctx context.Context, owner, provider string) error {
	c, err := s.cloudConnection(ctx, owner, provider)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := tx.Where("id = ? AND owner_id = ?", c.ID, owner).Delete(&CloudConnection{}).Error; e != nil {
			return e
		}
		return tx.Model(&CloudOperation{}).Where("connection_id = ? AND status IN ?", c.ID, []string{"pending", "failed"}).Updates(map[string]any{"status": "canceled", "last_error": "", "updated_at": time.Now().UTC()}).Error
	})
}
func (s *Service) CloudOperations(ctx context.Context, owner string) ([]CloudOperation, error) {
	if err := s.reconcileCloudOperations(ctx, owner); err != nil {
		return nil, err
	}
	rows := make([]CloudOperation, 0)
	err := s.db.WithContext(ctx).Where("owner_id = ?", owner).Order("updated_at DESC").Limit(50).Find(&rows).Error
	return rows, err
}
func enqueueCloudWord(ctx context.Context, tx *gorm.DB, c CloudConnection, word Word) (bool, error) {
	if word.Language != "en" || word.Provider != "local" || word.ArchivedAt != nil {
		return false, nil
	}
	var op CloudOperation
	err := tx.Where("connection_id = ? AND word_id = ? AND target_id = ? AND add_to_study = ?", c.ID, word.ID, c.TargetID, c.AddToStudy).First(&op).Error
	if err == nil && (op.Status == "succeeded" || op.Status == "pending") {
		return false, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return false, err
	}
	if op.ID == "" {
		op = CloudOperation{ID: uuid.NewString(), OwnerID: word.OwnerID, ConnectionID: c.ID, Provider: c.Provider, WordID: word.ID, Term: word.Term, TargetID: c.TargetID, AddToStudy: c.AddToStudy}
	}
	executionKey := op.ID
	if op.Status == "canceled" || op.Status == "failed" {
		executionKey += ":" + uuid.NewString()
	}
	job, err := asyncjob.EnqueueInTransaction(ctx, tx, asyncjob.EnqueueRequest{JobType: CloudSyncJobType, ResourceType: "vocabulary_cloud_connection", ResourceID: c.AccountKey, IdempotencyKey: executionKey, Payload: map[string]string{"operation_id": op.ID}, CreateUserID: word.OwnerID, MaxAttempts: 8, SkipSucceeded: op.Status == "canceled" || op.Status == "failed"})
	if err != nil {
		return false, err
	}
	op.JobID = job.ID
	op.Status = "pending"
	op.LastError = ""
	return true, tx.Save(&op).Error
}
func enqueueCloudConnections(ctx context.Context, tx *gorm.DB, word Word) (bool, error) {
	var connections []CloudConnection
	if err := tx.Where("owner_id = ? AND enabled = ?", word.OwnerID, true).Find(&connections).Error; err != nil {
		return false, err
	}
	queued := false
	for _, c := range connections {
		added, err := enqueueCloudWord(ctx, tx, c, word)
		if err != nil {
			return false, err
		}
		queued = queued || added
	}
	return queued, nil
}
func (s *Service) BackfillCloud(ctx context.Context, owner, provider string) (int, error) {
	if err := s.reconcileCloudOperations(ctx, owner); err != nil {
		return 0, err
	}
	c, err := s.cloudConnection(ctx, owner, provider)
	if err != nil {
		return 0, errors.New("vocabulary cloud connection not found")
	}
	if !c.Enabled {
		return 0, errors.New("vocabulary cloud connection disabled")
	}
	count := 0
	// Bounded transactions keep SQLite writes short for large historical lists.
	lastID := ""
	for {
		var words []Word
		if err := s.db.WithContext(ctx).Where("owner_id = ? AND provider = ? AND language = ? AND archived_at IS NULL AND id > ?", owner, "local", "en", lastID).Order("id").Limit(100).Find(&words).Error; err != nil {
			return count, err
		}
		if len(words) == 0 {
			break
		}
		err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var current CloudConnection
			if e := tx.Where("id = ? AND owner_id = ? AND enabled = ?", c.ID, owner, true).First(&current).Error; e != nil {
				return errors.New("vocabulary cloud connection disabled")
			}
			for _, word := range words {
				ok, e := enqueueCloudWord(ctx, tx, current, word)
				if e != nil {
					return e
				}
				if ok {
					count++
				}
			}
			return nil
		})
		if err != nil {
			return count, err
		}
		lastID = words[len(words)-1].ID
	}
	return count, nil
}

// Recover the public state when a process dies before its handler can record a
// terminal outcome, or the async job is canceled outside the vocabulary UI.
func (s *Service) reconcileCloudOperations(ctx context.Context, owner string) error {
	for _, status := range []string{"failed", "canceled"} {
		jobs := s.db.Model(&orm.AsyncJob{}).Select("id").Where("job_type = ? AND create_user_id = ? AND status = ?", CloudSyncJobType, owner, status)
		updates := map[string]any{"status": status, "updated_at": time.Now().UTC()}
		if status == "failed" {
			updates["last_error"] = gorm.Expr("CASE WHEN last_error = '' THEN ? ELSE last_error END", "vocabulary cloud sync failed")
		}
		if err := s.db.WithContext(ctx).Model(&CloudOperation{}).Where("owner_id = ? AND status = ? AND job_id IN (?)", owner, "pending", jobs).Updates(updates).Error; err != nil {
			return err
		}
	}
	return nil
}
