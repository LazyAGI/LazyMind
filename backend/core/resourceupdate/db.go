package resourceupdate

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func withUpdateLock(db *gorm.DB) *gorm.DB {
	if db == nil || db.Dialector == nil || db.Dialector.Name() == "sqlite" {
		return db
	}
	return db.Clauses(clause.Locking{Strength: "UPDATE"})
}

func withUpdateSkipLocked(db *gorm.DB) *gorm.DB {
	if db == nil || db.Dialector == nil || db.Dialector.Name() == "sqlite" {
		return db
	}
	return db.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"})
}

func clauseOnConflictDoNothing() clause.OnConflict {
	return clause.OnConflict{DoNothing: true}
}
