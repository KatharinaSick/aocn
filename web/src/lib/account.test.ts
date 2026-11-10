import { describe, expect, it } from 'vitest';
import { accountAt, fetchMe, loginHref } from './account';

const opensAt = new Date('2026-12-01T06:00:00+01:00');
const me = { login: 'octo', avatarUrl: 'https://example.com/a.png' };

function answering(status: number, body: unknown): typeof fetch {
  return async () => new Response(JSON.stringify(body), { status });
}

describe('fetchMe', () => {
  it('reads who is logged in', async () => {
    expect(await fetchMe(answering(200, me))).toEqual(me);
  });

  it('is nobody when not logged in', async () => {
    expect(await fetchMe(answering(401, { error: 'not logged in' }))).toBeNull();
  });

  it('is nobody when the API is down or answers nonsense', async () => {
    expect(await fetchMe(answering(503, {}))).toBeNull();
    expect(await fetchMe(answering(200, { login: 42 }))).toBeNull();
    expect(await fetchMe(async () => new Response('<html>', { status: 200 }))).toBeNull();
    expect(
      await fetchMe(async () => {
        throw new TypeError('offline');
      }),
    ).toBeNull();
  });
});

describe('accountAt', () => {
  it('hides logging in until door 1 opens', () => {
    expect(accountAt(null, new Date(opensAt.getTime() - 1), opensAt)).toEqual({ state: 'hidden' });
    expect(accountAt(null, opensAt, opensAt)).toEqual({ state: 'loggedOut' });
  });

  it('shows who is logged in, also before door 1', () => {
    expect(accountAt(me, new Date('2026-11-20T12:00:00Z'), opensAt)).toEqual({ state: 'loggedIn', me });
  });
});

describe('loginHref', () => {
  it('comes back to the page', () => {
    expect(loginHref('/doors/3')).toBe('/api/auth/github/login?return=%2Fdoors%2F3');
  });
});
