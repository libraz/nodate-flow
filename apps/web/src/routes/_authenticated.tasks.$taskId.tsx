/**
 * /tasks/$taskId — route stub. Component lives in the sibling
 * `.lazy.tsx` file so the heavy task-detail bundle is code-split.
 */

import { createFileRoute } from '@tanstack/react-router';

export const Route = createFileRoute('/_authenticated/tasks/$taskId')({});
