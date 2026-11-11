/**
 * The share image of a profile card (/u/<login>/card.png): what POLARIS-7 thinks of
 * someone and their doors, 1200 × 630, for link previews. This file builds the layout as a
 * tree satori can draw; the endpoint only draws it. No logic beyond the card's own.
 *
 * Colors come from the site's design tokens (styles/base.css), read as text: the image has
 * no CSS to look them up in.
 */
import { polaFaceSvg, type PolaMood } from './polaFace';
import type { Assessment, DoorCell } from './profile';

export const SHARE_IMAGE_WIDTH = 1200;
export const SHARE_IMAGE_HEIGHT = 630;
const MARGIN = 64;

/** A node satori draws: the shape of a React element, without React. */
export interface CardNode {
  type: string;
  props: Record<string, unknown> & { style?: Record<string, unknown>; children?: unknown };
}

/** The custom properties set on :root in base.css, by name without the dashes. Mobile overrides come later in the file and are ignored. */
export function readTokens(css: string): Record<string, string> {
  const tokens: Record<string, string> = {};
  for (const [, name, value] of css.matchAll(/--([a-z0-9-]+):\s*([^;]+);/g)) {
    tokens[name] ??= value.trim();
  }
  return tokens;
}

/** Her title's size: long titles get smaller, so the longest still fits on one line. */
export function titleSize(title: string): number {
  return title.length > 14 ? 56 : 72;
}

const MOOD_TOKEN: Record<PolaMood, string> = {
  neutral: 'color-aurora',
  annoyed: 'color-signal',
  worried: 'color-violet',
  fond: 'color-warm',
};

function el(type: string, style: Record<string, unknown>, ...children: unknown[]): CardNode {
  return { type, props: { style, children: children.length === 1 ? children[0] : children } };
}

function svgImage(svg: string, width: number, height: number, style: Record<string, unknown> = {}): CardNode {
  const src = `data:image/svg+xml;base64,${Buffer.from(svg).toString('base64')}`;
  return { type: 'img', props: { src, width, height, style } };
}

/** The icon in a door's cell: ✓ fixed, ○ open, a lock while closed. Drawn, since the font has no ✓. */
function doorIcon(state: DoorCell['state'], color: string): CardNode {
  const paths = {
    fixed: `<path d="M3 10 L8 15 L17 5" fill="none" stroke="${color}" stroke-width="2.5"/>`,
    open: `<circle cx="10" cy="10" r="5" fill="none" stroke="${color}" stroke-width="2"/>`,
    closed: `<rect x="4" y="9" width="12" height="9" rx="1.5" fill="none" stroke="${color}" stroke-width="2"/><path d="M7 9 V6.5 a3 3 0 0 1 6 0 V9" fill="none" stroke="${color}" stroke-width="2"/>`,
  };
  return svgImage(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 20">${paths[state]}</svg>`, 20, 20);
}

export interface ShareCardInput {
  login: string;
  assessment: Assessment;
  cells: DoorCell[];
  /** The season's year, from the schedule: "dec 2026". */
  year: number;
  tokens: Record<string, string>;
  /** The sky behind everything, the site's own drawing as SVG. */
  skySvg: string;
}

/** The share image's layout. */
export function shareCard({ login, assessment, cells, year, tokens: t, skySvg }: ShareCardInput): CardNode {
  const fixed = cells.filter((c) => c.state === 'fixed').length;
  const face = t[MOOD_TOKEN[assessment.mood]];

  const doorCell = (cell: DoorCell) => {
    const color = cell.state === 'fixed' ? t['color-aurora'] : cell.state === 'open' ? t['color-text'] : t['color-muted'];
    return el(
      'div',
      {
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        gap: 8,
        width: 72,
        padding: '14px 0',
        borderRadius: 8,
        border: `2px ${cell.state === 'closed' ? 'dashed' : 'solid'} ${cell.state === 'fixed' ? t['color-aurora'] : t['color-line-strong']}`,
        backgroundColor: cell.state === 'fixed' ? t['color-panel-new'] : t['color-panel'],
        color,
        fontSize: 24,
        fontWeight: cell.state === 'closed' ? 400 : 700,
      },
      String(cell.door).padStart(2, '0'),
      doorIcon(cell.state, color),
    );
  };

  // Three layers, each positioned, so they paint in this order: the sky, a shade on the left
  // where the text is, then the text. (Positioned elements paint over anything that isn't.)
  const layer = { position: 'absolute', top: 0, left: 0, width: SHARE_IMAGE_WIDTH, height: SHARE_IMAGE_HEIGHT };
  return el(
    'div',
    {
      display: 'flex',
      width: SHARE_IMAGE_WIDTH,
      height: SHARE_IMAGE_HEIGHT,
      backgroundColor: t['color-bg'],
      color: t['color-text'],
      fontFamily: 'JetBrains Mono',
      fontSize: 24,
    },
    // The sky is 16:9, the image wider than that: cut to fit.
    svgImage(skySvg, SHARE_IMAGE_WIDTH, 675, { position: 'absolute', top: -20, left: 0 }),
    el('div', {
      ...layer,
      backgroundImage: `linear-gradient(90deg, ${t['color-bg']} 0%, rgba(10, 17, 20, 0.6) 65%, rgba(10, 17, 20, 0.2) 100%)`,
    }),
    // Inside a 64px margin. satori adds padding to the size instead of taking it out of it.
    el(
      'div',
      {
        position: 'absolute',
        top: MARGIN,
        left: MARGIN,
        width: SHARE_IMAGE_WIDTH - 2 * MARGIN,
        height: SHARE_IMAGE_HEIGHT - 2 * MARGIN,
        display: 'flex',
        flexDirection: 'column',
        justifyContent: 'space-between',
      },
    el('span', { fontWeight: 700, letterSpacing: '0.08em' }, 'advent of cloud native'),
    // Her assessment, word for word as on the card page.
    el(
      'div',
      { display: 'flex', alignItems: 'center', gap: 48 },
      svgImage(polaFaceSvg(assessment.mood, face, t['color-bg']), 192, 144),
      el(
        'div',
        { display: 'flex', flexDirection: 'column', gap: 12, width: 768 },
        el('span', { color: t['color-text-soft'] }, `POLARIS-7 has assessed @${login} as`),
        el(
          'span',
          { color: t['color-aurora'], fontSize: titleSize(assessment.title), fontWeight: 700, letterSpacing: '-0.02em' },
          assessment.title,
        ),
        el('span', { color: t['color-text'], lineHeight: 1.4 }, assessment.line),
      ),
    ),
    el('div', { display: 'flex', gap: 12 }, ...cells.map(doorCell)),
    el(
      'div',
      { display: 'flex', justifyContent: 'space-between' },
      el('span', { color: t['color-text-soft'] }, `${fixed} of ${cells.length} doors · dec ${year}`),
      el('span', { color: t['color-aurora'] }, 'join the crew · aocn.dev'),
    ),
    ),
  );
}
