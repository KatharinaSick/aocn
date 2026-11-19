import { describe, expect, it } from 'vitest';
import { rewritePath } from './rewrites';

describe('rewritePath', () => {
  it('sends boards and invite links to their pages, and leaves everything else', () => {
    expect(rewritePath('/leaderboards/AbCdEfGh12345_-x')).toBe('/leaderboards/board/');
    expect(rewritePath('/join/AbCdEfGh12345_-x/')).toBe('/join/');
    // Like Netlify's splat: deeper paths too. The page says there's no leaderboard there.
    expect(rewritePath('/leaderboards/a/b')).toBe('/leaderboards/board/');
    expect(rewritePath('/join/a/b')).toBe('/join/');
    for (const path of ['/leaderboards', '/leaderboards/', '/leaderboards/board', '/leaderboards/board/', '/join', '/join/', '/u/x']) {
      expect(rewritePath(path)).toBeNull();
    }
  });
});
