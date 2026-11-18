// Package updateformattest builds SQLite fixtures for the desktop update tests.
package updateformattest

import (
	"errors"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/episode"
	"lazymind/core/migrate"
)

// CreateCoreDatabase initializes a new test database from the Core working directory.
func CreateCoreDatabase(filename string) error {
	runner, err := migrate.NewRunner("sqlite", filename, "migrations")
	if err != nil {
		return err
	}
	if err := errors.Join(runner.Up(0), runner.Close()); err != nil {
		return err
	}
	db, err := gorm.Open(sqlite.Open(filename), &gorm.Config{})
	if err != nil {
		return err
	}
	raw, err := db.DB()
	if err != nil {
		return err
	}
	return errors.Join(episode.Initialize(db), raw.Close())
}
