import { describe, expect, it, vi } from 'vitest';
import {
  doorCacheHeaders,
  doorDescription,
  fetchDoor,
  fetchThumbnail,
  paragraphs,
  playUrl,
  readDoorAnswer,
  videoEmbedUrl,
} from './doorPage';

const opensAt = new Date('2026-12-01T05:00:00Z');
const openBody = {
  door: 1,
  opensAt: '2026-12-01T06:00:00+01:00',
  title: 'Door',
  story: 'One.',
  mission: ['First goal', 'Second goal'],
  pola: 'Noted.',
  videoId: '',
  maintainer: { name: 'Jane Doe', role: 'Example maintainer', url: 'https://example.com/jane' },
  docs: [{ title: 'Example docs', url: 'https://example.com/docs' }],
};

describe('readDoorAnswer', () => {
  it('reads an open door', () => {
    expect(readDoorAnswer(200, openBody)).toEqual({
      state: 'open',
      preview: false,
      page: {
        door: 1,
        opensAt,
        title: 'Door',
        story: 'One.',
        mission: ['First goal', 'Second goal'],
        pola: 'Noted.',
        videoId: '',
        maintainer: { name: 'Jane Doe', role: 'Example maintainer', url: 'https://example.com/jane' },
        docs: [{ title: 'Example docs', url: 'https://example.com/docs' }],
      },
    });
  });

  it('reads what an older API leaves out as empty', () => {
    const answer = readDoorAnswer(200, { door: 1, opensAt: openBody.opensAt, title: 'Door', story: 'One.', mission: ['a', 2] });
    expect(answer.state === 'open' && answer.page).toMatchObject({
      mission: ['a'],
      pola: '',
      videoId: '',
      maintainer: { name: '', role: '', url: '' },
      docs: [],
    });
  });

  it('only keeps https links', () => {
    const answer = readDoorAnswer(200, {
      ...openBody,
      maintainer: { name: 'Jane', role: 'r', url: 'javascript:alert(1)' },
      docs: [
        { title: 'fine', url: 'https://example.com' },
        { title: 'plain http', url: 'http://example.com' },
        { title: 'no url' },
      ],
    });
    expect(answer.state === 'open' && answer.page.maintainer.url).toBe('');
    expect(answer.state === 'open' && answer.page.docs).toEqual([{ title: 'fine', url: 'https://example.com' }]);
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
    [429, { error: 'slow down' }],
  ])('reads %i as unavailable', (status, body) => {
    expect(readDoorAnswer(status, body)).toEqual({ state: 'unavailable' });
  });

  it.each([
    [200, { door: 1 }],
    [400, null],
  ])('throws on %i', (status, body) => {
    expect(() => readDoorAnswer(status, body)).toThrow();
  });
});

/** Doors every other day, like the real season. */
const isDoor = (door: number) => door % 2 === 1 && door <= 23;

describe('fetchDoor', () => {
  const answering = (status: number, body: unknown) =>
    vi.fn(async () => new Response(JSON.stringify(body), { status })) as unknown as typeof fetch;

  it('asks the API for the door', async () => {
    const fetchFn = answering(200, openBody);
    expect((await fetchDoor('http://api', '1', isDoor, { fetchFn })).state).toBe('open');
    expect(fetchFn).toHaveBeenCalledWith('http://api/doors/1', expect.anything());
  });

  it('passes the session on, and only the session', async () => {
    const fetchFn = answering(200, openBody);
    await fetchDoor('http://api', '1', isDoor, { session: 'signed.jwt.value', fetchFn });
    expect(fetchFn).toHaveBeenCalledWith('http://api/doors/1', {
      headers: { Accept: 'application/json', Cookie: 'aocn_session=signed.jwt.value' },
    });
    const anonymous = answering(200, openBody);
    await fetchDoor('http://api', '1', isDoor, { fetchFn: anonymous });
    expect(anonymous).toHaveBeenCalledWith('http://api/doors/1', { headers: { Accept: 'application/json' } });
  });

  it('reads an API it cannot reach as unavailable', async () => {
    const fetchFn = vi.fn(async () => {
      throw new TypeError('fetch failed');
    }) as unknown as typeof fetch;
    expect(await fetchDoor('http://api', '1', isDoor, { fetchFn })).toEqual({ state: 'unavailable' });
  });

  // 2 is a day without a door, 25 is after the last.
  it.each(['0', '01', '+1', '1x', '../health', '-1', '100', '', '2', '25', '50'])('never asks the API about %j', async (door) => {
    const fetchFn = answering(200, openBody);
    expect(await fetchDoor('http://api', door, isDoor, { fetchFn })).toEqual({ state: 'missing' });
    expect(fetchFn).not.toHaveBeenCalled();
  });
});

describe('a door that is not ready', () => {
  it('is told apart from an outage', () => {
    const body = { error: "door 3 isn't ready yet", door: 3, notReady: true };
    expect(readDoorAnswer(503, body)).toEqual({ state: 'notReady', door: 3 });
    expect(readDoorAnswer(503, { error: 'try again in a few minutes' })).toEqual({ state: 'unavailable' });
    expect(doorCacheHeaders({ state: 'notReady', door: 3 })).toEqual({ 'Cache-Control': 'no-store' });
  });
});

describe('doorCacheHeaders', () => {
  const closed = { state: 'closed' as const, door: 1, opensAt };
  const open = readDoorAnswer(200, openBody);
  const preview = readDoorAnswer(200, { ...openBody, preview: true });

  // Netlify would serve a cached closed door to a previewer too, who'd never see theirs.
  it('never caches a closed door', () => {
    expect(doorCacheHeaders(closed)).toEqual({ 'Cache-Control': 'no-store' });
  });

  it('keeps a preview nowhere, not even in the browser', () => {
    expect(preview).toMatchObject({ state: 'open', preview: true });
    expect(doorCacheHeaders(preview)).toEqual({
      'Cache-Control': 'private, no-store',
      'Netlify-CDN-Cache-Control': 'no-store',
    });
  });

  it('never caches an unavailable door', () => {
    expect(doorCacheHeaders({ state: 'unavailable' })).toEqual({ 'Cache-Control': 'no-store' });
  });

  it('keeps an open door longer', () => {
    expect(doorCacheHeaders(open)['Netlify-CDN-Cache-Control']).toBe('public, s-maxage=300');
  });

  it('keeps a door that is not in the schedule a minute', () => {
    expect(doorCacheHeaders({ state: 'missing' })['Netlify-CDN-Cache-Control']).toBe('public, s-maxage=60');
  });

  it('never lets browsers keep a copy', () => {
    for (const answer of [open, { state: 'missing' as const }]) {
      const headers = doorCacheHeaders(answer);
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

describe('playUrl', () => {
  it("opens a Codespace from the door's own config, reusing one the player has", () => {
    const url = new URL(playUrl(1));
    expect(url.origin + url.pathname).toBe('https://codespaces.new/KatharinaSick/aocn');
    expect(url.searchParams.get('devcontainer_path')).toBe('.devcontainer/door-01/devcontainer.json');
    expect(url.searchParams.get('quickstart')).toBe('1');
    expect(new URL(playUrl(23)).searchParams.get('devcontainer_path')).toBe('.devcontainer/door-23/devcontainer.json');
  });
});

describe('videoEmbedUrl', () => {
  it('uses the no-cookie player and keeps the ID in its place', () => {
    expect(videoEmbedUrl('abc_-1')).toBe('https://www.youtube-nocookie.com/embed/abc_-1?autoplay=1&rel=0');
    expect(videoEmbedUrl('a/../b?c')).toBe('https://www.youtube-nocookie.com/embed/a%2F..%2Fb%3Fc?autoplay=1&rel=0');
  });
});

describe('fetchThumbnail', () => {
  it('takes the large thumbnail when there is one', async () => {
    const fetchFn = vi.fn(async () => new Response('large'));
    const image = await fetchThumbnail('abc', fetchFn as unknown as typeof fetch);
    expect(new TextDecoder().decode(image!)).toBe('large');
    expect(fetchFn).toHaveBeenCalledWith('https://i.ytimg.com/vi/abc/maxresdefault.jpg');
  });

  it('falls back to the one every video has', async () => {
    const fetchFn = vi.fn(async (url: string) =>
      url.endsWith('maxresdefault.jpg') ? new Response(null, { status: 404 }) : new Response('small'),
    );
    const image = await fetchThumbnail('abc', fetchFn as unknown as typeof fetch);
    expect(new TextDecoder().decode(image!)).toBe('small');
  });

  it('is null when YouTube has none or is unreachable', async () => {
    const missing = vi.fn(async () => new Response(null, { status: 404 }));
    expect(await fetchThumbnail('abc', missing as unknown as typeof fetch)).toBeNull();
    const down = vi.fn(async () => {
      throw new Error('offline');
    });
    expect(await fetchThumbnail('abc', down as unknown as typeof fetch)).toBeNull();
  });
});
