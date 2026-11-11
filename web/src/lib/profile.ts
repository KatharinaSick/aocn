/**
 * The profile card (/u/<login>): what the API says about someone, which doors to show as
 * fixed, and what POLARIS-7 makes of it.
 *
 * The card is the same for everyone who looks at it, so it can be cached. Whether it's your
 * own card (and "log out" shows) is asked afterwards, in the browser.
 */
import type { PolaMood } from './polaFace';

export interface Profile {
  login: string;
  avatarUrl: string;
  /** When they first logged in or sent a result. */
  since: Date;
  /** The doors they fixed, by door, with when they first passed. Only doors that are open. */
  solved: Map<number, Date>;
}

export type ProfileAnswer = { state: 'found'; profile: Profile } | { state: 'missing' } | { state: 'unavailable' };

/** What GitHub allows in a login; the API checks the same. */
const GITHUB_LOGIN = /^[A-Za-z0-9][A-Za-z0-9-]{0,38}$/;

/** Asks the API for someone's card. A login GitHub wouldn't allow is missing without asking. */
export async function fetchProfile(
  apiUrl: string,
  login: string,
  fetchFn: typeof fetch = fetch,
): Promise<ProfileAnswer> {
  if (!GITHUB_LOGIN.test(login)) {
    return { state: 'missing' };
  }
  let response: Response;
  try {
    response = await fetchFn(`${apiUrl}/users/${login}`, { headers: { Accept: 'application/json' } });
  } catch {
    return { state: 'unavailable' };
  }
  return readProfileAnswer(response.status, await response.json().catch(() => null));
}

/** Reads the API's answer to GET /users/{login}. An answer that makes no sense throws. */
export function readProfileAnswer(status: number, body: unknown): ProfileAnswer {
  if (status === 404) {
    return { state: 'missing' };
  }
  if (status >= 500 || status === 429) {
    return { state: 'unavailable' };
  }
  const b = (body ?? {}) as Record<string, unknown>;
  if (status !== 200 || typeof b.login !== 'string' || typeof b.since !== 'string' || !Array.isArray(b.doors)) {
    throw new Error(`The API answered ${status} for a profile`);
  }
  const solved = new Map<number, Date>();
  for (const d of b.doors as Record<string, unknown>[]) {
    if (typeof d?.door === 'number' && typeof d.solvedAt === 'string') {
      solved.set(d.door, new Date(d.solvedAt));
    }
  }
  return {
    state: 'found',
    profile: {
      login: b.login,
      avatarUrl: typeof b.avatarUrl === 'string' ? b.avatarUrl : '',
      since: new Date(b.since),
      solved,
    },
  };
}

export interface DoorCell {
  door: number;
  /** fixed: open and passed. open: open, not passed yet. closed: not open yet. */
  state: 'fixed' | 'open' | 'closed';
}

/** One cell per door in the schedule, by the doors' numbers, for the strip on the card. */
export function doorCells(
  solved: Map<number, Date>,
  doorNumbers: number[],
  isDoorOpen: (door: number, now: Date) => boolean,
  now: Date,
): DoorCell[] {
  return doorNumbers.map((door) => {
    if (!isDoorOpen(door, now)) {
      return { door, state: 'closed' };
    }
    return { door, state: solved.has(door) ? 'fixed' : 'open' };
  });
}

/** POLARIS-7's line on someone's card, from how many doors are open and how many they fixed. */
export function profileLine(cells: DoorCell[]): { mood: PolaMood; message: string } {
  const open = cells.filter((c) => c.state !== 'closed').length;
  const fixed = cells.filter((c) => c.state === 'fixed').length;
  if (open === 0) {
    return { mood: 'neutral', message: 'Nothing to fix yet. The crew is working on it.' };
  }
  if (fixed === 0) {
    return { mood: 'annoyed', message: 'No repairs on record. Yet.' };
  }
  if (fixed === cells.length) {
    return { mood: 'fond', message: 'Everything fixed. I am almost impressed.' };
  }
  if (fixed === open) {
    return { mood: 'fond', message: `Fixed everything the crew broke so far. They will break more.` };
  }
  return { mood: 'neutral', message: `Fixed ${fixed} of the ${open} things the crew broke.` };
}

/** How long the rendered card may stay in Netlify's cache: a new pass shows up after this. */
export const PROFILE_CDN_SECONDS = 60;

/** Cache headers for the rendered card. Browsers always ask again; Netlify keeps a copy. */
export function profileCacheHeaders(answer: ProfileAnswer): Record<string, string> {
  if (answer.state === 'unavailable') {
    return { 'Cache-Control': 'no-store' };
  }
  return {
    'Cache-Control': 'public, max-age=0, must-revalidate',
    'Netlify-CDN-Cache-Control': `public, s-maxage=${PROFILE_CDN_SECONDS}`,
  };
}

/** What POLARIS-7 makes of someone, by how many doors they fixed. */
export interface Assessment {
  title: string;
  mood: PolaMood;
  line: string;
  /** The next title and how many more doors it takes; none once every door is fixed. */
  next?: { title: string; more: number };
}

/** Her titles, from the fewest doors fixed. The last one takes every door. */
const TITLES: { from: number | 'all'; title: string; mood: PolaMood; line: string }[] = [
  { from: 0, title: 'Unverified human', mood: 'neutral', line: 'Has not fixed anything yet. Has not broken anything either.' },
  { from: 1, title: 'Occasionally useful', mood: 'neutral', line: 'Fixed something once. I made a note.' },
  { from: 3, title: 'Adequate', mood: 'neutral', line: 'You fix more than the crew breaks. That is rarer than it sounds.' },
  { from: 6, title: 'Reliable', mood: 'fond', line: 'I have stopped double-checking your work. Mostly.' },
  { from: 9, title: 'Trusted', mood: 'fond', line: 'I would let you near production. Unsupervised, even.' },
  { from: 'all', title: 'Part of the crew', mood: 'fond', line: 'You are one of them now. Please do not start breaking things.' },
];

/** Her assessment of someone who fixed `fixed` of `doorCount` doors. */
export function assess(fixed: number, doorCount: number): Assessment {
  const from = (t: (typeof TITLES)[number]) => (t.from === 'all' ? doorCount : t.from);
  const index = TITLES.findLastIndex((t) => fixed >= from(t));
  const { title, mood, line } = TITLES[index];
  const next = TITLES[index + 1];
  return next ? { title, mood, line, next: { title: next.title, more: from(next) - fixed } } : { title, mood, line };
}
