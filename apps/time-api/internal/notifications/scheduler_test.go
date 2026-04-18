package notifications

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCheckAndNotify_MarksEventsAsNotified(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer db.Close()

	// Simulate one pending event returned by the SELECT query.
	rows := sqlmock.NewRows([]string{"id", "public_id", "title", "start_at", "notification_offset", "owner_user_id"}).
		AddRow(42, make([]byte, 16), "Standup", time.Now().Add(30*time.Minute), 15, 1)

	mock.ExpectQuery("SELECT ce.id, ce.public_id, ce.title, ce.start_at, ce.notification_offset, ce.owner_user_id").
		WillReturnRows(rows)

	// Expect the UPDATE to mark the event as notified.
	mock.ExpectExec("UPDATE calendar_events SET notified_at = NOW\\(\\) WHERE id = \\?").
		WithArgs(42).
		WillReturnResult(sqlmock.NewResult(0, 1))

	CheckAndNotify(context.Background(), db)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestCheckAndNotify_NoRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer db.Close()

	rows := sqlmock.NewRows([]string{"id", "public_id", "title", "start_at", "notification_offset", "owner_user_id"})

	mock.ExpectQuery("SELECT ce.id, ce.public_id, ce.title, ce.start_at, ce.notification_offset, ce.owner_user_id").
		WillReturnRows(rows)

	// No UPDATE should be issued when there are no rows.
	CheckAndNotify(context.Background(), db)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestCheckAndNotify_MultipleEvents(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer db.Close()

	rows := sqlmock.NewRows([]string{"id", "public_id", "title", "start_at", "notification_offset", "owner_user_id"}).
		AddRow(10, make([]byte, 16), "Event A", time.Now().Add(20*time.Minute), 30, 1).
		AddRow(20, make([]byte, 16), "Event B", time.Now().Add(45*time.Minute), 60, 2)

	mock.ExpectQuery("SELECT ce.id").WillReturnRows(rows)

	mock.ExpectExec("UPDATE calendar_events SET notified_at").
		WithArgs(10).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE calendar_events SET notified_at").
		WithArgs(20).
		WillReturnResult(sqlmock.NewResult(0, 1))

	CheckAndNotify(context.Background(), db)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("unmet expectations: %v", err)
	}
}

func TestCheckAndNotifyQuery_ExcludesNotifiedEvents(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("failed to open sqlmock: %v", err)
	}
	defer db.Close()

	// The query must contain "notified_at IS NULL" to exclude already-notified events.
	mock.ExpectQuery("notified_at IS NULL").
		WillReturnRows(sqlmock.NewRows([]string{"id", "public_id", "title", "start_at", "notification_offset", "owner_user_id"}))

	CheckAndNotify(context.Background(), db)

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("query should filter by notified_at IS NULL: %v", err)
	}
}
