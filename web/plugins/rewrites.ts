import type { Plugin } from 'vite';

/**
 * The pages that serve many addresses, the way netlify.toml rewrites them in production:
 * every /leaderboards/<id> gets the prerendered board page, every /join/<code> the join page.
 * The page reads the ID or the code from the address itself.
 */
export function rewritePath(path: string): string | null {
  if (/^\/leaderboards\/[A-Za-z0-9_-]+\/?$/.test(path) && !/^\/leaderboards\/board\/?$/.test(path)) {
    return '/leaderboards/board/';
  }
  if (/^\/join\/[A-Za-z0-9_-]+\/?$/.test(path)) {
    return '/join/';
  }
  return null;
}

/** Does the same for `astro dev`, which doesn't read netlify.toml. */
export function rewrites(): Plugin {
  return {
    name: 'aocn-rewrites',
    configureServer(server) {
      server.middlewares.use((req, _res, next) => {
        const [path, query] = (req.url ?? '').split('?');
        const to = rewritePath(path);
        if (to) req.url = query ? `${to}?${query}` : to;
        next();
      });
    },
  };
}
