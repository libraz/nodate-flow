// Package handlerutil — duplicate-entry detection helper for auth-api
// handlers. Mirrors apps/flow-api/internal/http/handlers/handlerutil so
// the two services classify the same MySQL error consistently and one
// service can not silently diverge.
package handlerutil

import (
	"errors"

	"github.com/go-sql-driver/mysql"
)

// mysqlErrDuplicateEntry is the MySQL error number for a unique-constraint
// violation (ER_DUP_ENTRY). See https://dev.mysql.com/doc/mysql-errors/8.4/en/
const mysqlErrDuplicateEntry = 1062

// IsDuplicateEntry reports whether err is a MySQL duplicate-entry
// error (1062 / ER_DUP_ENTRY). It uses errors.As against the mysql
// driver's typed error so wrapped errors are still detected. Callers
// in the avatar-upload path use it to distinguish "another concurrent
// upload won the race for this storage_objects row" from a generic
// insert failure.
func IsDuplicateEntry(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == mysqlErrDuplicateEntry
}
