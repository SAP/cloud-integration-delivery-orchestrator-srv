package db

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// IsUniqueViolation reports whether err is a Postgres unique-constraint
// violation (SQLSTATE 23505). The project does not enable gorm TranslateError,
// so callers match the raw pgconn error directly; this is the single shared
// detector used by both the handler and service layers to map duplicate-key
// failures to 409 Conflict instead of a raw 500.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
