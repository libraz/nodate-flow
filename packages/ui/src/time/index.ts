/**
 * The shared time model: a resolved timezone and a wall-clock calendar
 * day, mirroring `packages/go-shared/region` on the TypeScript side.
 *
 * Lives here rather than in the SDK because this is the only workspace
 * package that both web apps already depend on *and* that already
 * depends on luxon, and because the recurrence expander next door needs
 * the same "absent timezone means UTC" rule it publishes. Putting it in
 * the SDK would have meant either a new luxon dependency there or a
 * `packages/ui` -> `packages/sdk` edge pointing the wrong way.
 */

export { Day } from './day';
export { Zone } from './zone';
