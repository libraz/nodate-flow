-- name: CreatePage :execlastid
-- Insert a new wiki/documentation page.
INSERT INTO pages (
  public_id,
  workspace_id,
  project_id,
  creator_id,
  parent_page_id,
  title,
  body,
  is_ai_generated
) VALUES (?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListPagesForWorkspace :many
-- List enabled root pages (no parent) for a workspace with creator info.
SELECT
  pg.public_id,
  u.public_id AS creator_public_id,
  u.display_name AS creator_display_name,
  p.public_id AS project_public_id,
  p.name AS project_name,
  pg.title,
  pg.is_ai_generated,
  pg.sort_weight,
  pg.updated_at,
  pg.created_at,
  COUNT(*) OVER() AS total
FROM pages pg
INNER JOIN users u ON u.id = pg.creator_id
LEFT JOIN projects p ON p.id = pg.project_id
WHERE pg.workspace_id = ?
  AND pg.parent_page_id IS NULL
  AND pg.enabled = TRUE
ORDER BY pg.sort_weight ASC, pg.title ASC, pg.id ASC
LIMIT ? OFFSET ?;

-- name: ListChildPages :many
-- List enabled child pages for a given parent page with creator info.
-- The recursive CTE enforces that every ancestor on the chain up to the
-- root is enabled, so a soft-disabled ancestor (parent, grandparent, ...)
-- transitively hides the entire subtree even when the row itself is
-- still enabled = TRUE.
WITH RECURSIVE enabled_tree (id) AS (
  SELECT pages.id FROM pages
  WHERE pages.workspace_id = sqlc.arg('workspace_id')
    AND pages.parent_page_id IS NULL
    AND pages.enabled = TRUE
  UNION ALL
  SELECT p.id FROM pages p
  INNER JOIN enabled_tree et ON et.id = p.parent_page_id
  WHERE p.workspace_id = sqlc.arg('workspace_id')
    AND p.enabled = TRUE
)
SELECT
  pg.public_id,
  u.public_id AS creator_public_id,
  u.display_name AS creator_display_name,
  p.public_id AS project_public_id,
  p.name AS project_name,
  pg.title,
  pg.is_ai_generated,
  pg.sort_weight,
  pg.updated_at,
  pg.created_at,
  COUNT(*) OVER() AS total
FROM pages pg
INNER JOIN users u ON u.id = pg.creator_id
LEFT JOIN projects p ON p.id = pg.project_id
WHERE pg.workspace_id = sqlc.arg('workspace_id')
  AND pg.parent_page_id = ?
  AND pg.enabled = TRUE
  AND pg.id IN (SELECT id FROM enabled_tree)
ORDER BY pg.sort_weight ASC, pg.title ASC, pg.id ASC
LIMIT ? OFFSET ?;

-- name: ListPagesForProject :many
-- List all enabled pages (any nesting level) scoped to a project with creator info.
-- The recursive CTE filters out pages whose ancestor chain contains any
-- soft-disabled row, so disabling a parent transitively hides the entire
-- subtree even though descendants are still enabled = TRUE.
WITH RECURSIVE enabled_tree (id) AS (
  SELECT pages.id FROM pages
  WHERE pages.workspace_id = sqlc.arg('workspace_id')
    AND pages.parent_page_id IS NULL
    AND pages.enabled = TRUE
  UNION ALL
  SELECT p.id FROM pages p
  INNER JOIN enabled_tree et ON et.id = p.parent_page_id
  WHERE p.workspace_id = sqlc.arg('workspace_id')
    AND p.enabled = TRUE
)
SELECT
  pg.public_id,
  u.public_id AS creator_public_id,
  u.display_name AS creator_display_name,
  parent.public_id AS parent_page_public_id,
  pg.title,
  pg.is_ai_generated,
  pg.sort_weight,
  pg.updated_at,
  pg.created_at,
  COUNT(*) OVER() AS total
FROM pages pg
INNER JOIN users u ON u.id = pg.creator_id
LEFT JOIN pages parent ON parent.id = pg.parent_page_id
WHERE pg.workspace_id = sqlc.arg('workspace_id')
  AND pg.project_id = ?
  AND pg.enabled = TRUE
  AND pg.id IN (SELECT id FROM enabled_tree)
ORDER BY pg.sort_weight ASC, pg.title ASC, pg.id ASC
LIMIT ? OFFSET ?;

-- name: GetPageByPublicId :one
-- Fetch a single page by workspace_id + public_id, including parent page info.
-- pg.id is required: used by MCP resolvePage and page handlers for
-- parent_page_id resolution and circular-reference checks.
-- The recursive CTE enforces ancestor-chain enabled propagation: a page
-- with any disabled ancestor is treated as not found, matching the list
-- queries above and preventing direct-fetch bypass of soft-disabled trees.
WITH RECURSIVE enabled_tree (id) AS (
  SELECT pages.id FROM pages
  WHERE pages.workspace_id = sqlc.arg('workspace_id')
    AND pages.parent_page_id IS NULL
    AND pages.enabled = TRUE
  UNION ALL
  SELECT p.id FROM pages p
  INNER JOIN enabled_tree et ON et.id = p.parent_page_id
  WHERE p.workspace_id = sqlc.arg('workspace_id')
    AND p.enabled = TRUE
)
SELECT
  pg.id,
  pg.public_id,
  pg.workspace_id,
  u.public_id AS creator_public_id,
  u.display_name AS creator_display_name,
  p.public_id AS project_public_id,
  p.name AS project_name,
  parent.public_id AS parent_page_public_id,
  parent.title AS parent_page_title,
  pg.title,
  pg.body,
  pg.is_ai_generated,
  pg.sort_weight,
  pg.notes,
  pg.updated_at,
  pg.created_at
FROM pages pg
INNER JOIN users u ON u.id = pg.creator_id
LEFT JOIN projects p ON p.id = pg.project_id
LEFT JOIN pages parent ON parent.id = pg.parent_page_id
WHERE pg.workspace_id = sqlc.arg('workspace_id')
  AND pg.public_id = ?
  AND pg.enabled = TRUE
  AND pg.id IN (SELECT id FROM enabled_tree)
LIMIT 1;

-- name: UpdatePage :exec
-- Update mutable page fields. Uses sqlc.narg for nullable columns.
UPDATE pages
SET title          = COALESCE(sqlc.narg('title'), title),
    body           = COALESCE(sqlc.narg('body'), body),
    project_id     = sqlc.narg('project_id'),
    parent_page_id = sqlc.narg('parent_page_id')
WHERE workspace_id = ?
  AND public_id = ?
  AND enabled = TRUE;

-- name: DisablePage :exec
-- Soft-delete a page.
UPDATE pages
SET enabled = FALSE
WHERE workspace_id = ?
  AND public_id = ?;

-- name: CountChildPages :one
-- Count enabled child pages for a given parent. Used to check before deletion.
SELECT COUNT(*) AS child_count
FROM pages
WHERE workspace_id = ?
  AND parent_page_id = ?
  AND enabled = TRUE;

-- name: GetPageDepth :one
-- Compute nesting depth of a page by walking up to the root via recursive CTE.
-- Returns 0 for root pages, 1 for direct children of root, etc.
WITH RECURSIVE ancestors AS (
  SELECT pages.id, pages.parent_page_id, 0 AS depth
  FROM pages
  WHERE pages.id = ?
  UNION ALL
  SELECT p.id, p.parent_page_id, a.depth + 1
  FROM pages p
  INNER JOIN ancestors a ON a.parent_page_id = p.id
)
SELECT MAX(depth) AS depth
FROM ancestors
LIMIT 1;

-- name: SearchPages :many
-- Search enabled pages by title pattern within a workspace.
-- The recursive CTE filters out pages whose ancestor chain contains any
-- soft-disabled row, matching the propagation enforced by the list and
-- get queries so search cannot surface a child of a disabled subtree.
WITH RECURSIVE enabled_tree (id) AS (
  SELECT pages.id FROM pages
  WHERE pages.workspace_id = sqlc.arg('workspace_id')
    AND pages.parent_page_id IS NULL
    AND pages.enabled = TRUE
  UNION ALL
  SELECT p.id FROM pages p
  INNER JOIN enabled_tree et ON et.id = p.parent_page_id
  WHERE p.workspace_id = sqlc.arg('workspace_id')
    AND p.enabled = TRUE
)
SELECT
  pg.public_id,
  u.public_id AS creator_public_id,
  u.display_name AS creator_display_name,
  p.public_id AS project_public_id,
  p.name AS project_name,
  parent.public_id AS parent_page_public_id,
  pg.title,
  pg.is_ai_generated,
  pg.sort_weight,
  pg.updated_at,
  pg.created_at,
  COUNT(*) OVER() AS total
FROM pages pg
INNER JOIN users u ON u.id = pg.creator_id
LEFT JOIN projects p ON p.id = pg.project_id
LEFT JOIN pages parent ON parent.id = pg.parent_page_id
WHERE pg.workspace_id = sqlc.arg('workspace_id')
  AND pg.title LIKE ?
  AND pg.enabled = TRUE
  AND pg.id IN (SELECT id FROM enabled_tree)
ORDER BY pg.sort_weight ASC, pg.title ASC, pg.id ASC
LIMIT ? OFFSET ?;
