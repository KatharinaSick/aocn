/**
 * POLARIS-7's pixel face: the geometry of each mood on a 128 × 96 grid. The PolaFace
 * component draws it on the site, and the share image (lib/shareCard.ts) draws the same.
 */

export type PolaMood = 'neutral' | 'annoyed' | 'worried' | 'fond';

/** [x, y, width, height] on the 128 × 96 grid. */
export type Pixel = [number, number, number, number];

export const eyes: Record<PolaMood, Pixel[]> = {
  // Small eyes, no lids.
  neutral: [[36, 40, 8, 8], [84, 40, 8, 8]],
  // Lids low on the pupils.
  annoyed: [[28, 32, 24, 8], [76, 32, 24, 8], [36, 40, 8, 8], [84, 40, 8, 8]],
  // Lids tilted up toward the middle, lower pupils.
  worried: [[28, 36, 8, 8], [36, 32, 8, 8], [44, 28, 8, 8], [76, 28, 8, 8], [84, 32, 8, 8], [92, 36, 8, 8], [36, 48, 8, 8], [84, 48, 8, 8]],
  // Closed into ^ shapes.
  fond: [[32, 40, 8, 8], [40, 32, 8, 8], [48, 40, 8, 8], [72, 40, 8, 8], [80, 32, 8, 8], [88, 40, 8, 8]],
};

export const mouths: Record<PolaMood, Pixel[]> = {
  neutral: [[44, 64, 40, 8]],
  annoyed: [[44, 64, 40, 8]],
  worried: [[48, 68, 32, 8]],
  fond: [[40, 60, 8, 8], [48, 68, 32, 8], [80, 60, 8, 8]],
};

/** The drop of sweat on her worried face. */
export const sweat: Pixel[] = [
  [106, 16, 6, 6],
  [104, 22, 10, 8],
];

/** Her face as a standalone SVG, for places without the site's CSS (the share image). */
export function polaFaceSvg(mood: PolaMood, color: string, background: string): string {
  const rects = (pixels: Pixel[], opacity = 1) =>
    pixels.map(([x, y, w, h]) => `<rect x="${x}" y="${y}" width="${w}" height="${h}" fill="${color}" opacity="${opacity}"/>`).join('');
  return (
    `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 128 96" shape-rendering="crispEdges">` +
    `<rect x="4" y="4" width="120" height="88" rx="8" fill="${background}" stroke="${color}" stroke-width="4"/>` +
    rects(eyes[mood]) +
    rects(mouths[mood]) +
    (mood === 'worried' ? rects(sweat, 0.6) : '') +
    `</svg>`
  );
}
