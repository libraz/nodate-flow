// Package taskservice owns transactionally authorized task reads and
// commands shared by transports and internal callers.
package taskservice

import (
	"database/sql"
	"errors"
	"unicode/utf8"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
)

const (
	MinPriority     = 0
	MaxPriority     = 4
	MaxCommentRunes = 50000
)

var (
	ErrServiceUnavailable = errors.New("taskservice: required dependency is unavailable")
	ErrPriorityOutOfRange = errors.New("taskservice: priority must be between 0 and 4")
	ErrCommentLength      = errors.New("taskservice: comment must contain between 1 and 50000 characters")
)

// Service is the shared task command boundary.
type Service struct {
	DB        *sql.DB
	Queries   *generated.Queries
	Mutations *mutationlog.Recorder
}

// New constructs a Service over the database and shared generated queries.
func New(db *sql.DB, queries *generated.Queries, mutations *mutationlog.Recorder) *Service {
	return &Service{DB: db, Queries: queries, Mutations: mutations}
}

func (s *Service) ready(write bool) error {
	if s == nil || s.DB == nil || s.Queries == nil || (write && s.Mutations == nil) {
		return ErrServiceUnavailable
	}
	return nil
}

func requireCommentBody(body string) error {
	n := utf8.RuneCountInString(body)
	if n < 1 || n > MaxCommentRunes {
		return ErrCommentLength
	}
	return nil
}

func internalTaskID(id uint32) int64 {
	return int64(id) // #nosec G115 -- task ids are INT UNSIGNED and fit in int64
}

func internalAgentID(id uint32) int64 {
	return int64(id) // #nosec G115 -- agent ids are INT UNSIGNED and fit in int64
}

func (s *Service) checkReadiness(write bool) error {
	return s.ready(write)
}
