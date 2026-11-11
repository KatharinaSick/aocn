import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { assess, type DoorCell } from './profile';
import { readTokens, shareCard, titleSize, type CardNode } from './shareCard';

const css = readFileSync(new URL('../styles/base.css', import.meta.url), 'utf8');
const tokens = readTokens(css);

/** Every text in the card, in order. */
function texts(node: unknown): string[] {
  if (typeof node === 'string') return [node];
  if (Array.isArray(node)) return node.flatMap(texts);
  if (node && typeof node === 'object' && 'props' in node) return texts((node as CardNode).props.children);
  return [];
}

describe('readTokens', () => {
  it("reads the site's tokens, the desktop values first", () => {
    expect(tokens['color-aurora']).toBe('#5ce6a8');
    expect(tokens['color-bg']).toBe('#0a1114');
    // Redefined for mobile further down; the image is never mobile.
    expect(tokens['font-size-lead']).toBe('17px');
  });
});

describe('titleSize', () => {
  it('fits the longest title on one line', () => {
    expect(titleSize('Adequate')).toBe(72);
    expect(titleSize('Occasionally useful')).toBe(56);
  });
});

describe('shareCard', () => {
  const cells: DoorCell[] = [
    { door: 1, state: 'fixed' },
    { door: 3, state: 'open' },
    { door: 5, state: 'closed' },
  ];
  const card = shareCard({ login: 'octo', assessment: assess(1, 3), cells, year: 2026, tokens, skySvg: '<svg/>' });

  it('has her whole assessment, as on the card page, and how far they got', () => {
    expect(texts(card)).toEqual([
      'advent of cloud native',
      'POLARIS-7 has assessed @octo as',
      'Occasionally useful',
      'Fixed something once. I made a note.',
      '01',
      '03',
      '05',
      '1 of 3 doors · dec 2026',
      'join the crew · aocn.dev',
    ]);
  });

  it('uses only colors from the tokens', () => {
    const colors = JSON.stringify(card).match(/#[0-9a-f]{6}\b/gi) ?? [];
    const known = new Set(Object.values(tokens).map((v) => v.toLowerCase()));
    for (const color of colors) expect(known).toContain(color.toLowerCase());
  });
});
