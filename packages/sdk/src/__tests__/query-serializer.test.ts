/**
 * Array query parameters must reach the wire in the comma form.
 *
 * The schema declares every array parameter `explode: false`, and the
 * API reads only the first occurrence of a repeated parameter: sent
 * exploded, `?state=open&state=done` filters by `open` alone and the
 * caller gets a short list with no error. The assertions below are on
 * the URL the client actually requested, because that is the only place
 * the difference is observable.
 */

import { describe, expect, it, vi } from 'vitest';

import { createClient } from '../client';

/** Captures the requested URL and answers with an empty task list. */
function clientWithSpy(): { client: ReturnType<typeof createClient>; urls: string[] } {
  const urls: string[] = [];
  const fetchSpy = vi.fn(async (input: Request): Promise<Response> => {
    urls.push(input.url);
    return new Response(JSON.stringify({ tasks: [], total: 0 }), {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    });
  });
  const client = createClient({
    baseUrl: 'https://api.example.com',
    fetchOptions: { fetch: fetchSpy as unknown as typeof fetch },
  });
  return { client, urls };
}

describe('createClient query serialization', () => {
  it('joins an array parameter with commas instead of repeating it', async () => {
    const { client, urls } = clientWithSpy();

    await client.GET('/tasks', {
      params: { query: { projectId: 'prj-1', state: ['open', 'done'], limit: 100, offset: 0 } },
    });

    const url = urls[0] ?? '';
    expect(decodeURIComponent(url)).toContain('state=open,done');
    // The exploded form is what silently drops the second value.
    expect(url).not.toContain('state=open&state=done');
    expect(new URL(url).searchParams.getAll('state')).toHaveLength(1);
  });

  it('applies to every array parameter, not just the task list filters', async () => {
    const { client, urls } = clientWithSpy();

    await client.GET('/tasks', {
      params: { query: { projectId: 'prj-1', priority: [4, 2], limit: 100, offset: 0 } },
    });

    const url = urls[0] ?? '';
    expect(decodeURIComponent(url)).toContain('priority=4,2');
    expect(new URL(url).searchParams.getAll('priority')).toHaveLength(1);
  });

  it('leaves scalar parameters untouched', async () => {
    const { client, urls } = clientWithSpy();

    await client.GET('/tasks', {
      params: { query: { projectId: 'prj-1', q: 'design review', limit: 100, offset: 0 } },
    });

    const params = new URL(urls[0] ?? '').searchParams;
    expect(params.get('projectId')).toBe('prj-1');
    expect(params.get('q')).toBe('design review');
  });
});
