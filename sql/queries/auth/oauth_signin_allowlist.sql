-- oauth_signin_allowlist statements.
--
-- There is no workspace_id predicate anywhere in this file and none is
-- missing: the table is instance-level. Sign-in resolves who the caller is
-- before any workspace is chosen, so an entry admits a person to the
-- deployment rather than to a tenant, and a tenant predicate would have
-- nothing to bind to.
--
-- The statements live under queries/auth because the read that decides a
-- sign-in is part of the OIDC callback pipeline; the administrator's
-- statements stay beside it rather than under queries/admin so that one
-- table's writes and its liveness rule are read together. The Admin* naming
-- prefix travels with the queries/admin directory, so it is not used here.
--
-- No statement here compares entry_value to users.email and none can:
-- entry_value is latin1_bin and users.email is latin1_swedish_ci, so MySQL
-- rejects a predicate over the two columns outright (error 1267, illegal mix
-- of collations). Matching an address against the list is the caller's job,
-- over the rows ListEnabledOauthSigninAllowlistEntries returns. Comparing
-- entry_value against a bind parameter is unaffected.

-- name: ListEnabledOauthSigninAllowlistEntries :many
-- Every live entry, as the OIDC callback needs it: the kind and the value and
-- nothing else, since the match itself runs in memory over the whole list.
--
-- No LIMIT, deliberately. The convention that bounds a list exists to stop an
-- unbounded user-generated table from being read whole; this one is operator-
-- maintained and small, and the query is a membership test rather than a page
-- of results. A LIMIT here would silently drop the tail of the allowlist and
-- turn everyone below the cut into a rejected sign-in, with nothing in the
-- response to say why.
--
-- (entry_kind, entry_value) is unique, so ordering by the pair is already a
-- total order and needs no further tie-breaker.
SELECT
  entry_kind,
  entry_value
FROM oauth_signin_allowlist
WHERE enabled = TRUE
ORDER BY entry_kind ASC, entry_value ASC;

-- name: ListOauthSigninAllowlistEntries :many
-- Every entry for the administrator's screen, withdrawn ones included: a
-- withdrawn entry keeps its claim on (entry_kind, entry_value) and can be
-- brought back, so it has to stay visible. That is what separates this from
-- the sign-in read above, which must see live entries only.
--
-- The adder is resolved to the user's public_id: added_by_user_id is an
-- internal sequence and must not reach a response. The join is LEFT because
-- the FK is ON DELETE SET NULL, so an entry outlives the account that added
-- it; PublicID scans NULL as the zero UUID, which the mapper reads back as
-- "nobody" -- the same treatment granted_by_public_id gets on the instance
-- admin list.
SELECT
  a.public_id,
  a.entry_kind,
  a.entry_value,
  a.notes,
  a.enabled,
  adder.public_id AS added_by_public_id,
  adder.display_name AS added_by_display_name,
  a.updated_at,
  a.created_at,
  COUNT(*) OVER() AS total
FROM oauth_signin_allowlist a
LEFT JOIN users adder ON adder.id = a.added_by_user_id
ORDER BY a.sort_weight ASC, a.entry_kind ASC, a.entry_value ASC
LIMIT ? OFFSET ?;

-- name: FindOauthSigninAllowlistEntry :one
-- One entry by public_id, shaped exactly like a row of the list above so a
-- write handler can answer with the entry as it now stands -- including the
-- created_at and the adder it cannot know from its own input, and, after an
-- upsert that revived a withdrawn row, the values that row actually carries.
SELECT
  a.public_id,
  a.entry_kind,
  a.entry_value,
  a.notes,
  a.enabled,
  adder.public_id AS added_by_public_id,
  adder.display_name AS added_by_display_name,
  a.updated_at,
  a.created_at
FROM oauth_signin_allowlist a
LEFT JOIN users adder ON adder.id = a.added_by_user_id
WHERE a.public_id = ?
LIMIT 1;

-- name: UpsertOauthSigninAllowlistEntry :exec
-- Add an entry, or revive the one that already holds this
-- (entry_kind, entry_value) pair.
--
-- uniq_oauth_signin_allowlist_kind_value covers withdrawn entries too: taking
-- an entry out only clears its enabled flag, so the row keeps the pair and a
-- plain INSERT of the same domain or address collides instead of restoring
-- it. Without the revival branch an operator who removes an entry can never
-- add it back -- and the failure surfaces as a duplicate-key error on a
-- perfectly ordinary request.
--
-- Re-adding states the entry afresh: the notes given now, the administrator
-- doing it now, and the public_id the caller has already minted for the
-- response. Leaving the old public_id in place would answer the request with
-- an identifier that addresses nothing.
--
-- Not :execrows. An upsert always leaves the row in the state the caller
-- asked for, so there is no claim to lose; MySQL still reports zero affected
-- rows when the revived row already held these exact values, and a caller
-- reading that as "nothing happened" would skip the audit entry for a write
-- that did take effect.
INSERT INTO oauth_signin_allowlist (
  public_id,
  added_by_user_id,
  entry_kind,
  entry_value,
  notes
) VALUES (?, ?, ?, ?, ?)
ON DUPLICATE KEY UPDATE
  public_id        = VALUES(public_id),
  added_by_user_id = VALUES(added_by_user_id),
  notes            = VALUES(notes),
  enabled          = TRUE;

-- name: WithdrawOauthSigninAllowlistEntry :execrows
-- Withdraw an entry by clearing its enabled flag. The row stays: it is what
-- holds the (entry_kind, entry_value) claim that lets the same entry be added
-- back later, and deleting it would strand that path.
--
-- The WHERE re-checks enabled, so an entry withdrawn by another path between
-- the caller's read and this write matches nothing. Callers MUST inspect
-- RowsAffected and treat 0 as "no live entry by that id" rather than
-- recording a withdrawal that never happened.
UPDATE oauth_signin_allowlist
SET enabled = FALSE
WHERE public_id = ?
  AND enabled = TRUE;
