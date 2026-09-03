import { IANAZone } from 'luxon';

/**
 * A resolved IANA timezone.
 *
 * The constructor is private, so a `Zone` cannot be produced by writing
 * an object literal or by casting a string: the only ways in are
 * [Zone.resolve], [Zone.utc] and [Zone.browser]. That is the whole point
 * of the type. A plain `string` timezone parameter can always be left
 * off or passed `undefined`, and every call site that does so silently
 * inherits whatever zone the machine running the code happens to be in
 * — which for a browser is the reader's, not the data's. Making the
 * resolved zone a value that has to be constructed moves that mistake
 * from something reviewers have to notice to something that does not
 * compile.
 *
 * Mirrors `Zone` in `packages/go-shared/region`, including the rule that
 * an absent zone resolves to UTC rather than to the host's local zone.
 */
export class Zone {
  readonly #name: string;

  private constructor(name: string) {
    this.#name = name;
  }

  static readonly #utc = new Zone('UTC');

  /**
   * One instance per IANA name, for the whole process.
   *
   * A `Zone` has no mutable state, so two instances of `Asia/Tokyo` are
   * indistinguishable by value — but not by identity, and identity is
   * what React compares. A zone rebuilt each render is a new object each
   * render, which silently defeats every `useMemo`, `useCallback` and
   * memoised child it is listed in: the grid would rebuild its entire
   * event map on every keystroke elsewhere on the page. Interning makes
   * the value type behave like one, so call sites can list `zone` in a
   * dependency array and mean it.
   */
  static readonly #interned = new Map<string, Zone>([['UTC', Zone.#utc]]);

  /**
   * The single sanctioned read of the host timezone in the TypeScript
   * tree, resolved once. The host zone does not change within a session
   * and `Intl.DateTimeFormat()` is not cheap enough to call per render.
   */
  static readonly #browser = Zone.#detectHostZone();

  static #detectHostZone(): Zone {
    let name = 'UTC';
    try {
      // zone-exempt: the one sanctioned read of the host zone, reached only via Zone.browser()
      name = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
    } catch {
      name = 'UTC';
    }
    return Zone.parse(name) ?? Zone.#utc;
  }

  /** The IANA name, e.g. `Asia/Tokyo`. Always a valid, non-empty zone. */
  get name(): string {
    return this.#name;
  }

  /** Whether this is UTC — the zone an absent timezone resolves to. */
  get isUtc(): boolean {
    return this.#name === 'UTC';
  }

  toString(): string {
    return this.#name;
  }

  /** UTC. The documented fallback for data that carries no timezone. */
  static utc(): Zone {
    return Zone.#utc;
  }

  /**
   * The first candidate that names a real IANA zone, else UTC.
   *
   * Candidates are tried in order and empty / nullish / unrecognised
   * entries are skipped, so a caller can write the precedence it means
   * — `Zone.resolve(event.timezone, user.timezone, workspace.timezone)`
   * — without each level needing its own guard.
   *
   * Falling back to UTC rather than to the host zone is the same rule
   * the Go resolver applies. It is what makes an expansion or a day
   * boundary a property of the data instead of a property of whoever is
   * looking at it.
   */
  static resolve(...candidates: ReadonlyArray<string | null | undefined>): Zone {
    for (const candidate of candidates) {
      const zone = Zone.parse(candidate);
      if (zone) return zone;
    }
    return Zone.#utc;
  }

  /**
   * The zone named by `value`, or `null` if it is empty or not a zone
   * this runtime knows.
   *
   * For validating input — a timezone picker, a query parameter — where
   * "not a zone" has to be distinguished from "absent". Callers that
   * only need a usable zone should use [Zone.resolve].
   */
  static parse(value: string | null | undefined): Zone | null {
    if (!value) return null;
    const trimmed = value.trim();
    if (!trimmed) return null;
    if (trimmed.toUpperCase() === 'UTC') return Zone.#utc;
    const existing = Zone.#interned.get(trimmed);
    if (existing) return existing;
    if (!IANAZone.isValidZone(trimmed)) return null;
    const zone = new Zone(trimmed);
    Zone.#interned.set(trimmed, zone);
    return zone;
  }

  /**
   * The zone the current runtime is in.
   *
   * This is a legitimate and distinct concept from the zone a piece of
   * data is defined in, and it is not interchangeable with it: it
   * answers "where is the person reading this", which is the right
   * question for the last fallback of a preference chain and for the
   * default offered when composing something new. It is the wrong
   * answer for interpreting data that states its own zone, and the
   * reason this is a separately named constructor rather than the
   * default behaviour of [Zone.resolve] is so that the two readings
   * cannot be confused at a glance.
   */
  static browser(): Zone {
    return Zone.#browser;
  }
}
