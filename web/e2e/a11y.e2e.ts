import { readdirSync } from 'node:fs';
import { join, relative, sep } from 'node:path';
import AxeBuilder from '@axe-core/playwright';
import { expect, test, type Page } from '@playwright/test';

// Every prerendered page in dist/, so a new page is covered without touching this file.
const distDir = join(import.meta.dirname, '..', 'dist');

function findPages(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) return findPages(path);
    if (!entry.name.endsWith('.html')) return [];
    const route = relative(distDir, path).split(sep).join('/');
    if (route === 'index.html') return ['/'];
    if (route.endsWith('/index.html')) return [`/${route.slice(0, -'index.html'.length)}`];
    return [`/${route.slice(0, -'.html'.length)}`];
  });
}

// On-demand pages aren't in dist/: they're rendered by `astro dev` from a fake API with a
// made-up door (playwright.config.ts). List each kind of on-demand page here.
const onDemandPages = ['http://localhost:4800/doors/1', 'http://localhost:4800/doors/3', 'http://localhost:4800/u/example'];

const pages = [...findPages(distDir), ...onDemandPages];

test('dist/ contains the pages to test', () => {
  expect(pages, 'run `npm run build` first').toContain('/');
});

// One entry per failing element, so the CI log says what broke and where.
function describeViolations(violations: Awaited<ReturnType<AxeBuilder['analyze']>>['violations']): string[] {
  return violations.flatMap((violation) =>
    violation.nodes.map((node) => `${violation.id} (${violation.impact}): ${violation.help} — ${node.target.join(' ')}`),
  );
}

// Tabs through the page and returns what received focus, until focus wraps or leaves the
// document. Capped so a focus trap fails the test instead of hanging it.
async function tabThrough(page: Page) {
  const stops: { name: string; hasRing: boolean }[] = [];
  for (let i = 0; i < 100; i++) {
    await page.keyboard.press('Tab');
    const stop = await page.evaluate(() => {
      const element = document.activeElement;
      if (!(element instanceof HTMLElement) || element === document.body) return null;
      // Marks the element itself: two stops can share a label, so names cannot detect the wrap.
      if (element.dataset.a11yVisited) return null;
      element.dataset.a11yVisited = 'true';
      const style = getComputedStyle(element);
      const hasOutline = style.outlineStyle !== 'none' && parseFloat(style.outlineWidth) > 0;
      const name = `${element.tagName.toLowerCase()} "${(element.getAttribute('aria-label') ?? element.textContent ?? '').trim().replace(/\s+/g, ' ').slice(0, 40)}"`;
      return { name, hasRing: hasOutline || style.boxShadow !== 'none' };
    });
    if (!stop) return stops;
    stops.push(stop);
  }
  throw new Error('Focus never wrapped after 100 tabs — is there a focus trap?');
}

// The widths the sky check covers: phones, the mobile breakpoint on both sides, tablets and
// the desktop frame. The aurora sits at a different spot behind the text at each of them.
const SKY_WIDTHS = [320, 375, 799, 800, 1024, 1280, 1440];

// The sky is 1000px tall; nothing below it can be over the aurora.
const SKY_HEIGHT = 1000;

// The aurora at its worst for contrast: drifted to the left end of its 40s drift, towards
// the text, with the rays at full opacity.
const AURORA_WORST_CASE = `
  .skyAurora { animation: none !important; transform: translateX(-36px) !important; }
  .skyRays { animation: none !important; opacity: 1 !important; }
`;

interface SkyContrastFailure {
  text: string;
  contrast: number;
  required: number;
}

// Measures every visible piece of text against the pixels actually behind it: collects the
// text's boxes, hides the text, takes a screenshot and compares the text color with the
// background in each box. It takes the 98th brightest background pixel, not the brightest,
// so a star behind a letter does not count. Text in the ghost color is decoration by rule
// and is skipped.
async function skyContrastFailures(page: Page): Promise<SkyContrastFailure[]> {
  await page.addStyleTag({ content: AURORA_WORST_CASE });
  await page.evaluate(() => document.fonts.ready);

  const boxes = await page.evaluate((skyHeight) => {
    const ghost = getComputedStyle(document.documentElement).getPropertyValue('--color-ghost').trim();
    const probe = document.createElement('span');
    probe.style.color = ghost;
    document.body.append(probe);
    const ghostRgb = getComputedStyle(probe).color;
    probe.remove();

    const found: { text: string; color: number[]; large: boolean; x: number; y: number; w: number; h: number }[] = [];
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      const element = node.parentElement;
      if (!element || !node.textContent?.trim() || element.closest('.srOnly, script, style')) continue;
      const style = getComputedStyle(element);
      if (style.visibility === 'hidden' || style.color === ghostRgb) continue;
      const color = (style.color.match(/[\d.]+/g) ?? []).map(Number);
      if (color[3] === 0) continue;
      const size = parseFloat(style.fontSize);
      const large = size >= 24 || (size >= 18.66 && Number(style.fontWeight) >= 700);
      const range = document.createRange();
      range.selectNodeContents(node);
      for (const rect of range.getClientRects()) {
        if (!rect.width || !rect.height || rect.top >= skyHeight) continue;
        const text = node.textContent.trim().replace(/\s+/g, ' ').slice(0, 40);
        found.push({ text, color, large, x: rect.left, y: rect.top, w: rect.width, h: rect.height });
      }
    }
    return found;
  }, SKY_HEIGHT);

  await page.addStyleTag({
    content: '* { color: transparent !important; text-decoration-color: transparent !important; caret-color: transparent !important; }',
  });
  const screenshot = (await page.screenshot()).toString('base64');

  return page.evaluate(
    async ({ screenshot, boxes }) => {
      const image = new Image();
      image.src = `data:image/png;base64,${screenshot}`;
      await image.decode();
      const canvas = document.createElement('canvas');
      canvas.width = image.width;
      canvas.height = image.height;
      const context = canvas.getContext('2d')!;
      context.drawImage(image, 0, 0);
      // The screenshot may be at a higher device pixel ratio than the CSS boxes.
      const scale = image.width / innerWidth;

      const luminance = ([r, g, b]: number[]) => {
        const [lr, lg, lb] = [r, g, b].map((channel) => {
          const s = channel / 255;
          return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
        });
        return 0.2126 * lr + 0.7152 * lg + 0.0722 * lb;
      };

      const failures = [];
      for (const box of boxes) {
        const x = Math.max(0, Math.floor(box.x * scale));
        const y = Math.max(0, Math.floor(box.y * scale));
        const w = Math.min(canvas.width - x, Math.ceil(box.w * scale));
        const h = Math.min(canvas.height - y, Math.ceil(box.h * scale));
        if (w <= 0 || h <= 0) continue;
        const data = context.getImageData(x, y, w, h).data;
        const background: number[] = [];
        for (let i = 0; i < data.length; i += 4) background.push(luminance([data[i], data[i + 1], data[i + 2]]));
        background.sort((a, b) => a - b);
        const backgroundLuminance = background[Math.floor((background.length - 1) * 0.98)];
        const textLuminance = luminance(box.color);
        const [light, dark] = [textLuminance, backgroundLuminance].sort((a, b) => b - a);
        const contrast = (light + 0.05) / (dark + 0.05);
        const required = box.large ? 3 : 4.5;
        if (contrast < required) failures.push({ text: box.text, contrast: Math.round(contrast * 100) / 100, required });
      }
      return failures;
    },
    { screenshot, boxes },
  );
}

for (const path of pages) {
  test.describe(path, () => {
    test('has no axe violations', async ({ page }) => {
      await page.goto(path);
      // axe cannot measure text over the sky (stars, aurora, the landing fade), so there it
      // reports contrast as "needs review" instead of failing. All three are decorative and
      // aria-hidden; without them, text is checked against the page background. Contrast
      // against the sky itself is the next test.
      await page.addStyleTag({ content: '.sky, .landingFade { display: none !important; }' });
      const results = await new AxeBuilder({ page })
        .withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa', 'best-practice'])
        // The design system's PromptLine demo duplicates the layout's real one, so it
        // repeats the "POLARIS-7" landmark. The real one is still checked on every page.
        .exclude('.designSystemGroup > .promptLine')
        .analyze();
      expect(describeViolations(results.violations), 'axe violations').toEqual([]);
    });

    test('text over the sky meets WCAG AA contrast', async ({ page }, testInfo) => {
      // Covers every width itself, so one project is enough.
      test.skip(testInfo.project.name !== 'desktop', 'runs once, at every width in SKY_WIDTHS');
      const failures: string[] = [];
      for (const width of SKY_WIDTHS) {
        await page.setViewportSize({ width, height: SKY_HEIGHT });
        await page.goto(path);
        for (const failure of await skyContrastFailures(page)) {
          failures.push(`${width}px: "${failure.text}" is ${failure.contrast}:1, needs ${failure.required}:1`);
        }
      }
      expect(failures, 'text below WCAG AA contrast against the sky').toEqual([]);
    });

    test('skip link comes first and every focus stop is visible', async ({ page }) => {
      await page.goto(path);
      const stops = await tabThrough(page);
      expect(stops[0]?.name, 'first tab stop').toMatch(/skip to content/i);
      const withoutRing = stops.filter((stop) => !stop.hasRing).map((stop) => stop.name);
      expect(withoutRing, 'focusable elements without a visible focus ring').toEqual([]);
    });
  });
}
