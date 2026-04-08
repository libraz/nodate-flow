-- v_projects
-- Project listing with basic metadata. Workspace-scoped.
CREATE OR REPLACE VIEW v_projects AS
SELECT
  p.workspace_id,
  p.public_id,
  p.slug,
  p.name,
  p.description,
  p.color,
  p.is_archived,
  p.started_on,
  p.ended_on,
  p.sort_weight,
  p.updated_at,
  p.created_at
FROM projects p
INNER JOIN workspaces w
  ON w.id = p.workspace_id AND w.enabled = TRUE
WHERE p.enabled = TRUE;
