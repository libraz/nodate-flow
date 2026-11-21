package calendars

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/operation"
	"github.com/libraz/nodate-flow/packages/go-shared/eventacl"
)

const createEventOperationKind = "calendar.event.create"

func validEventCreateIdempotencyKey(key string) bool {
	if len(key) == 0 || len(key) > 128 {
		return false
	}
	for _, c := range key {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			strings.ContainsRune("._~-", c) {
			continue
		}
		return false
	}
	return true
}

func isHTTPStatusError(err error) bool {
	var statusErr huma.StatusError
	return errors.As(err, &statusErr)
}

func normalizeCreatedEventResponse(response *EventResponse) {
	if response.RecurrenceRule != nil && bytes.Equal(bytes.TrimSpace(*response.RecurrenceRule), []byte("null")) {
		response.RecurrenceRule = nil
	}
	if response.RecurrenceExceptions != nil && bytes.Equal(bytes.TrimSpace(*response.RecurrenceExceptions), []byte("null")) {
		response.RecurrenceExceptions = nil
	}
}

func canonicalCreateEventHash(calendarID types.PublicID, body any) ([32]byte, error) {
	encodedBody, err := json.Marshal(body)
	if err != nil {
		return [32]byte{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encodedBody))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return [32]byte{}, err
	}
	canonical, err := json.Marshal(struct {
		CalendarID string `json:"calendarId"`
		Body       any    `json:"body"`
	}{CalendarID: calendarID.String(), Body: decoded})
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

func resolveEventCreateOwner(
	ctx context.Context,
	queries *generated.Queries,
	workspaceID, actorID uint32,
	sub calendar.FindCalendarMemberRow,
	ownerPublicID *string,
) (uint32, error) {
	if ownerPublicID == nil {
		return actorID, nil
	}
	ownerUID, err := uuid.Parse(*ownerPublicID)
	if err != nil {
		return 0, httpErr(apierrors.CalendarEventOwnerUserIdMalformed)
	}
	ownerInternal, err := queries.FindWorkspaceMemberUserInternalIdByPublicId(ctx,
		generated.FindWorkspaceMemberUserInternalIdByPublicIdParams{
			WorkspaceID: workspaceID,
			PublicID:    types.FromUUID(ownerUID),
		})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, httpErr(apierrors.CalendarEventOwnerUserNotFound)
		}
		return 0, err
	}
	if !canSetOwner(actorID, ownerInternal, sub) {
		return 0, httpErr(apierrors.CalendarEventEditPermissionRequired)
	}
	return ownerInternal, nil
}

func createEventWithIdempotency(
	ctx context.Context,
	deps Deps,
	input *CreateEventInput,
	workspaceID, actorID uint32,
	cal calendar.FindCalendarByPublicIdRow,
	params calendar.CreateCalendarEventParams,
) (*CreateEventOutput, error) {
	if !validEventCreateIdempotencyKey(input.IdempotencyKey) {
		return nil, httpErr(apierrors.ValidationBodyFieldInvalid)
	}
	requestHash, err := canonicalCreateEventHash(cal.PublicID, input.Body)
	if err != nil {
		return nil, httpErr(apierrors.InternalUnexpected)
	}

	var out CreateEventOutput
	var answered error
	err = dbretry.InTx(ctx, deps.DB, "calendars.CreateEvent", &sql.TxOptions{Isolation: sql.LevelReadCommitted}, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		receipt, err := operation.Lock(ctx, tx, operation.Request{
			WorkspaceID:    workspaceID,
			Principal:      operation.Principal{Kind: operation.PrincipalUser, ID: actorID},
			Kind:           createEventOperationKind,
			Key:            input.IdempotencyKey,
			TargetKind:     "calendar",
			TargetPublicID: cal.PublicID,
			Hash:           requestHash,
		})
		if err != nil {
			answered = httpErr(apierrors.InternalUnexpected)
			return err
		}

		queries := deps.Queries.WithTx(tx.RawTx())
		calendarQueries := deps.CalendarQueries.WithTx(tx.RawTx())
		currentWorkspaceID, currentActorID, err := resolveWorkspace(ctx, queries, input.WsID)
		if err != nil {
			if !isHTTPStatusError(err) {
				answered = httpErr(apierrors.InternalUnexpected)
			}
			return err
		}
		currentCalendar, sub, err := resolveCalendarWrite(ctx, calendarQueries, currentWorkspaceID, currentActorID, input.CalID)
		if err != nil {
			if !isHTTPStatusError(err) {
				answered = httpErr(apierrors.CalendarEventStoreReadInterrupted)
			}
			return err
		}
		if currentWorkspaceID != workspaceID || currentActorID != actorID || currentCalendar.ID != cal.ID {
			return errCalendarAccessDenied
		}
		if receipt.CompletedAt != nil {
			if receipt.Hash != requestHash {
				return httpErr(apierrors.CalendarEventIdempotencyKeyConflict)
			}
			err := replayCreateEventReceipt(ctx, calendarQueries, receipt.Result, currentCalendar,
				workspaceID, actorID, &out)
			if err != nil && !isHTTPStatusError(err) {
				answered = httpErr(apierrors.CalendarEventStoreReadInterrupted)
			}
			return err
		}

		currentOwnerID, ownerErr := resolveEventCreateOwner(ctx, queries, workspaceID, actorID, sub, input.Body.OwnerUserID)
		if ownerErr != nil {
			if !isHTTPStatusError(ownerErr) {
				answered = httpErr(apierrors.CalendarEventOwnerUserResolveInterrupted)
			}
			return ownerErr
		}
		params.OwnerUserID = currentOwnerID
		if _, err := calendarQueries.CreateCalendarEvent(ctx, params); err != nil {
			answered = httpErr(apierrors.CalendarEventStoreWriteInterrupted)
			return err
		}
		created, err := calendarQueries.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
			PublicID:    params.PublicID,
			CalendarID:  currentCalendar.ID,
			WorkspaceID: workspaceID,
		})
		if err != nil {
			answered = httpErr(apierrors.CalendarEventStoreReadInterrupted)
			return err
		}

		snapshot := eventFromFullRow(created)
		normalizeCreatedEventResponse(&snapshot)
		out.Body = snapshot
		scrubEventDetailsForParty(string(created.Visibility), string(currentCalendar.DefaultEventVisibility),
			created.OwnerUserID, created.CreatedByUserID, actorID, false, &out.Body.Location, &out.Body.Memo, &out.Body.URL)

		payload := map[string]any{
			"eventId":    created.PublicID.String(),
			"calendarId": currentCalendar.PublicID.String(),
			"title":      created.Title,
			"kind":       string(created.Kind),
		}
		if params.StartAt.Valid {
			payload["startAt"] = params.StartAt.Time
			payload["endAt"] = params.EndAt.Time
		}
		if err := deps.Mutations.RecordInTx(ctx, tx, calendarMutationActor(workspaceID, actorID), mutationlog.Mutation{
			EventType:       eventbus.CalEventCreated,
			AuditAction:     "calendar.event.create",
			ResourceType:    "calendar.event",
			ResourceID:      created.PublicID.String(),
			SubjectPublicID: created.PublicID,
			CalendarID:      calendarMutationCalendar(currentCalendar.ID),
			Payload:         payload,
			CallSite:        "calendars.CreateEvent",
		}); err != nil {
			answered = httpErr(apierrors.CalendarEventStoreWriteInterrupted)
			return err
		}
		result, err := json.Marshal(out.Body)
		if err != nil {
			answered = httpErr(apierrors.InternalUnexpected)
			return err
		}
		if err := operation.Complete(ctx, tx, receipt, result); err != nil {
			answered = httpErr(apierrors.InternalUnexpected)
			return err
		}
		return nil
	})
	if err != nil {
		if answered != nil {
			return nil, answered
		}
		if isHTTPStatusError(err) {
			return nil, err
		}
		return nil, httpErr(apierrors.CalendarEventStoreWriteInterrupted)
	}
	return &out, nil
}

func replayCreateEventReceipt(
	ctx context.Context,
	queries *calendar.Queries,
	result json.RawMessage,
	cal calendar.FindCalendarByPublicIdRow,
	workspaceID, actorID uint32,
	out *CreateEventOutput,
) error {
	var snapshot EventResponse
	if err := json.Unmarshal(result, &snapshot); err != nil {
		return httpErr(apierrors.InternalUnexpected)
	}
	eventUUID, err := uuid.Parse(snapshot.ID)
	if err != nil {
		return httpErr(apierrors.InternalUnexpected)
	}
	evt, err := queries.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
		PublicID:    types.FromUUID(eventUUID),
		CalendarID:  cal.ID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errEventNotFound
		}
		return err
	}

	isAttendee, err := calendarEventAttendee(ctx, queries, actorID, evt.ID, evt.RecurrenceParentID)
	if err != nil {
		return err
	}
	if !eventacl.CanSee(
		eventacl.Event{
			Visibility:      eventacl.Visibility(evt.Visibility),
			OwnerUserID:     evt.OwnerUserID,
			CreatedByUserID: evt.CreatedByUserID,
			CalendarDefault: eventacl.Visibility(cal.DefaultEventVisibility),
		},
		eventacl.Actor{UserID: actorID, IsAttendee: isAttendee},
	) {
		return errEventNotFound
	}
	scrubEventDetailsForParty(string(evt.Visibility), string(cal.DefaultEventVisibility), evt.OwnerUserID, evt.CreatedByUserID, actorID, isAttendee,
		&snapshot.Location, &snapshot.Memo, &snapshot.URL)
	out.Body = snapshot
	return nil
}

// calendarEventAttendee reports whether the actor is an enabled attendee
// of this occurrence. An override that holds attendee rows of its own,
// enabled or not, is judged on them alone; one with none reads its
// series' rows.
func calendarEventAttendee(
	ctx context.Context,
	queries *calendar.Queries,
	actorID, eventID uint32,
	recurrenceParentID sql.NullInt32,
) (bool, error) {
	target := handlerutil.NullInt32From(eventID)
	if recurrenceParentID.Valid {
		own, err := queries.HasCalendarEventAttendeeRows(ctx, target)
		if err != nil {
			return false, err
		}
		if !own {
			target = recurrenceParentID
		}
	}
	_, err := queries.FindCalendarEventAttendee(ctx, calendar.FindCalendarEventAttendeeParams{
		EventID: target,
		UserID:  actorID,
	})
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}
