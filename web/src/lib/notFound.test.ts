import { describe, expect, it } from 'vitest';
import { notFound } from './notFound';

const page = new URL('https://aocn.dev/u/octocat');
const headers = { 'Cache-Control': 'private, no-store' };

describe('notFound', () => {
  it('is the site 404 page, with status 404 and the given cache headers', async () => {
    let asked = '';
    const fetchFn = (async (url: URL) => {
      asked = url.toString();
      return new Response('<!doctype html><html><body>not found_</body></html>', { status: 200 });
    }) as typeof fetch;
    const response = await notFound(page, headers, fetchFn);
    expect(asked).toBe('https://aocn.dev/404.html');
    expect(response.status).toBe(404);
    expect(response.headers.get('Cache-Control')).toBe('private, no-store');
    expect(response.headers.get('Content-Type')).toBe('text/html; charset=utf-8');
    expect(await response.text()).toContain('not found_');
  });

  it('is still a 404, with the same cache headers, when the page cannot be fetched', async () => {
    const fetchFn = (async () => {
      throw new Error('offline');
    }) as typeof fetch;
    const response = await notFound(page, headers, fetchFn);
    expect(response.status).toBe(404);
    expect(response.headers.get('Cache-Control')).toBe('private, no-store');
    expect(await response.text()).toBe('not found');
  });

  it('never passes on an empty answer', async () => {
    const fetchFn = (async () => new Response('', { status: 404 })) as typeof fetch;
    const response = await notFound(page, headers, fetchFn);
    expect(response.status).toBe(404);
    expect(await response.text()).toBe('not found');
  });
});
