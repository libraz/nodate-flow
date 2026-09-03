import { describe, expect, it } from 'vitest';

import { Zone } from '../zone';

describe('Zone.resolve', () => {
  it('takes the first candidate that names a real zone', () => {
    expect(Zone.resolve('Asia/Tokyo', 'Europe/Paris').name).toBe('Asia/Tokyo');
  });

  it('skips nullish and empty candidates rather than treating them as a choice', () => {
    expect(Zone.resolve(undefined, null, '', '   ', 'Europe/Paris').name).toBe('Europe/Paris');
  });

  it('skips a candidate that is not a zone this runtime knows', () => {
    expect(Zone.resolve('Not/AZone', 'Europe/Paris').name).toBe('Europe/Paris');
  });

  it('falls back to UTC, not to the host zone', () => {
    // The whole point of the type. If this ever returns the host zone,
    // every day boundary in the app becomes a property of who is
    // looking, and the client stops agreeing with the Go expander.
    const resolved = Zone.resolve(undefined);
    expect(resolved.name).toBe('UTC');
    expect(resolved.isUtc).toBe(true);
  });

  it('resolves with no candidates at all to UTC', () => {
    expect(Zone.resolve().name).toBe('UTC');
  });

  it('accepts UTC in any casing', () => {
    expect(Zone.resolve('utc').isUtc).toBe(true);
    expect(Zone.resolve('Utc').isUtc).toBe(true);
  });
});

describe('Zone.parse', () => {
  it('distinguishes "not a zone" from "absent"', () => {
    expect(Zone.parse('Not/AZone')).toBeNull();
    expect(Zone.parse(undefined)).toBeNull();
    expect(Zone.parse('  ')).toBeNull();
    expect(Zone.parse('Asia/Tokyo')?.name).toBe('Asia/Tokyo');
  });

  it('trims surrounding whitespace', () => {
    expect(Zone.parse('  Asia/Tokyo  ')?.name).toBe('Asia/Tokyo');
  });
});

describe('Zone.browser', () => {
  it('returns a usable zone and is stable within a session', () => {
    const first = Zone.browser();
    expect(first.name.length).toBeGreaterThan(0);
    expect(Zone.browser()).toBe(first);
  });

  it('does not collapse a distinct zone onto the UTC default', () => {
    // Interning makes equal zones the same instance, so identity only
    // says anything between zones that genuinely differ. Comparing
    // `Zone.browser()` with `Zone.utc()` would instead make this a
    // statement about where the suite runs: on a UTC host the two are
    // the same zone and rightly the same instance, so the assertion
    // would pass east of Greenwich and fail at it.
    expect(Zone.resolve('Asia/Tokyo')).not.toBe(Zone.utc());
    expect(Zone.utc().isUtc).toBe(true);
    expect(Zone.resolve('Asia/Tokyo').isUtc).toBe(false);
  });

  // That `Zone.browser()` reads the host at all — rather than quietly
  // answering UTC — is pinned where the host can actually be varied:
  // the sweep in apps/flow-web replays its assertions in a child
  // process per zone and compares the offset the accessor reports
  // against the one the child was started in. A single-host unit test
  // cannot make that claim without becoming a claim about the host.
});

describe('Zone cannot be forged', () => {
  // These are compile-time assertions, not runtime ones: `tsc -b` fails
  // if any `@ts-expect-error` below stops being an error, which is what
  // makes "every zone came from the resolver" a property of the type
  // rather than a convention. A plain `string` timezone parameter has no
  // such property, and that is the whole reason this class exists.
  it('rejects construction, casting and structural impersonation', () => {
    // @ts-expect-error the constructor is private: zones come from the resolver
    const constructed = new Zone('Asia/Tokyo');
    expect(constructed).toBeDefined();

    const shaped = { name: 'Asia/Tokyo', isUtc: false, toString: () => 'Asia/Tokyo' };
    // @ts-expect-error a private field makes Zone nominal, so a lookalike object is not one
    const impersonated: Zone = shaped;
    expect(impersonated).toBeDefined();

    // @ts-expect-error a bare string is not a resolved zone
    const fromString: Zone = 'Asia/Tokyo';
    expect(fromString).toBeDefined();
  });
});

describe('Zone identity', () => {
  it('renders as its IANA name when interpolated', () => {
    expect(`${Zone.resolve('Asia/Tokyo')}`).toBe('Asia/Tokyo');
  });

  it('reports isUtc only for UTC', () => {
    expect(Zone.utc().isUtc).toBe(true);
    expect(Zone.resolve('Asia/Tokyo').isUtc).toBe(false);
  });
});
