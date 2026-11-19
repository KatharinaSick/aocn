import { describe, expect, it } from 'vitest';
import {
  boardIdFromPath,
  boardMeta,
  callApi,
  cleanName,
  deadlineDropsFixes,
  endOfMinute,
  doorCells,
  formatDeadline,
  fromLocalInput,
  inviteUrl,
  joinCodeFromPath,
  readAnswer,
  playerName,
  readBoard,
  rowKey,
  readJoinPreview,
  readMyBoards,
  readNothing,
  skippedLocalTime,
  toLocalInput,
} from './leaderboards';

const ID = 'AbCdEfGh12345_-x';

describe('paths', () => {
  it('finds a board ID and a join code, and nothing else', () => {
    expect(boardIdFromPath(`/leaderboards/${ID}`)).toBe(ID);
    expect(boardIdFromPath(`/leaderboards/${ID}/`)).toBe(ID);
    expect(joinCodeFromPath(`/join/${ID}`)).toBe(ID);
    for (const path of ['/leaderboards/', '/leaderboards/board', `/leaderboards/${ID}x`, `/leaderboards/${ID}/edit`, `/join/${ID}/../x`, '/join/', `/u/${ID}`]) {
      expect(boardIdFromPath(path)).toBeNull();
      expect(joinCodeFromPath(path)).toBeNull();
    }
  });

  it('builds an absolute invite link', () => {
    expect(inviteUrl('https://aocn.dev', ID)).toBe(`https://aocn.dev/join/${ID}`);
  });
});

describe('cleanName', () => {
  it('trims, and takes 1 to 40 characters on one line', () => {
    expect(cleanName('  Platform Team ')).toBe('Platform Team');
    expect(cleanName('ö'.repeat(40))).toBe('ö'.repeat(40));
    expect(cleanName('🎄'.repeat(40))).toBe('🎄'.repeat(40));
    for (const name of ['', '   ', 'x'.repeat(41), 'two\nlines', 'a\tb', 'a b']) {
      expect(cleanName(name)).toBeNull();
    }
  });
});

describe('readAnswer', () => {
  const read = (b: Record<string, unknown>) => (typeof b.x === 'number' ? b.x : null);
  it('tells the cases apart', () => {
    expect(readAnswer(200, { x: 1 }, read)).toEqual({ state: 'ok', value: 1 });
    expect(readAnswer(200, { y: 1 }, read)).toEqual({ state: 'unavailable' });
    expect(readAnswer(200, null, read)).toEqual({ state: 'unavailable' });
    expect(readAnswer(401, { error: 'not logged in' }, read)).toEqual({ state: 'loggedOut' });
    expect(readAnswer(404, { error: 'no such leaderboard' }, read)).toEqual({ state: 'missing', message: 'no such leaderboard' });
    expect(readAnswer(409, { error: 'full' }, read)).toEqual({ state: 'refused', message: 'full' });
    expect(readAnswer(409, null, read)).toEqual({ state: 'unavailable' });
    expect(readAnswer(503, { error: 'try again' }, read)).toEqual({ state: 'unavailable' });
    expect(readAnswer(429, { error: 'slow down' }, read)).toEqual({ state: 'unavailable' });
  });
});

describe('callApi', () => {
  it('sends changes as JSON to /api, and reads the answer', async () => {
    const calls: [string, RequestInit][] = [];
    const fetchFn = (async (url: string, init: RequestInit) => {
      calls.push([url, init]);
      return new Response('{"id":"x"}', { status: 201 });
    }) as unknown as typeof fetch;
    const answer = await callApi('POST', '/me/leaderboards', (b) => String(b.id), { body: { name: 'n' }, fetchFn });
    expect(answer).toEqual({ state: 'ok', value: 'x' });
    expect(calls[0][0]).toBe('/api/me/leaderboards');
    expect(calls[0][1].method).toBe('POST');
    expect((calls[0][1].headers as Record<string, string>)['Content-Type']).toBe('application/json');
    expect(calls[0][1].body).toBe('{"name":"n"}');
  });

  it('reads an empty answer, and turns a network failure into unavailable', async () => {
    const empty = (async () => new Response(null, { status: 204 })) as unknown as typeof fetch;
    expect(await callApi('DELETE', '/leaderboards/x', readNothing, { fetchFn: empty })).toEqual({ state: 'ok', value: true });
    const down = (async () => {
      throw new TypeError('offline');
    }) as unknown as typeof fetch;
    expect(await callApi('GET', '/me/leaderboards', readMyBoards, { fetchFn: down })).toEqual({ state: 'unavailable' });
    const html = (async () => new Response('<html>', { status: 502 })) as unknown as typeof fetch;
    expect(await callApi('GET', '/me/leaderboards', readMyBoards, { fetchFn: html })).toEqual({ state: 'unavailable' });
  });
});

const board = {
  id: ID,
  name: 'Platform Team',
  public: false,
  openJoin: false,
  countsUntil: '2026-12-31T22:59:00Z',
  final: false,
  owner: { login: 'alice' },
  members: 3,
  maxMembers: 200,
  you: { loggedIn: true, member: true, owner: false },
  joinCode: 'code000000000000',
  doors: [
    { door: 1, opensAt: '2026-12-01T05:00:00Z', counts: true },
    { door: 3, opensAt: '2026-12-03T05:00:00Z', counts: true },
    { door: 5, opensAt: '2026-12-05T05:00:00Z', counts: false },
  ],
  standings: [
    { rank: 1, id: 1, login: 'alice', profilePublic: true, you: false, owner: true, points: 195, failedVerifies: 1, fixed: 2, doors: [{ door: 1, points: 95, hints: 1, failedVerifies: 1 }, { door: 3, points: 100, hints: 0, failedVerifies: 0 }] },
    { rank: 2, id: 2, login: 'bob', profilePublic: false, you: true, owner: false, points: 0, failedVerifies: 0, fixed: 0, doors: [] },
    { rank: 3, private: true, login: 'ignored', profilePublic: true, points: 0, doors: [] },
    { login: '' },
  ],
  together: 2,
  removed: [{ id: 4, login: 'mallory', removedAt: '2026-12-02T10:00:00Z' }, { id: 0, login: 'nobody' }],
};

describe('readBoard', () => {
  it('reads a board, and leaves out what makes no sense', () => {
    const b = readBoard(board)!;
    expect(b.name).toBe('Platform Team');
    expect(b.countsUntil?.toISOString()).toBe('2026-12-31T22:59:00.000Z');
    expect(b.you).toEqual({ loggedIn: true, member: true, owner: false });
    expect(b.joinCode).toBe('code000000000000');
    expect(b.standings).toHaveLength(3);
    // A private row has no name to show and no card to link to, whatever else it says.
    expect(b.standings[2]).toMatchObject({ private: true, profilePublic: false, login: '', id: 0 });
    expect(playerName(b.standings[2])).toBe('private');
    expect(playerName(b.standings[0])).toBe('@alice');
    expect(rowKey(b.standings[2], 2)).toBe('private-2');
    expect(rowKey(b.standings[0], 0)).toBe('1');
    expect(b.standings[0].doors[0]).toEqual({ door: 1, points: 95, hints: 1, failedVerifies: 1 });
    expect(readBoard({ ...board, joinCode: undefined, countsUntil: null })).toMatchObject({ joinCode: null, countsUntil: null });
    expect(b.removed).toEqual([{ id: 4, login: 'mallory', removedAt: new Date('2026-12-02T10:00:00Z') }]);
    expect(readBoard({ ...board, removed: undefined })!.removed).toEqual([]);
    expect(readBoard({ name: 'x' })).toBeNull();
  });
});

describe('readMyBoards and readJoinPreview', () => {
  it('reads them', () => {
    const mine = readMyBoards({ leaderboards: [{ id: ID, name: 'x', own: true, members: 2, rank: 1, countsUntil: null }, { id: '' }], owned: 1, maxOwned: 3 })!;
    expect(mine.boards).toEqual([{ id: ID, name: 'x', public: false, own: true, members: 2, countsUntil: null, final: false, rank: 1 }]);
    expect(mine.owned).toBe(1);
    expect(readMyBoards({})).toBeNull();
    const preview = readJoinPreview({ name: 'x', owner: { login: 'alice' }, members: 2, maxMembers: 200, full: false, member: false })!;
    expect(preview.id).toBeNull();
    expect(preview.owner.login).toBe('alice');
    expect(readJoinPreview({})).toBeNull();
  });
});

describe('deadlines', () => {
  it('writes one in the given time zone, with its name', () => {
    const d = new Date('2026-12-31T22:59:00Z');
    expect(formatDeadline(d, 'Europe/Vienna')).toBe('dec 31, 23:59 CET');
    expect(formatDeadline(d, 'UTC')).toBe('dec 31, 22:59 UTC');
    expect(formatDeadline(d, 'America/Los_Angeles')).toBe('dec 31, 14:59 GMT-8');
    // The day is the viewer's too.
    expect(formatDeadline(d, 'Asia/Tokyo')).toBe('jan 1, 07:59 GMT+9');
  });

  it('round-trips a datetime-local value', () => {
    const d = fromLocalInput('2026-12-31T23:59')!;
    expect(d.getFullYear()).toBe(2026);
    expect(d.getMonth()).toBe(11);
    expect(d.getHours()).toBe(23);
    expect(toLocalInput(d)).toBe('2026-12-31T23:59');
    for (const value of ['', '2026-12-31', '2026-02-30T10:00', '2026-12-31T25:00', 'tomorrow']) {
      expect(fromLocalInput(value)).toBeNull();
    }
  });

  it('tells a time the clocks skip from one that isn\'t a time', () => {
    const tz = process.env.TZ;
    // Node picks up a new TZ right away. Vienna springs forward at 02:00 on March 29, 2026.
    process.env.TZ = 'Europe/Vienna';
    try {
      expect(fromLocalInput('2026-03-29T02:30')).toBeNull();
      expect(skippedLocalTime('2026-03-29T02:30')).toBe(true);
      expect(skippedLocalTime('2026-03-29T03:30')).toBe(false);
      for (const value of ['', '2026-02-30T10:00', '2026-12-31T25:00', 'tomorrow']) {
        expect(skippedLocalTime(value)).toBe(false);
      }
    } finally {
      if (tz === undefined) delete process.env.TZ;
      else process.env.TZ = tz;
    }
  });

  it('takes the whole picked minute', () => {
    const d = endOfMinute(new Date('2026-12-31T22:59:00Z'));
    expect(d.toISOString()).toBe('2026-12-31T22:59:59.999Z');
    expect(toLocalInput(d)).toBe(toLocalInput(new Date('2026-12-31T22:59:00Z')));
  });

  it('knows when a change drops fixes that count now', () => {
    const dec20 = new Date('2026-12-20T22:59:00Z');
    const dec31 = new Date('2026-12-31T22:59:00Z');
    expect(deadlineDropsFixes(null, dec31)).toBe(true);
    expect(deadlineDropsFixes(dec31, dec20)).toBe(true);
    expect(deadlineDropsFixes(dec20, dec31)).toBe(false);
    expect(deadlineDropsFixes(dec31, dec31)).toBe(false);
    expect(deadlineDropsFixes(dec31, null)).toBe(false);
  });

  it('says it in the line under the name', () => {
    const until = new Date('2026-12-31T22:59:00Z');
    expect(boardMeta({ members: 1, public: false, countsUntil: null, final: false })).toBe('1 member · private');
    expect(boardMeta({ members: 12, public: true, countsUntil: until, final: false }, 'UTC')).toBe('12 members · public · counts until dec 31, 22:59 UTC');
    expect(boardMeta({ members: 12, public: false, countsUntil: until, final: true }, 'UTC')).toBe('12 members · private · final standings, ended dec 31, 22:59 UTC');
  });
});

describe('doorCells', () => {
  it('has one cell per door, in order, and says what each one means', () => {
    const b = readBoard(board)!;
    const now = new Date('2026-12-04T00:00:00Z');
    expect(doorCells(b.doors, b.standings[0], now)).toEqual([
      { door: 1, state: 'fixed', label: 'door 1: fixed, 95 points, 1 hint, 1 failed verify' },
      { door: 3, state: 'fixed', label: 'door 3: fixed, 100 points, 0 hints, 0 failed verifies' },
      { door: 5, state: 'closed', label: 'door 5: not open yet' },
    ]);
    expect(doorCells(b.doors, b.standings[1], now).map((c) => c.state)).toEqual(['open', 'open', 'closed']);
    // Open, but after the board's deadline.
    expect(doorCells(b.doors, b.standings[1], new Date('2026-12-06T00:00:00Z'))[2].label).toBe('door 5: opened after the deadline');
  });
});
