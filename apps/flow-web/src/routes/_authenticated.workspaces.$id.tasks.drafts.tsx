/**
 * /workspaces/$id/tasks/drafts — route stub. See sibling `.lazy.tsx`
 * for the page component; the retro draft queue is code-split so the
 * Card / EmptyState chunk only loads when the surface is opened. The
 * queue is deep-linked (no sidebar entry) and the Phase 6 / L3
 * timeline backlink will navigate here.
 */

import { createFileRoute } from '@tanstack/react-router';

export const Route = createFileRoute('/_authenticated/workspaces/$id/tasks/drafts')({});
