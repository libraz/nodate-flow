-- name: CreatePublicShare :execlastid
-- Insert a new workspace-owned public share page. Token is pre-hashed
-- by the handler (SHA-256); the plaintext is returned to the caller
-- exactly once at create time.
INSERT INTO calendar_public_shares (
  public_id,
  workspace_id,
  created_by_user_id,
  token_hash,
  title,
  description,
  icon_url,
  cover_url,
  timezone,
  show_holidays_country,
  expires_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: FindPublicShareByPublicId :one
-- Resolve a share within a workspace for the authenticated editor UI.
SELECT
  id,
  public_id,
  workspace_id,
  created_by_user_id,
  token_hash,
  title,
  description,
  icon_url,
  cover_url,
  timezone,
  show_holidays_country,
  expires_at,
  sort_weight,
  enabled,
  updated_at,
  created_at
FROM calendar_public_shares
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: FindPublicShareByTokenHash :one
-- Resolve a share by its SHA-256 token hash for the unauthenticated
-- public render path. Returns only enabled rows; caller applies the
-- expires_at gate so the 410 code path can be distinguished from 404.
SELECT
  cps.id,
  cps.public_id,
  cps.workspace_id,
  cps.title,
  cps.description,
  cps.icon_url,
  cps.cover_url,
  cps.timezone,
  cps.show_holidays_country,
  cps.expires_at,
  cps.created_at,
  w.public_id AS workspace_public_id,
  w.name      AS workspace_name
FROM calendar_public_shares cps
INNER JOIN workspaces w ON w.id = cps.workspace_id AND w.enabled = TRUE
WHERE cps.token_hash = ?
  AND cps.enabled = TRUE
LIMIT 1;

-- name: ListPublicShares :many
-- List workspace shares for the admin UI. Ordered by sort_weight then
-- creation time; no pagination (shares are expected to be few per ws).
SELECT
  cps.public_id,
  cps.title,
  cps.description,
  cps.icon_url,
  cps.cover_url,
  cps.timezone,
  cps.show_holidays_country,
  cps.expires_at,
  cps.sort_weight,
  cps.created_by_user_id,
  u.public_id AS creator_public_id,
  u.display_name AS creator_display_name,
  cps.updated_at,
  cps.created_at,
  (
    SELECT COUNT(*)
    FROM calendar_public_share_events cpse
    WHERE cpse.share_id = cps.id AND cpse.enabled = TRUE
  ) AS event_count
FROM calendar_public_shares cps
LEFT JOIN users u ON u.id = cps.created_by_user_id
WHERE cps.workspace_id = ?
  AND cps.enabled = TRUE
ORDER BY cps.sort_weight ASC, cps.created_at ASC, cps.public_id ASC;

-- name: PatchPublicShare :exec
-- Update mutable share fields. NULL arguments leave columns untouched.
UPDATE calendar_public_shares
SET title                  = COALESCE(sqlc.narg('title'), title),
    description            = COALESCE(sqlc.narg('description'), description),
    icon_url               = COALESCE(sqlc.narg('icon_url'), icon_url),
    cover_url              = COALESCE(sqlc.narg('cover_url'), cover_url),
    timezone               = COALESCE(sqlc.narg('timezone'), timezone),
    show_holidays_country  = COALESCE(sqlc.narg('show_holidays_country'), show_holidays_country),
    expires_at             = COALESCE(sqlc.narg('expires_at'), expires_at),
    sort_weight            = COALESCE(sqlc.narg('sort_weight'), sort_weight)
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: ClearPublicShareExpiresAt :exec
-- Dedicated setter that clears expires_at (COALESCE-based patch cannot
-- distinguish "leave unchanged" from "clear" for nullable columns).
UPDATE calendar_public_shares
SET expires_at = NULL
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: RotatePublicShareToken :exec
-- Regenerate the token hash; invalidates any previously issued URL.
UPDATE calendar_public_shares
SET token_hash = ?
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: DisablePublicShare :exec
-- Soft-delete a share page. Child rows in calendar_public_share_events
-- are left as-is (soft-disabled at the share level is sufficient; the
-- render query joins through cps.enabled).
UPDATE calendar_public_shares
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?;

-- name: FindEventIDAndVisibility :one
-- Lightweight resolver used by the public-share attach path to translate
-- an event public_id into its internal id + visibility within a workspace.
-- Enforces workspace isolation so a share cannot publish another ws's events.
SELECT id, visibility
FROM calendar_events
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE
LIMIT 1;

-- name: AttachEventToShare :execlastid
-- Publish one event on a share page. Caller validates:
--   - event.visibility != 'confidential' (otherwise SHARE.SHARE_EVENT.EVENT_NOT_VISIBLE).
--   - event.workspace_id matches share.workspace_id.
-- Uniqueness (share_id, event_id, enabled) is enforced by the table.
INSERT INTO calendar_public_share_events (
  public_id,
  workspace_id,
  share_id,
  event_id,
  sort_weight
) VALUES (?, ?, ?, ?, ?);

-- name: DetachEventFromShare :exec
-- Remove one event from a share (soft). Looks up the link by share +
-- event internal ids (caller resolves both via their public ids first).
UPDATE calendar_public_share_events
SET enabled = FALSE
WHERE share_id = ?
  AND event_id = ?
  AND enabled = TRUE;

-- name: UpdateShareEventSortWeight :exec
-- Update the sort_weight of a single share-event link.
-- Called in a loop (inside a tx) when a user reorders the events on a
-- public share. Scoped to (share_id, public_id) so a caller cannot
-- accidentally reorder a link belonging to a different share.
UPDATE calendar_public_share_events
SET sort_weight = ?
WHERE share_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: ListPublicShareEventsForEditor :many
-- List events published on a share for the workspace-authenticated
-- editor UI. Returns full event metadata so the editor can show what is
-- currently public. Does not filter by visibility (the editor needs to
-- see even confidential events that slipped through so they can be
-- detached, though AttachEventToShare prevents that path going forward).
SELECT
  cpse.public_id AS link_public_id,
  cpse.sort_weight AS link_sort_weight,
  cpse.created_at AS link_created_at,
  ce.public_id     AS event_public_id,
  ce.title         AS event_title,
  ce.start_at,
  ce.end_at,
  ce.all_day,
  ce.timezone     AS event_timezone,
  ce.location,
  ce.visibility,
  c.public_id      AS calendar_public_id,
  c.name           AS calendar_name
FROM calendar_public_share_events cpse
INNER JOIN calendar_events ce ON ce.id = cpse.event_id AND ce.enabled = TRUE
INNER JOIN calendars c        ON c.id  = ce.calendar_id AND c.enabled = TRUE
INNER JOIN calendar_public_shares cps ON cps.id = cpse.share_id AND cps.enabled = TRUE
WHERE cps.workspace_id = ?
  AND cps.public_id = ?
  AND cpse.enabled = TRUE
ORDER BY cpse.sort_weight ASC, ce.start_at ASC, ce.public_id ASC;

-- name: ListPublicShareEventsByTokenHash :many
-- Unauthenticated public-render query. Final safety gate on event
-- visibility and start_at IS NOT NULL. expires_at is checked in the
-- handler (not here) so 410 can be differentiated from 404.
SELECT
  ce.public_id AS event_public_id,
  ce.title,
  ce.start_at,
  ce.end_at,
  ce.all_day,
  ce.timezone,
  ce.location,
  ce.memo,
  ce.url,
  ce.kind,
  ce.visibility,
  ce.show_as,
  ce.block_label,
  COALESCE(ce.recurrence_rule, CAST('null' AS JSON)) AS recurrence_rule,
  ce.recurrence_end,
  COALESCE(ce.recurrence_exceptions, CAST('null' AS JSON)) AS recurrence_exceptions,
  cpse.sort_weight AS link_sort_weight
FROM calendar_public_shares cps
INNER JOIN calendar_public_share_events cpse ON cpse.share_id = cps.id AND cpse.enabled = TRUE
INNER JOIN calendar_events ce ON ce.id = cpse.event_id AND ce.enabled = TRUE
WHERE cps.token_hash = ?
  AND cps.enabled = TRUE
  AND ce.visibility <> 'confidential'
  AND ce.start_at IS NOT NULL
ORDER BY cpse.sort_weight ASC, ce.start_at ASC, ce.public_id ASC
LIMIT 2000;
