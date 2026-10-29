import { z } from 'zod';

// The production CSP forbids eval. Must be imported before any module that builds a zod schema, because zod probes eval at schema construction.
z.config({ jitless: true });
