/**
 * Who is logged in, for the header. Every page is the same for everyone and can be cached;
 * the header asks the API afterwards, with a request that's never cached.
 *
 * Logging in is hidden until door 1 opens: before that there's nothing to log in for, and
 * the landing page asks for an email address instead. The API takes logins all along, so
 * previewers can log in before then, and once they have, they see themselves here.
 */

export interface Me {
  login: string;
  avatarUrl: string;
}

/** What the header shows. */
export type Account = { state: 'loggedIn'; me: Me } | { state: 'loggedOut' } | { state: 'hidden' };

/** Asks the API who is logged in. Anything but a clear answer counts as nobody. */
export async function fetchMe(fetchFn: typeof fetch = fetch): Promise<Me | null> {
  try {
    const response = await fetchFn('/api/me', { headers: { Accept: 'application/json' } });
    if (response.status !== 200) {
      return null;
    }
    const body = (await response.json()) as Record<string, unknown>;
    if (typeof body.login !== 'string' || body.login === '') {
      return null;
    }
    return { login: body.login, avatarUrl: typeof body.avatarUrl === 'string' ? body.avatarUrl : '' };
  } catch {
    return null;
  }
}

/** What the header shows at `now`, given who is logged in and when door 1 opens. */
export function accountAt(me: Me | null, now: Date, loginOpensAt: Date): Account {
  if (me) {
    return { state: 'loggedIn', me };
  }
  return now >= loginOpensAt ? { state: 'loggedOut' } : { state: 'hidden' };
}

/** Where "log in" goes: GitHub, then back to `path`. */
export function loginHref(path: string): string {
  return `/api/auth/github/login?return=${encodeURIComponent(path)}`;
}
