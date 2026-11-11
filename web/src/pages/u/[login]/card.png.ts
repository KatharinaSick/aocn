// The share image of someone's profile card, drawn on request: the link preview of
// /u/<login>, with their real progress. The layout is lib/shareCard.ts; this only draws it.
//
// satori turns the layout into SVG, resvg into PNG. resvg is WebAssembly, so the Netlify
// function needs no native binary; the wasm, the fonts and the sky are built into it.
import { Resvg, initWasm } from '@resvg/resvg-wasm';
import wasm from '@resvg/resvg-wasm/index_bg.wasm?inline';
import bold from '@fontsource/jetbrains-mono/files/jetbrains-mono-latin-700-normal.woff?inline';
import regular from '@fontsource/jetbrains-mono/files/jetbrains-mono-latin-400-normal.woff?inline';
import type { APIRoute } from 'astro';
import satori from 'satori';
import skySvg from '../../../social/sky.svg?raw';
import css from '../../../styles/base.css?raw';
import { DOOR_NUMBERS, FIRST_DOOR, doorOpensAt, isDoorOpen } from '../../../lib/doors';
import { SESSION_COOKIE } from '../../../lib/doorPage';
import { assess, doorCells, fetchProfile, profileCacheHeaders } from '../../../lib/profile';
import { SHARE_IMAGE_HEIGHT, SHARE_IMAGE_WIDTH, readTokens, shareCard } from '../../../lib/shareCard';

export const prerender = false;

/** The bytes behind a data: URL, as Vite's ?inline hands out files. */
const bytes = (dataUrl: string) => Buffer.from(dataUrl.slice(dataUrl.indexOf(',') + 1), 'base64');

// Once per function instance; every later request reuses it. The dev server loads this module
// again after an edit, while resvg stays initialized: that's fine, not an error.
const ready = initWasm(bytes(wasm)).catch((err: unknown) => {
  if (!String(err).includes('Already initialized')) throw err;
});
const tokens = readTokens(css);
const fonts = [
  { name: 'JetBrains Mono', data: bytes(regular), weight: 400 as const, style: 'normal' as const },
  { name: 'JetBrains Mono', data: bytes(bold), weight: 700 as const, style: 'normal' as const },
];

// The session goes along, like on the card: the owner can download the image of their card
// even while it's private. Everyone else, link previews included, gets none for a private
// card. profileCacheHeaders keeps the owner's copy out of every cache.
export const GET: APIRoute = async ({ params, cookies }) => {
  const answer = await fetchProfile(import.meta.env.API_URL, params.login ?? '', {
    session: cookies.get(SESSION_COOKIE)?.value,
  });
  const headers = profileCacheHeaders(answer);
  if (answer.state === 'missing') {
    return new Response(null, { status: 404, headers });
  }
  if (answer.state === 'unavailable') {
    return new Response(null, { status: 503, headers: { ...headers, 'Retry-After': '120' } });
  }

  const { profile } = answer;
  const cells = doorCells(profile.solved, DOOR_NUMBERS, isDoorOpen, new Date());
  const fixed = cells.filter((c) => c.state === 'fixed').length;
  const card = shareCard({
    login: profile.login,
    assessment: assess(fixed, cells.length),
    cells,
    year: doorOpensAt(FIRST_DOOR).getUTCFullYear(),
    tokens,
    skySvg,
  });

  const svg = await satori(card as Parameters<typeof satori>[0], { width: SHARE_IMAGE_WIDTH, height: SHARE_IMAGE_HEIGHT, fonts });
  await ready;
  const png = new Resvg(svg, { fitTo: { mode: 'width', value: SHARE_IMAGE_WIDTH } }).render().asPng();
  return new Response(Buffer.from(png), { headers: { ...headers, 'Content-Type': 'image/png' } });
};
