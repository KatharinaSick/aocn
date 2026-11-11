// @ts-check
import { defineConfig, fontProviders } from 'astro/config';
import netlify from '@astrojs/netlify';
import sitemap from '@astrojs/sitemap';
import vue from '@astrojs/vue';
import { schedule } from './plugins/schedule.ts';

// The Netlify adapter is loaded for `astro build` only. `astro dev` renders on-demand
// pages itself, and loading the adapter in dev pulls in Netlify's platform emulation
// for features this site does not use. See AGENTS.md → Rendering.
const isBuild = process.argv.includes('build');

// Netlify sets CONTEXT to "production" only for the build that goes live, so deploy
// previews, branch deploys and local builds do not add to the visit counts. Decided at
// build time, because an on-demand page cannot read the build's environment.
const countVisits = process.env.CONTEXT === 'production';

// Where the door page asks the API, from the server. netlify.toml sets the Cloud Run URL;
// locally it's the API from `devbox run api`. Not /api/*: that proxy is for browsers, and a
// function calling back into its own site would take a detour through Netlify's edge.
const apiUrl = process.env.API_URL || 'http://localhost:8080';

// Pages with `noindex` in BaseLayout. They stay out of the sitemap too; keep the two in step.
const noindexPaths = ['/404', '/design-system', '/subscribe/confirm'];

// The page the social preview image is drawn from (scripts/social-image.ts). It exists
// under `astro dev` only, so it is never built or deployed.
/** @type {import('astro').AstroIntegration} */
const socialImagePage = {
  name: 'social-image-page',
  hooks: {
    'astro:config:setup': ({ command, injectRoute }) => {
      if (command === 'dev') {
        injectRoute({ pattern: '/social-image', entrypoint: './src/social/SocialImagePage.astro' });
      }
    },
  },
};

// https://astro.build/config
export default defineConfig({
  // Canonical URLs and social image URLs are built from this.
  site: 'https://aocn.dev',
  adapter: isBuild ? netlify() : undefined,
  // The accessibility tests render on-demand pages with `astro dev`, where the toolbar
  // would be tested as if it were part of the page.
  devToolbar: { enabled: process.env.AOCN_E2E !== 'true' },
  // Vue 3 islands for anything interactive; everything else stays plain Astro.
  integrations: [
    vue(),
    sitemap({ filter: (page) => !noindexPaths.includes(new URL(page).pathname.replace(/\/$/, '')) }),
    socialImagePage,
  ],
  // Locally, /api/* goes to the API from `devbox run api`, the way netlify.toml sends it
  // to Cloud Run in production: same prefix, stripped before it reaches the API.
  vite: {
    // resvg's WebAssembly, built into the share image's function with ?inline
    // (src/pages/u/[login]/card.png.ts), so it needs no file next to it at runtime.
    assetsInclude: ['**/*.wasm'],
    define: {
      'import.meta.env.COUNT_VISITS': JSON.stringify(countVisits),
      'import.meta.env.API_URL': JSON.stringify(apiUrl),
    },
    server: {
      proxy: {
        '^/api/': { target: 'http://localhost:8080', rewrite: (path) => path.replace(/^\/api/, '') },
      },
    },
    // schedule.yaml at the repository root, as `virtual:schedule` (see plugins/schedule.ts).
    plugins: [schedule()],
  },
  // Self-hosted: Astro downloads the font files at build time and serves them from
  // aocn.dev, so no visitor request goes to a third-party font CDN.
  fonts: [
    {
      provider: fontProviders.fontsource(),
      name: 'JetBrains Mono',
      cssVariable: '--font-mono',
      weights: [400, 700],
      styles: ['normal'],
      subsets: ['latin'],
      fallbacks: ['ui-monospace', 'Menlo', 'Consolas', 'monospace'],
    },
  ],
});