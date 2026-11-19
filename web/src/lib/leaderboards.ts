/**
 * Leaderboards (/leaderboards, /leaderboards/<id>, /join/<code>): reading what the API says,
 * and the rules for showing it. The API decides everything about who may see or change a
 * board; this only shows its answers, and asks it again for every change.
 *
 * The pages are prerendered once and load their board in the browser, so nothing personal is
 * ever in a cached page. Netlify serves every /leaderboards/<id> the one prerendered board page
 * (netlify.toml), and every /join/<code> the join page; `astro dev` does the same
 * (plugins/rewrites.ts).
 */

/** What an ID or a join code looks like; the API checks the same. */
const TOKEN = '[A-Za-z0-9_-]{16}';

/** The longest name a leaderboard may have, in characters; the API checks the same. */
export const NAME_MAX = 40;

export interface BoardSummary {
  id: string;
  name: string;
  public: boolean;
  /** Whether it's yours. */
  own: boolean;
  members: number;
  countsUntil: Date | null;
  final: boolean;
  /** Your place on it, or the card's owner's on a card. */
  rank: number;
}

export interface MyBoards {
  boards: BoardSummary[];
  /** How many are your own, and how many you may have. */
  owned: number;
  maxOwned: number;
}

export interface StandingDoor {
  door: number;
  points: number;
  hints: number;
  failedVerifies: number;
}

export interface Standing {
  rank: number;
  /** Their GitHub user ID, which removing them takes. */
  id: number;
  login: string;
  avatarUrl: string;
  /** Whether their card may be linked to. */
  profilePublic: boolean;
  /**
   * Who they are is left out: their card is private, and you see a public board without
   * being on it. id and login are empty then.
   */
  private: boolean;
  you: boolean;
  owner: boolean;
  points: number;
  failedVerifies: number;
  fixed: number;
  /** The doors they fixed. */
  doors: StandingDoor[];
}

export interface BoardDoor {
  door: number;
  opensAt: Date;
  /** Whether fixing it counts on this board now: open, and opened before the deadline. */
  counts: boolean;
}

/** Someone the owner removed: they can't join again until the owner lets them back in. */
export interface RemovedMember {
  id: number;
  login: string;
  removedAt: Date | null;
}

export interface Board {
  id: string;
  name: string;
  public: boolean;
  openJoin: boolean;
  countsUntil: Date | null;
  final: boolean;
  owner: { login: string; avatarUrl: string };
  members: number;
  maxMembers: number;
  you: { loggedIn: boolean; member: boolean; owner: boolean };
  /** For the invite link; only members get it. */
  joinCode: string | null;
  doors: BoardDoor[];
  standings: Standing[];
  together: number;
  /** Who the owner removed; only the owner gets it. */
  removed: RemovedMember[];
}

export interface JoinPreview {
  /** Only once you may see the board: it's public, or you're in it. */
  id: string | null;
  name: string;
  public: boolean;
  countsUntil: Date | null;
  final: boolean;
  owner: { login: string; avatarUrl: string };
  members: number;
  maxMembers: number;
  full: boolean;
  member: boolean;
}

/**
 * The API's answer: the thing, or why not. missing is a 404: a board that doesn't exist and
 * one you may not see look the same. refused carries the API's own sentence.
 */
export type Answer<T> =
  | { state: 'ok'; value: T }
  | { state: 'missing'; message: string }
  | { state: 'loggedOut' }
  | { state: 'refused'; message: string }
  | { state: 'unavailable' };

type Json = Record<string, unknown>;

const isRecord = (v: unknown): v is Json => typeof v === 'object' && v !== null && !Array.isArray(v);
const str = (v: unknown): string => (typeof v === 'string' ? v : '');
const num = (v: unknown): number => (typeof v === 'number' && Number.isFinite(v) ? v : 0);
const bool = (v: unknown): boolean => v === true;
const date = (v: unknown): Date | null => {
  if (typeof v !== 'string') return null;
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? null : d;
};
const person = (v: unknown) => (isRecord(v) ? { login: str(v.login), avatarUrl: str(v.avatarUrl) } : { login: '', avatarUrl: '' });

/** Turns an API answer into an Answer, with read making sense of a 2xx body. */
export function readAnswer<T>(status: number, body: unknown, read: (body: Json) => T | null): Answer<T> {
  const message = isRecord(body) ? str(body.error) : '';
  if (status >= 200 && status < 300) {
    const value = isRecord(body) ? read(body) : null;
    return value === null ? { state: 'unavailable' } : { state: 'ok', value };
  }
  if (status === 401) return { state: 'loggedOut' };
  if (status === 404) return { state: 'missing', message };
  if (status >= 500 || status === 429 || !message) return { state: 'unavailable' };
  return { state: 'refused', message };
}

export function readSummary(b: unknown): BoardSummary | null {
  if (!isRecord(b) || !str(b.id) || !str(b.name)) return null;
  return {
    id: str(b.id),
    name: str(b.name),
    public: bool(b.public),
    own: bool(b.own),
    members: num(b.members),
    countsUntil: date(b.countsUntil),
    final: bool(b.final),
    rank: num(b.rank),
  };
}

export function readMyBoards(b: Json): MyBoards | null {
  if (!Array.isArray(b.leaderboards)) return null;
  return {
    boards: b.leaderboards.map(readSummary).filter((s): s is BoardSummary => s !== null),
    owned: num(b.owned),
    maxOwned: num(b.maxOwned),
  };
}

function readStanding(s: unknown): Standing | null {
  if (!isRecord(s) || !(str(s.login) || bool(s.private))) return null;
  const doors = Array.isArray(s.doors) ? s.doors.filter(isRecord) : [];
  return {
    rank: num(s.rank),
    id: bool(s.private) ? 0 : num(s.id),
    login: bool(s.private) ? '' : str(s.login),
    avatarUrl: str(s.avatarUrl),
    profilePublic: bool(s.profilePublic) && !bool(s.private),
    private: bool(s.private),
    you: bool(s.you),
    owner: bool(s.owner),
    points: num(s.points),
    failedVerifies: num(s.failedVerifies),
    fixed: num(s.fixed),
    doors: doors.map((d) => ({ door: num(d.door), points: num(d.points), hints: num(d.hints), failedVerifies: num(d.failedVerifies) })),
  };
}

export function readBoard(b: Json): Board | null {
  if (!str(b.id) || !Array.isArray(b.standings) || !Array.isArray(b.doors)) return null;
  const you = isRecord(b.you) ? b.you : {};
  return {
    id: str(b.id),
    name: str(b.name),
    public: bool(b.public),
    openJoin: bool(b.openJoin),
    countsUntil: date(b.countsUntil),
    final: bool(b.final),
    owner: person(b.owner),
    members: num(b.members),
    maxMembers: num(b.maxMembers),
    you: { loggedIn: bool(you.loggedIn), member: bool(you.member), owner: bool(you.owner) },
    joinCode: str(b.joinCode) || null,
    doors: b.doors
      .filter(isRecord)
      .map((d) => ({ door: num(d.door), opensAt: date(d.opensAt) ?? new Date(0), counts: bool(d.counts) })),
    standings: b.standings.map(readStanding).filter((s): s is Standing => s !== null),
    together: num(b.together),
    removed: (Array.isArray(b.removed) ? b.removed.filter(isRecord) : [])
      .map((r) => ({ id: num(r.id), login: str(r.login), removedAt: date(r.removedAt) }))
      .filter((r) => r.id > 0 && r.login !== ''),
  };
}

export function readJoinPreview(b: Json): JoinPreview | null {
  if (!str(b.name)) return null;
  return {
    id: str(b.id) || null,
    name: str(b.name),
    public: bool(b.public),
    countsUntil: date(b.countsUntil),
    final: bool(b.final),
    owner: person(b.owner),
    members: num(b.members),
    maxMembers: num(b.maxMembers),
    full: bool(b.full),
    member: bool(b.member),
  };
}

/**
 * Sends a request to the API from the browser, as JSON, and reads the answer. Any failure to
 * get one is unavailable.
 */
export async function callApi<T>(
  method: 'GET' | 'POST' | 'PATCH' | 'DELETE',
  path: string,
  read: (body: Json) => T | null,
  { body, fetchFn = fetch }: { body?: unknown; fetchFn?: typeof fetch } = {},
): Promise<Answer<T>> {
  try {
    const headers: Record<string, string> = { Accept: 'application/json' };
    // Every change goes as JSON: the API refuses anything else, which a form on another
    // site can't send.
    if (method !== 'GET') headers['Content-Type'] = 'application/json';
    const response = await fetchFn(`/api${path}`, {
      method,
      headers,
      body: method === 'GET' ? undefined : JSON.stringify(body ?? {}),
      credentials: 'same-origin',
    });
    const text = await response.text();
    let parsed: unknown = null;
    try {
      parsed = text ? JSON.parse(text) : {};
    } catch {
      parsed = null;
    }
    return readAnswer(response.status, parsed, read);
  } catch {
    return { state: 'unavailable' };
  }
}

/** Reads nothing: for answers whose body doesn't matter. */
export const readNothing = (): true => true;

/** The board's ID in a path like /leaderboards/<id>, or null. */
export function boardIdFromPath(pathname: string): string | null {
  return new RegExp(`^/leaderboards/(${TOKEN})/?$`).exec(pathname)?.[1] ?? null;
}

/** The join code in a path like /join/<code>, or null. */
export function joinCodeFromPath(pathname: string): string | null {
  return new RegExp(`^/join/(${TOKEN})/?$`).exec(pathname)?.[1] ?? null;
}

export function boardHref(id: string): string {
  return `/leaderboards/${id}`;
}

/** The invite link, to share: absolute, since it's pasted elsewhere. */
export function inviteUrl(origin: string, code: string): string {
  return `${origin}/join/${code}`;
}

/** A name as the API keeps it, or null if it wouldn't take it. */
export function cleanName(name: string): string | null {
  const trimmed = name.trim();
  const length = [...trimmed].length;
  // The API refuses control characters and line breaks: a name is one line of text.
  const oneLine = [...trimmed].every((c) => {
    const code = c.codePointAt(0) ?? 0;
    return code > 0x1f && !(code >= 0x7f && code <= 0x9f) && code !== 0x2028 && code !== 0x2029;
  });
  if (length < 1 || length > NAME_MAX || !oneLine) {
    return null;
  }
  return trimmed;
}

/** A player as the board names them: "@alice", or "private" when it may not. */
export function playerName(s: Pick<Standing, 'login' | 'private'>): string {
  return s.private ? 'private' : `@${s.login}`;
}

/** What keeps a row apart from the others: its ID, or its place for private ones, which have none. */
export function rowKey(s: Pick<Standing, 'id' | 'private'>, index: number): string {
  return s.private ? `private-${index}` : String(s.id);
}

/** "#1 of 12". */
export function rankText(rank: number, members: number): string {
  return `#${rank} of ${members}`;
}

export function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`;
}

/**
 * A deadline as the site writes it, in the viewer's own time zone with its name, since
 * whoever set it picked a date and a time in theirs: "dec 31, 23:59 CET".
 */
export function formatDeadline(d: Date, timeZone?: string): string {
  const day = new Intl.DateTimeFormat('en-US', { timeZone, month: 'short', day: 'numeric' }).format(d).toLowerCase();
  const parts = new Intl.DateTimeFormat('en-GB', { timeZone, hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZoneName: 'short' }).formatToParts(d);
  const part = (type: string) => parts.find((p) => p.type === type)?.value ?? '';
  return `${day}, ${part('hour')}:${part('minute')} ${part('timeZoneName')}`;
}

/** The line under a board's name: who's in it, who sees it, until when it counts. */
export function boardMeta(board: Pick<Board, 'members' | 'public' | 'countsUntil' | 'final'>, timeZone?: string): string {
  const parts = [plural(board.members, 'member', 'members'), board.public ? 'public' : 'private'];
  if (board.countsUntil) {
    parts.push(board.final ? `final standings, ended ${formatDeadline(board.countsUntil, timeZone)}` : `counts until ${formatDeadline(board.countsUntil, timeZone)}`);
  }
  return parts.join(' · ');
}

/** A date for a datetime-local input, in the browser's time zone: "2026-12-31T23:59". */
export function toLocalInput(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

/** What a datetime-local input holds, as a moment in the browser's time zone, or null. */
export function fromLocalInput(value: string): Date | null {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/.exec(value);
  if (!m) return null;
  const [year, month, day, hour, minute] = m.slice(1).map(Number);
  const d = new Date(year, month - 1, day, hour, minute);
  // new Date rolls over impossible dates (Feb 30); they're refused instead.
  if (d.getFullYear() !== year || d.getMonth() !== month - 1 || d.getDate() !== day || d.getHours() !== hour) return null;
  return d;
}

/**
 * The deadline for a minute picked in a datetime-local input: the end of that minute, so a
 * fix at 23:59:30 still counts when the owner picked 23:59. Written back, it shows as 23:59.
 */
export function endOfMinute(d: Date): Date {
  const start = new Date(d);
  start.setSeconds(0, 0);
  return new Date(start.getTime() + 59_999);
}

/**
 * Whether changing the deadline from before to after can drop fixes that count now: a
 * deadline where there was none, or an earlier one. The settings ask first.
 */
export function deadlineDropsFixes(before: Date | null, after: Date | null): boolean {
  if (!after) return false;
  return !before || after.getTime() < before.getTime();
}

export interface DoorCell {
  door: number;
  /** fixed: counts and fixed. open: counts, not fixed. closed: doesn't count here (yet). */
  state: 'fixed' | 'open' | 'closed';
  /** What the cell says, in words, for screen readers and the details. */
  label: string;
}

/** One cell per door, in order, for a player's strip on the board at now. */
export function doorCells(doors: BoardDoor[], standing: Pick<Standing, 'doors'>, now: Date): DoorCell[] {
  const fixed = new Map(standing.doors.map((d) => [d.door, d]));
  return doors.map(({ door, opensAt, counts }) => {
    const d = fixed.get(door);
    if (d) {
      const details = [plural(d.points, 'point', 'points'), plural(d.hints, 'hint', 'hints'), plural(d.failedVerifies, 'failed verify', 'failed verifies')];
      return { door, state: 'fixed', label: `door ${door}: fixed, ${details.join(', ')}` };
    }
    if (counts) return { door, state: 'open', label: `door ${door}: not fixed` };
    const why = opensAt.getTime() > now.getTime() ? 'not open yet' : 'opened after the deadline';
    return { door, state: 'closed', label: `door ${door}: ${why}` };
  });
}
