import { describe, expect, it } from 'vitest';
import { rewritePath } from './rewrites';

describe('rewritePath', () => {
  it('sends boards and invite links to their pages, and leaves everything else', () => {
    expect(rewritePath('/leaderboards/AbCdEfGh12345_-x')).toBe('/leaderboards/board/');
    expect(rewritePath('/join/AbCdEfGh12345_-x/')).toBe('/join/');
    for (const path of ['/leaderboards', '/leaderboards/', '/leaderboards/board', '/join', '/join/', '/u/x', '/leaderboards/a/b']) {
      expect(rewritePath(path)).toBeNull();
    }
  });
});
