// Package store text core text DB、Redis InitializetextRequestUsertext，text chat、doc、file text。
package store

import (
	"gorm.io/gorm"

	"lazymind/core/state"
)

var (
	db        *gorm.DB
	lazyllmDB *gorm.DB
	stateDB   state.Store
)

// Init Initializetext DB text Redis，text main textStarttext
func Init(database, lazyllmDatabase *gorm.DB, stateStore state.Store) {
	db = database
	if lazyllmDatabase != nil {
		lazyllmDB = lazyllmDatabase
	} else {
		lazyllmDB = database
	}
	stateDB = stateStore
}

// DB text *gorm.DB
func DB() *gorm.DB { return db }

// LazyLLMDB text lazyllm text；text。
func LazyLLMDB() *gorm.DB {
	if lazyllmDB != nil {
		return lazyllmDB
	}
	return db
}

// State returns the short-lived shared state backend.
func State() state.Store { return stateDB }

// MustStateFromEnv creates the configured state backend.
func MustStateFromEnv() state.Store {
	return state.MustFromEnv()
}
