package main

import (
	"database/sql"
	"log/slog"

	"github.com/libraz/nodate-flow/apps/auth-api/internal/auth/sessadapter"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/libraz/nodate-flow/packages/go-shared/sessionstore"
)

// buildSessionStore returns the refresh-token session driver. Config
// validation admits only mysql: the access-token validator on auth-api
// and flow-api reads the MySQL sessions table, so the store that creates
// a session has to be that table.
func buildSessionStore(db *sql.DB, q *generated.Queries, logger *slog.Logger) sessionstore.Store {
	logger.Info("session store: mysql")
	return sessadapter.NewMySQLStore(db, q)
}
