/**
 * /workspaces/$id/settings/ai/metrics — route stub. See sibling
 * `.lazy.tsx` for the real component.
 *
 * The `?windowDays=` search param is validated through Zod before the
 * lazy module mounts so an invalid URL value is normalised to one of
 * the supported window presets.
 */

import { createFileRoute } from '@tanstack/react-router';
import { z } from 'zod';

const searchSchema = z.object({
  windowDays: z
    .union([z.literal(7), z.literal(30), z.literal(90)])
    .or(z.coerce.number().pipe(z.union([z.literal(7), z.literal(30), z.literal(90)])))
    .optional(),
});

export const Route = createFileRoute('/_authenticated/workspaces/$id/settings/ai/metrics')({
  validateSearch: (raw) => searchSchema.parse(raw),
});
