import { afterEach, describe, expect, it } from 'vitest';

import { getAuthApiUrl, resolveAuthApiUrl } from '../src/config.js';

describe('getAuthApiUrl', () => {
  const original = process.env.NF_AUTH_API_URL;

  afterEach(() => {
    if (original === undefined) {
      delete process.env.NF_AUTH_API_URL;
    } else {
      process.env.NF_AUTH_API_URL = original;
    }
  });

  it('defaults to the auth-api development port', () => {
    delete process.env.NF_AUTH_API_URL;
    expect(getAuthApiUrl()).toBe('http://localhost:8082');
  });

  it('honors NF_AUTH_API_URL', () => {
    process.env.NF_AUTH_API_URL = 'https://auth.example.test';
    expect(getAuthApiUrl()).toBe('https://auth.example.test');
  });

  it('uses stored authApiBaseUrl when env is unset', () => {
    delete process.env.NF_AUTH_API_URL;
    expect(resolveAuthApiUrl({ authApiBaseUrl: 'https://stored-auth.example.test' })).toBe(
      'https://stored-auth.example.test',
    );
  });

  it('lets NF_AUTH_API_URL override stored authApiBaseUrl', () => {
    process.env.NF_AUTH_API_URL = 'https://env-auth.example.test';
    expect(resolveAuthApiUrl({ authApiBaseUrl: 'https://stored-auth.example.test' })).toBe(
      'https://env-auth.example.test',
    );
  });
});
