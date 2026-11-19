import type { Plugin } from 'vite';

/**
 * The pages that serve many addresses, the way netlify.toml rewrites them in production:
 * every /leaderboards/<id> gets the prerendered board page, every /join/<code> the join page.
 * The page reads the ID or the code from the address itself.
 */
export function rewritePath(path: string): string | null {
  // Anything below them, like Netlify's splat: /leaderboards/a/b too, which the page answers
  // with "no leaderboard here". The pages that exist are served as they are.
  if (/^\/leaderboards\/.+/.test(path) && !/^\/leaderboards\/board\/?$/.test(path)) {
    return '/leaderboards/board/';
  }
  if (/^\/join\/.+/.test(path)) {
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
