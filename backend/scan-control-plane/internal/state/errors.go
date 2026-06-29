package state

import (
	"database/sql"
	"errors"

	"github.com/redis/go-redis/v9"
)

func IsMissing(err error) bool {
	return errors.Is(err, redis.Nil) || errors.Is(err, sql.ErrNoRows)
}
