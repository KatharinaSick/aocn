/**
 * The door page (/doors/N): what the API says about a door, how long the rendered page may
 * be cached, and how a door's text becomes paragraphs.
 *
 * The API decides whether a door is open, on every request. This file never decides it; it
 * only reads the answer. Door content is never part of the build.
 */

/** What the website shows for an open door, from its page.yaml. */
export interface DoorPage {
  door: number;
  opensAt: Date;
  title: string;
  /** A few sentences; paragraphs are separated by a blank line. */
  story: string;
  /** What to fix, one short line each. */
  mission: string[];
  /** POLARIS-7's one sentence for the door, her prompt line on the page. */
  pola: string;
  videoId: string;
  /** Who the interview is with. */
  maintainer: Maintainer;
  /** A few links that help with the mission. */
  docs: Link[];
}

export interface Maintainer {
  name: string;
  role: string;
  /** Where to find them; empty if the API sent no https link. */
  url: string;
}

export interface Link {
  title: string;
  url: string;
}

/** The link if it's an absolute https URL, the only kind the page links to; otherwise ''. */
export function httpsOnly(url: unknown): string {
  if (typeof url !== 'string') return '';
  try {
    return new URL(url).protocol === 'https:' ? url : '';
  } catch {
    return '';
  }
}

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

export type DoorAnswer =
  /** preview: the door isn't open, and the session belongs to a previewer of it. */
  | { state: 'open'; page: DoorPage; preview: boolean }
  | { state: 'closed'; door: number; opensAt: Date }
  | { state: 'missing' }
  /** The door may be served (open, or to a previewer), but nothing was uploaded for it yet. */
  | { state: 'notReady'; door: number }
  /** The door may be open, but the API can't serve it right now: down, or its files unreadable. */
  | { state: 'unavailable' };

/** The session cookie's name, as the API sets it. */
export const SESSION_COOKIE = 'aocn_session';

/**
 * Asks the API about a door, if isDoor says there is one. With a session (the visitor's
 * session cookie), a previewer gets their door before it opens. An answer that makes no
 * sense throws.
 */
export async function fetchDoor(
  apiUrl: string,
  door: string,
  isDoor: (door: number) => boolean,
  { session, fetchFn = fetch }: { session?: string; fetchFn?: typeof fetch } = {},
): Promise<DoorAnswer> {
  // Only plain door numbers reach the API, so nothing else can be smuggled into its path.
  // The API accepts the same spelling only: 1, never 01. A door that isn't in the schedule
  // is missing without asking, so it's a 404 even while the API is down, never "stuck".
  if (!/^[1-9][0-9]?$/.test(door) || !isDoor(Number(door))) {
    return { state: 'missing' };
  }
  let response: Response;
  try {
    // Only the session cookie goes on, none of the visitor's other cookies.
    const headers: Record<string, string> = { Accept: 'application/json' };
    if (session) {
      headers.Cookie = `${SESSION_COOKIE}=${session}`;
    }
    response = await fetchFn(`${apiUrl}/doors/${door}`, { headers });
  } catch {
    return { state: 'unavailable' };
  }
  return readDoorAnswer(response.status, await response.json().catch(() => null));
}

/** Reads the API's answer to GET /doors/{n}. */
export function readDoorAnswer(status: number, body: unknown): DoorAnswer {
  const b = (body ?? {}) as Record<string, unknown>;
  if (status === 404) {
    if (typeof b.opensAt === 'string' && typeof b.door === 'number') {
      return { state: 'closed', door: b.door, opensAt: new Date(b.opensAt) };
    }
    return { state: 'missing' };
  }
  if (status === 503 && b.notReady === true && typeof b.door === 'number') {
    return { state: 'notReady', door: b.door };
  }
  if (status >= 500 || status === 429) {
    return { state: 'unavailable' };
  }
  if (status !== 200 || typeof b.title !== 'string' || typeof b.story !== 'string' || typeof b.door !== 'number') {
    throw new Error(`The API answered ${status} for a door`);
  }
  return {
    state: 'open',
    preview: b.preview === true,
    page: {
      door: b.door,
      opensAt: new Date(String(b.opensAt)),
      title: b.title,
      story: b.story,
      mission: Array.isArray(b.mission) ? b.mission.filter((goal): goal is string => typeof goal === 'string') : [],
      pola: text(b.pola),
      videoId: text(b.videoId),
      maintainer: readMaintainer(b.maintainer),
      docs: Array.isArray(b.docs) ? b.docs.map(readLink).filter((link) => link.title && link.url) : [],
    },
  };
}

function readMaintainer(value: unknown): Maintainer {
  const m = (value ?? {}) as Record<string, unknown>;
  return { name: text(m.name), role: text(m.role), url: httpsOnly(m.url) };
}

function readLink(value: unknown): Link {
  const l = (value ?? {}) as Record<string, unknown>;
  return { title: text(l.title), url: httpsOnly(l.url) };
}

/** How long an open door's page may stay in Netlify's cache: a late fix shows up after this. */
export const OPEN_DOOR_CDN_SECONDS = 300;

/** How long a door that isn't in the schedule may stay in Netlify's cache. */
export const MISSING_DOOR_CDN_SECONDS = 60;

/**
 * Cache headers for the rendered door page. Browsers always ask again; Netlify keeps a copy
 * of what's the same for everyone.
 *
 * A closed door isn't kept: a cached copy would be served to a previewer too, who'd never
 * reach the function and see their door. A preview is only for the one previewer, so it's
 * kept nowhere, neither by Netlify nor by the browser.
 */
export function doorCacheHeaders(answer: DoorAnswer): Record<string, string> {
  let seconds: number;
  switch (answer.state) {
    case 'open':
      if (answer.preview) {
        return { 'Cache-Control': 'private, no-store', 'Netlify-CDN-Cache-Control': 'no-store' };
      }
      seconds = OPEN_DOOR_CDN_SECONDS;
      break;
    case 'missing':
      seconds = MISSING_DOOR_CDN_SECONDS;
      break;
    case 'closed':
    case 'notReady':
    case 'unavailable':
      // The next request should ask again: it may be a previewer's, or work this time.
      return { 'Cache-Control': 'no-store' };
  }
  return {
    'Cache-Control': 'public, max-age=0, must-revalidate',
    'Netlify-CDN-Cache-Control': `public, s-maxage=${seconds}`,
  };
}

/**
 * A door's text as paragraphs. page.yaml separates paragraphs with a blank line and wraps
 * lines inside them for readability; those line breaks are not part of the text.
 */
export function paragraphs(text: string): string[] {
  return text
    .split(/\n\s*\n/)
    .map((p) => p.trim().replace(/\s*\n\s*/g, ' '))
    .filter((p) => p !== '');
}

/** The description for search and social previews: the story's first paragraph, cut to fit. */
export function doorDescription(story: string, max = 160): string {
  const first = paragraphs(story)[0] ?? '';
  if (first.length <= max) {
    return first;
  }
  const cut = first.slice(0, max - 1);
  return `${cut.slice(0, cut.lastIndexOf(' ') > 0 ? cut.lastIndexOf(' ') : cut.length)}…`;
}

/** The repository players' Codespaces are made from. */
const PLAY_REPO = 'KatharinaSick/aocn';

/**
 * The "play" link: a new Codespace from the door's own devcontainer.json, which opens the door
 * by itself. quickstart offers the player's existing Codespace for that door instead of a
 * second one.
 */
export function playUrl(door: number): string {
  const config = `.devcontainer/door-${String(door).padStart(2, '0')}/devcontainer.json`;
  const params = new URLSearchParams({ devcontainer_path: config, quickstart: '1' });
  return `https://codespaces.new/${PLAY_REPO}?${params}`;
}

/**
 * The interview's player. youtube-nocookie, and only loaded once the visitor asks for it, so
 * the page itself sends nothing to YouTube.
 */
export function videoEmbedUrl(videoId: string): string {
  return `https://www.youtube-nocookie.com/embed/${encodeURIComponent(videoId)}?autoplay=1&rel=0`;
}

/**
 * The interview's thumbnail from YouTube: the large one, or the one every video has when there
 * is no large one. null if YouTube has neither or can't be reached.
 */
export async function fetchThumbnail(videoId: string, fetchFn: typeof fetch = fetch): Promise<ArrayBuffer | null> {
  for (const name of ['maxresdefault', 'hqdefault']) {
    try {
      const response = await fetchFn(`https://i.ytimg.com/vi/${encodeURIComponent(videoId)}/${name}.jpg`);
      if (response.ok) {
        return await response.arrayBuffer();
      }
    } catch {
      return null;
    }
  }
  return null;
}
