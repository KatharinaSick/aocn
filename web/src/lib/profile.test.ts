import { describe, expect, it } from 'vitest';
import {
  assess,
  doorCells,
  fetchProfile,
  profileCacheHeaders,
  profileLine,
  readProfileAnswer,
  type DoorCell,
} from './profile';

const body = {
  login: 'Octo',
  avatarUrl: 'https://example.com/a.png',
  since: '2026-11-20T09:00:00Z',
  doors: [{ door: 1, solvedAt: '2026-12-01T06:30:00Z' }],
};

describe('fetchProfile', () => {
  it('reads the card', async () => {
    let asked = '';
    const answer = await fetchProfile('http://api', 'Octo', async (url) => {
      asked = String(url);
      return new Response(JSON.stringify(body), { status: 200 });
    });
    expect(asked).toBe('http://api/users/Octo');
    expect(answer).toEqual({
      state: 'found',
      profile: {
        login: 'Octo',
        avatarUrl: 'https://example.com/a.png',
        since: new Date('2026-11-20T09:00:00Z'),
        solved: new Map([[1, new Date('2026-12-01T06:30:00Z')]]),
      },
    });
  });

  it("doesn't ask for logins GitHub wouldn't allow", async () => {
    const never = async (): Promise<Response> => {
      throw new Error('asked');
    };
    for (const login of ['', '-octo', 'octo_cat', '../health', 'a'.repeat(40), 'octo%2F']) {
      expect(await fetchProfile('http://api', login, never)).toEqual({ state: 'missing' });
    }
  });

  it('is unavailable when the API is down', async () => {
    expect(
      await fetchProfile('http://api', 'octo', async () => {
        throw new TypeError('connection refused');
      }),
    ).toEqual({ state: 'unavailable' });
  });
});

describe('readProfileAnswer', () => {
  it('reads 404 as missing and 5xx as unavailable', () => {
    expect(readProfileAnswer(404, { error: 'nobody here by that name' })).toEqual({ state: 'missing' });
    expect(readProfileAnswer(503, null)).toEqual({ state: 'unavailable' });
  });

  it('throws on nonsense', () => {
    expect(() => readProfileAnswer(200, { login: 'octo' })).toThrow();
    expect(() => readProfileAnswer(400, body)).toThrow();
  });
});

describe('doorCells', () => {
  const openUpTo = (last: number) => (door: number) => door <= last;
  const doors = [1, 3, 5, 7];
  const now = new Date('2026-12-05T12:00:00Z');

  it('marks fixed, open and closed doors', () => {
    const solved = new Map([[3, now]]);
    expect(doorCells(solved, doors, openUpTo(5), now).map((c) => c.state)).toEqual(['open', 'fixed', 'open', 'closed']);
  });

  it("never shows a door as fixed before it's open", () => {
    const solved = new Map([[7, now]]);
    expect(doorCells(solved, doors, openUpTo(5), now)[3]).toEqual({ door: 7, state: 'closed' });
  });
});

describe('profileLine', () => {
  const cells = (...states: DoorCell['state'][]): DoorCell[] => states.map((state, i) => ({ door: i + 1, state }));

  it('fits where the season stands', () => {
    expect(profileLine(cells('closed', 'closed')).message).toBe('Nothing to fix yet. The crew is working on it.');
    expect(profileLine(cells('open', 'closed')).message).toBe('No repairs on record. Yet.');
    expect(profileLine(cells('fixed', 'open', 'open', 'closed')).message).toBe('Fixed 1 of the 3 things the crew broke.');
    expect(profileLine(cells('fixed', 'closed')).mood).toBe('fond');
    expect(profileLine(cells('fixed', 'fixed')).message).toBe('Everything fixed. I am almost impressed.');
  });
});

describe('profileCacheHeaders', () => {
  it('caches cards and misses on Netlify, never a failure', () => {
    expect(profileCacheHeaders({ state: 'missing' })['Netlify-CDN-Cache-Control']).toBe('public, s-maxage=60');
    expect(profileCacheHeaders({ state: 'unavailable' })).toEqual({ 'Cache-Control': 'no-store' });
  });
});

describe('assess', () => {
  it('climbs the titles with the doors fixed', () => {
    const titles = Array.from({ length: 13 }, (_, fixed) => assess(fixed, 12).title);
    expect(titles).toEqual([
      'Unverified human',
      'Occasionally useful',
      'Occasionally useful',
      'Adequate',
      'Adequate',
      'Adequate',
      'Reliable',
      'Reliable',
      'Reliable',
      'Trusted',
      'Trusted',
      'Trusted',
      'Part of the crew',
    ]);
  });

  it('says how far the next title is', () => {
    expect(assess(4, 12).next).toEqual({ title: 'Reliable', more: 2 });
    expect(assess(11, 12).next).toEqual({ title: 'Part of the crew', more: 1 });
    expect(assess(12, 12).next).toBeUndefined();
  });
});
