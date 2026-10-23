package apierr

import (
	"errors"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// See https://dev.mysql.com/doc/mysql-errors/8.4/en/
const (
	mysqlDuplicateEntry           uint16 = 1062 // ER_DUP_ENTRY
	mysqlDuplicateKeyMessageToken        = " for key '"
)

// IsDuplicateEntry reports whether err is a unique-key violation
// (ER_DUP_ENTRY), however deeply it is wrapped.
//
// This file is the one place either service reads the MySQL driver's
// error type; a caller decides which catalog code its own keys map to
// with [DuplicateKey].
func IsDuplicateEntry(err error) bool {
	me, ok := mysqlError(err)
	return ok && me.Number == mysqlDuplicateEntry
}

// DuplicateKey returns the name of the unique key a duplicate-entry error
// names, without the table qualifier MySQL 8 prepends, and whether err is
// a duplicate-entry error at all. The name is "" when the message does
// not carry one.
func DuplicateKey(err error) (string, bool) {
	me, ok := mysqlError(err)
	if !ok || me.Number != mysqlDuplicateEntry {
		return "", false
	}
	i := strings.LastIndex(me.Message, mysqlDuplicateKeyMessageToken)
	if i < 0 {
		return "", true
	}
	key := strings.TrimSuffix(me.Message[i+len(mysqlDuplicateKeyMessageToken):], "'")
	if j := strings.LastIndexByte(key, '.'); j >= 0 {
		key = key[j+1:]
	}
	return key, true
}

func mysqlError(err error) (*mysql.MySQLError, bool) {
	var me *mysql.MySQLError
	if errors.As(err, &me) && me != nil {
		return me, true
	}
	return nil, false
}
