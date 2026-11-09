import { describe, expect, it, vi } from 'vitest';
import { doorCacheHeaders, doorDescription, fetchDoor, paragraphs, readDoorAnswer } from './doorPage';

const opensAt = new Date('2026-12-01T05:00:00Z');
const openBody = {
  door: 1,
  opensAt: '2026-12-01T06:00:00+01:00',
  title: 'Door',
  story: 'One.',
  log: 'Noted.',
  videoId: '',
};

describe('readDoorAnswer', () => {
  it('reads an open door', () => {
    expect(readDoorAnswer(200, openBody)).toEqual({
      state: 'open',
      page: { door: 1, opensAt, title: 'Door', story: 'One.', log: 'Noted.', videoId: '' },
    });
  });

  it('reads a closed door with its opening time', () => {
    expect(readDoorAnswer(404, { door: 2, opensAt: '2026-12-03T06:00:00+01:00' })).toEqual({
      state: 'closed',
      door: 2,
      opensAt: new Date('2026-12-03T05:00:00Z'),
    });
  });

  it('reads a 404 without an opening time as no such door', () => {
    expect(readDoorAnswer(404, { error: 'no such door' })).toEqual({ state: 'missing' });
    expect(readDoorAnswer(404, null)).toEqual({ state: 'missing' });
  });

  it.each([
    [503, { error: 'try again' }],
    [500, null],
    [200, { door: 1 }],
  ])('throws on %i', (status, body) => {
    expect(() => readDoorAnswer(status, body)).toThrow();
  });
});

describe('fetchDoor', () => {
  const answering = (status: number, body: unknown) =>
    vi.fn(async () => new Response(JSON.stringify(body), { status })) as unknown as typeof fetch;

  it('asks the API for the door', async () => {
    const fetchFn = answering(200, openBody);
    expect((await fetchDoor('http://api', '1', fetchFn)).state).toBe('open');
    expect(fetchFn).toHaveBeenCalledWith('http://api/doors/1', expect.anything());
  });

  it.each(['0', '01', '1x', '../health', '-1', '100', ''])('never asks the API about %j', async (door) => {
    const fetchFn = answering(200, openBody);
    expect(await fetchDoor('http://api', door, fetchFn)).toEqual({ state: 'missing' });
    expect(fetchFn).not.toHaveBeenCalled();
  });
});

describe('doorCacheHeaders', () => {
  const closed = { state: 'closed' as const, door: 1, opensAt };

  it('keeps a closed door at most a minute', () => {
    const headers = doorCacheHeaders(closed, new Date(opensAt.getTime() - 3600_000));
    expect(headers['Netlify-CDN-Cache-Control']).toBe('public, s-maxage=60');
  });

  it('never keeps a closed door past its opening', () => {
    const headers = doorCacheHeaders(closed, new Date(opensAt.getTime() - 20_500));
    expect(headers['Netlify-CDN-Cache-Control']).toBe('public, s-maxage=20');
  });

  it.each([0, 999, -5000])('does not cache a closed door %i ms before its opening', (ms) => {
    expect(doorCacheHeaders(closed, new Date(opensAt.getTime() - ms))).toEqual({ 'Cache-Control': 'no-store' });
  });

  it('keeps an open door longer', () => {
    const open = readDoorAnswer(200, openBody);
    expect(doorCacheHeaders(open, opensAt)['Netlify-CDN-Cache-Control']).toBe('public, s-maxage=300');
  });

  it('never lets browsers keep a copy', () => {
    for (const answer of [closed, readDoorAnswer(200, openBody), { state: 'missing' as const }]) {
      const headers = doorCacheHeaders(answer, new Date(opensAt.getTime() - 3600_000));
      expect(headers['Cache-Control']).toBe('public, max-age=0, must-revalidate');
    }
  });
});

describe('paragraphs', () => {
  it('splits on blank lines and joins wrapped lines', () => {
    expect(paragraphs('One line\nwrapped.\n\nTwo.\n   \nThree.\n')).toEqual(['One line wrapped.', 'Two.', 'Three.']);
  });

  it('has nothing for an empty text', () => {
    expect(paragraphs('  \n')).toEqual([]);
  });
});

describe('doorDescription', () => {
  it('is the first paragraph', () => {
    expect(doorDescription('First\nparagraph.\n\nSecond.')).toBe('First paragraph.');
  });

  it('cuts a long paragraph at a word', () => {
    expect(doorDescription('aaa bbb ccc ddd', 10)).toBe('aaa bbb…');
  });
});
