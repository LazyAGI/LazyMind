package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"lazymind/core/common"
	"lazymind/core/common/orm"
)

func registerForkableHistoryRun(ctx context.Context, db *gorm.DB, convID, historyID, runID, query string, target chatPersistTarget, ext json.RawMessage) error {
	return conversationCheckpoint(ctx, db, convID, func(tx *gorm.DB) error {
		var conversation orm.Conversation
		if err := tx.Where("id = ? AND deleted_at IS NULL", convID).Take(&conversation).Error; err != nil {
			return err
		}
		var history orm.ChatHistory
		err := tx.Where("id = ? AND conversation_id = ?", historyID, convID).Take(&history).Error
		if err == nil {
			if history.RunID == runID {
				return nil
			}
			if !target.IsRegeneration {
				return forkFail("SOURCE_NOT_SETTLED")
			}
			return tx.Model(&history).Updates(map[string]any{"run_id": runID, "run_status": "generating", "run_terminal": nil, "ext": ext, "update_time": time.Now().UTC()}).Error
		}
		if err != gorm.ErrRecordNotFound {
			return err
		}
		now := time.Now().UTC()
		return tx.Create(&orm.ChatHistory{ID: historyID, ConversationID: convID, Seq: target.Seq, RawContent: query, Content: query, RunID: runID, RunStatus: "generating", Ext: ext, TimeMixin: orm.TimeMixin{CreateTime: now, UpdateTime: now}}).Error
	})
}

// A short database checkpoint shared by snapshot readers and history writers.
// SQLite pins BEGIN IMMEDIATE to one connection; no model execution runs here.
func conversationCheckpoint(ctx context.Context, db *gorm.DB, conversationID string, fn func(*gorm.DB) error) error {
	locked := func(tx *gorm.DB) error {
		if conversationID == "" {
			return fn(tx)
		}
		var conversation orm.Conversation
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Where("id = ?", conversationID).Take(&conversation).Error; err != nil {
			return err
		}
		return fn(tx)
	}
	// Callers that already own a transaction must keep using its connection.
	if _, inTransaction := db.Statement.ConnPool.(gorm.TxCommitter); inTransaction {
		return locked(db.WithContext(ctx))
	}
	if db.Dialector.Name() != "sqlite" {
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Exec("SET LOCAL lock_timeout = '5s'").Error; err != nil {
				return err
			}
			return locked(tx)
		})
	}
	for attempt := 0; attempt < 6; attempt++ {
		err := db.WithContext(ctx).Connection(func(conn *gorm.DB) error {
			var busyTimeout int
			if err := conn.Session(&gorm.Session{NewDB: true}).Raw("PRAGMA busy_timeout").Scan(&busyTimeout).Error; err != nil {
				return err
			}
			if err := conn.Exec("PRAGMA busy_timeout = 1000").Error; err != nil {
				return err
			}
			defer conn.WithContext(context.WithoutCancel(ctx)).Exec(fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeout))
			if err := conn.Exec("BEGIN IMMEDIATE").Error; err != nil {
				return err
			}
			committed := false
			defer func() {
				if !committed {
					conn.WithContext(context.WithoutCancel(ctx)).Exec("ROLLBACK")
				}
			}()
			tx := conn.Session(&gorm.Session{SkipDefaultTransaction: true})
			if err := locked(tx); err != nil {
				return err
			}
			if err := tx.Exec("COMMIT").Error; err != nil {
				return err
			}
			committed = true
			return nil
		})
		if !common.IsSQLiteBusy(err) || attempt == 5 {
			return err
		}
		timer := time.NewTimer(time.Duration(10<<attempt) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}
