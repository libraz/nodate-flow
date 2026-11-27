import { describe, expect, it } from 'vitest';

import { queryRetryDelay, shouldRetryQuery } from '../query-client.js';

describe('query retry policy', () => {
  const err = (httpStatus: number, retryAfterSeconds?: number) => ({
    httpStatus,
    retryAfterSeconds,
  });

  it('retries 429 up to three times and 5xx once, never 401', () => {
    expect(shouldRetryQuery(2, err(429))).toBe(true);
    expect(shouldRetryQuery(3, err(429))).toBe(false);
    expect(shouldRetryQuery(0, err(500))).toBe(true);
    expect(shouldRetryQuery(1, err(500))).toBe(false);
    expect(shouldRetryQuery(0, err(401))).toBe(false);
  });

  it('honours Retry-After on 429, capped at 30s, else backs off exponentially', () => {
    expect(queryRetryDelay(0, err(429, 5))).toBe(5_000);
    expect(queryRetryDelay(0, err(429, 120))).toBe(30_000);
    expect(queryRetryDelay(2, err(429))).toBe(4_000);
    expect(queryRetryDelay(9, err(500))).toBe(30_000);
  });
});
