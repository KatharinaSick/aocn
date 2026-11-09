import { defineConfig, devices } from '@playwright/test';

// Accessibility tests against the production build. They need a browser, so they run on
// pull requests (.github/workflows/web-ci.yml) rather than in `npm run build` — Netlify
// would otherwise download Chromium on every deploy. See AGENTS.md → Testing.
//
// `*.e2e.ts` rather than `*.test.ts`, so vitest does not pick these files up.
const port = 4799;
// Where on-demand pages are rendered, and the fake API they ask. e2e/a11y.e2e.ts uses the
// same port for the on-demand pages it tests.
const onDemandPort = 4800;
const fakeApiPort = 4801;

export default defineConfig({
  testDir: './e2e',
  testMatch: '**/*.e2e.ts',
  forbidOnly: !!process.env.CI,
  reporter: process.env.CI ? [['github'], ['list']] : 'list',
  use: {
    baseURL: `http://localhost:${port}`,
    // Lands in test-results/, which CI uploads when a test fails.
    screenshot: 'only-on-failure',
    // The countdown adds the visitor's own time outside CET, so the text on the page
    // depends on the timezone. Pinned, so CI and a laptop in Vienna test the same page,
    // and pinned outside CET, so that longer text is the one tested.
    timezoneId: 'America/Los_Angeles',
  },
  // `astro preview` does not serve the Netlify adapter's output, so dist/ is served as
  // plain static files for the prerendered pages. On-demand pages (the door page) come from
  // `astro dev` instead, asking a fake API with a made-up door (e2e/fakeApi.ts).
  // --ignore-lock keeps it in the foreground under an agent, and lets it run next to
  // another dev server.
  webServer: [
    {
      command: `serve dist --listen ${port} --no-request-logging`,
      url: `http://localhost:${port}`,
      reuseExistingServer: !process.env.CI,
    },
    {
      command: `node e2e/fakeApi.ts ${fakeApiPort}`,
      url: `http://localhost:${fakeApiPort}/health`,
      reuseExistingServer: !process.env.CI,
    },
    {
      command: `astro dev --port ${onDemandPort} --ignore-lock`,
      url: `http://localhost:${onDemandPort}`,
      env: { API_URL: `http://localhost:${fakeApiPort}`, AOCN_E2E: 'true' },
      reuseExistingServer: !process.env.CI,
    },
  ],
  projects: [
    { name: 'desktop', use: { ...devices['Desktop Chrome'], viewport: { width: 1280, height: 800 } } },
    { name: 'mobile', use: { ...devices['Desktop Chrome'], viewport: { width: 375, height: 812 } } },
  ],
});
