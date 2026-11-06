# AI Agent Instructions for Advent of Cloud Native

## About
Advent of Cloud Native is a 24-day Cloud Native advent calendar in the spirit of Advent of Code.
Twelve CNCF projects get two days each. Every other day starting December 1 a new door opens with a
5–10 minute maintainer interview and a hands-on challenge that takes 15–30 minutes. Challenges start
from something running and broken; players fix them and verify against a live cluster with the
`aocn` CLI. Points decay with each verification attempt and over time, so players can catch up at
any point.

The whole calendar tells one story: POLARIS-7, the dry AI of an Arctic research station, spends December
watching a stranded crew break everything on her cluster. Players are the station's new engineer.

This repository holds the website, its API and the `aocn` CLI (`cli/`). The challenges live in their
own private repository and are out of scope here. The CLI is public and the same for every door: it
never names a door's tools, checks or content, not even in examples or tests.

## Repository
GitHub: `KatharinaSick/aocn`

## Ways of Working
This project is built in small, separately verifiable steps, not in big chunks. The point is that
every step can be reviewed and trusted on its own — a large drop of code nobody can check is worse
than slow progress.

- **Where tasks come from**: Work is tracked in Asana, which agents cannot read. The task is
  whatever the conversation states — do not go looking for GitHub issues, there are none.
- **One task at a time**: Work on a single task, finish it, and stop. Do not roll the next task
  into the current one because it is convenient.
- **A task is small enough when it can be verified on its own.** If checking the result means
  "look at the whole site and see if it feels right", the task is too big — split it.
- **Say how it was verified**: Every finished step reports what was actually run or checked, not
  just what was written. Never describe something as working without having run it.
- **No speculative building**: Do not add structure, abstractions or pages the current task does
  not ask for.
- **Approval**: Always ask for approval before implementing any changes.
- **Clarification**: Never guess. If anything is unclear, ask for clarification until the task is
  fully understood.
- **Implementation**: Do not implement new features or significant changes without explicit
  approval. Planning should precede implementation.

## General Guidelines
- **Audience**: End users are developers and platform engineers. Technical language is fine, but the
  calendar should stay playful — it is a game, not documentation.
- **POLARIS-7's voice**: Any in-story text is POLARIS-7 speaking: dry, understated, faintly exasperated with
  the crew, never cutesy and never cruel. She states facts and lets them land. Keep her logs short.
- **Her name**: Always write POLARIS-7, on the site and in this repository. The crew's nickname
  for her is saved for December; do not use it anywhere before then.
- **Spoilers**: Never put challenge solutions, or hints specific enough to substitute for solving,
  into this repository. It is public.
- **Full-stack security**: Always think about the full picture — this project has a website, an API
  and a CLI. Hiding or restricting something in the website is not security. Any access control must
  be enforced in the API as well.
- **Door gating**: A door's content must not be reachable before it opens, including via the API or
  a prerendered page. Gate on the server, not just in the UI.

## Git Commits
- **Style**: Use [Conventional Commits](https://www.conventionalcommits.org/) (e.g. `feat:`, `fix:`,
  `chore:`, `docs:`).

## CLI Versions
- **`cli/VERSION`** holds the CLI's version (`X.Y.Z`, no `v`). A PR that changes the CLI bumps it:
  patch for fixes, minor for something new. It stays below 1.0 until the commands settle, and
  below 2.0 as long as possible, since Go wants the module path changed to `…/cli/v2` from then on.
- Merging to main tags the commit `cli/vX.Y.Z` (`.github/workflows/cli-release.yml`), which is
  what `go install …/cli/cmd/aocn@vX.Y.Z` finds. Nobody tags by hand, and a version is never
  tagged twice.
- A PR that changes `cli/` (READMEs and tests aside) without a bump gets a comment
  (`.github/workflows/version-check.yml`).
- Players get a new CLI through the devcontainer image: once it's built with the new version,
  bump the image in `.devcontainer/devcontainer.json`.

## Website Tech Stack
- **Framework**: Astro 7, with Vue 3 islands for anything interactive (`@astrojs/vue`).
- **Language**: TypeScript, checked with `astro check`.
- **Tests**: Vitest, next to the code they test (`*.test.ts`).
- **Deployment**: Netlify, via `@astrojs/netlify`.
- **Font**: JetBrains Mono, self-hosted through Astro's `fonts` config. No third-party font CDN.
- **Content**: Astro content collections (`web/src/content/`), once door content arrives.
- **No i18n**: The site is English only. Do not add vue-i18n, Astro's i18n routing, or any
  translation layer.

## API Tech Stack
Follows the same stack as `KatharinaSick/clubrizer`:
- **Language**: Go.
- **Database**: PostgreSQL on Neon (free plan), pgx/v5. Migrations are golang-migrate, embedded in
  the binary and run at startup, so the deploy identity never needs database credentials.
- **Email**: Resend. Signup is double opt-in; subscribers are Resend contacts.
- **Validation**: go-playground/validator.
- **Authentication**: golang-jwt/jwt/v5. Login is GitHub OAuth only — no passwords, no email login.
- **Configuration**: yaml.v3.
- **Hosting**: Cloud Run, reached from Netlify through an `/api/*` proxy in `netlify.toml`.

## Testing
- The schedule and the door gating must have unit tests: the code that decides when a door opens, and
  the code that decides whether a door's content may be shown. A mistake in either cannot be undone
  once it is public.
- Scoring, points decay and anything the `aocn` CLI submits are in the same category when they arrive.
- Layout and styling do not need tests.
- Tests run in the build, so a broken invariant fails the deploy rather than reaching production:
  `npm run build` runs `astro check`, then `vitest run`, then `astro build`.
- Accessibility is the one exception: it needs a browser, so it runs on pull requests
  (`.github/workflows/web-ci.yml`) instead of in the build, where Netlify would download Chromium
  on every deploy. `npm run test:a11y` (after `npm run build`) runs axe, a keyboard check and a
  contrast check against the real sky (aurora at its worst, 320–1440px) on every prerendered
  page in `dist/`, so a new page is covered without touching the test.
- Logic with rules (the schedule, vim commands, focus movement, the calendar feed) lives in
  `web/src/lib/`, free of the DOM, so it can be tested. Components only carry it out.

## Coding Conventions

### Astro / Vue
- **Astro by default**: Write pages and components as `.astro` unless they need client-side state.
  Reach for a Vue island only when there is real interactivity, and give it the narrowest
  `client:*` directive that works (`client:visible` over `client:load` where possible).
- **Vue components**: Single File Components with `<script setup lang="ts">`, Composition API.
- **TypeScript**: Always use TypeScript. Avoid `any` where possible.
- **Components**: Prefer reusing existing components in `web/src/components` over native HTML
  elements, and extend them with new props when a variant is needed.
- **Styling**: Scoped styles (`<style>` in `.astro`, `<style scoped>` in Vue) plus the design
  tokens are the whole styling system. Do not add a CSS framework or utility-class library — the
  tokens are the constraint that keeps the site consistent.
  - **CSS Variables**: Always use the design tokens in `web/src/styles/base.css`. Never
    hardcode colors, font sizes, font weights, spacing, radii or shadows.
  - **A deliberate scale, not a changelog**: The token set is decided as a scale. Adding a value
    means deciding it belongs in the scale — do not append one-off variables per page.
  - **Class Naming**: camelCase, prefixed with the component name (e.g. `.doorCardHeader`).
- **Dates**: Door opening times are defined once in `schedule.yaml` at the repository root,
  read by the website (`web/src/lib/doors.ts`, through `web/plugins/schedule.ts`) and the API
  (`api/internal/schedule`). Never hardcode a date anywhere else. Both sides must accept and
  reject the same files; `testdata/` holds the ones their tests share.
- **SEO**: Every page sets its own title, description and social image through `BaseLayout`. The
  landing page and the profile pages are what get shared — treat their previews as a feature.
  The default social image, `public/social.png`, is drawn from `src/social/SocialImagePage.astro`
  (served under `astro dev` only); run `npm run social-image` after changing that page. A page
  with `noindex` must also be listed in `noindexPaths` in `astro.config.mjs`, which keeps it
  out of the sitemap.
- **Dev server**: `astro dev` blocks the terminal. Agents should start it with
  `astro dev --background` and manage it with `astro dev stop`, `astro dev status` and
  `astro dev logs`. `devbox run web` stays foreground, which is what a human wants.
  The dev server sometimes keeps serving a component's old styles after an edit. If a style
  change does not show up, check the production build (`npm run build`) before debugging the
  code, and restart the dev server.
- **External links**: Every link to another site opens in a new tab: `target="_blank"` with
  `rel="noopener"` (plus `sponsored` for the supporter). Links within aocn.dev stay in the tab.
- **Whitespace in templates**: Astro drops the space at a line break next to a tag, so
  `a\n<code>b</code>` renders as "ab". Keep inline elements on the same line as the words around
  them, or add an explicit `{' '}`. Check rendered text, not just the source.

### Rendering
- Pages are prerendered by default. A page that depends on per-request data sets
  `export const prerender = false`. Player profiles are on-demand so their link previews can show
  real progress; keep the set of on-demand pages as small as possible.
- **Why there is a Netlify function at all, given the API is Go.** `/u/<handle>` is the only page
  that cannot be prerendered — a handle is not known until someone visits. Its `og:*` tags have to
  contain that player's real progress, and meta tags cannot be filled in by client-side JavaScript
  as far as crawlers are concerned. So some server has to render that HTML per request.
  It is the Netlify function rather than Go on purpose: the function contains no business logic, no
  auth decisions and no database access — it fetches JSON from the Go API and renders the same Astro
  layout every other page uses. Rendering it in Go instead would mean a second copy of the layout,
  header, footer and class names in `html/template`, free to drift from the real site.
  Keep it that way: no logic in the function, and no second on-demand page unless it genuinely needs
  per-request HTML.
- Load the Netlify adapter only for `astro build`. `astro dev` renders on-demand pages itself, and
  loading the adapter in dev pulls in Netlify's platform emulation for features this site does not
  use. If the site ever does use one — the Image CDN for social images, or Blobs-backed sessions —
  local dev will not emulate it and that decision needs revisiting.

## Design System
A living design system page lives at `/design-system` (`web/src/pages/design-system.astro`). It
documents every design token (colors, typography, spacing, radii, shadows) and shows every reusable
component with usage notes.

**When to update it:**
- A new component is added to `web/src/components` → add a section demonstrating it.
- An existing component gains a new prop, variant or state → update its section.
- A new token is added to `base.css` → add it to the relevant token section.

The page uses the real components, not mocks, so it doubles as a smoke test that they still render.
It is the reason token drift is visible instead of theoretical — keep it current in the same change
that introduces the component or token.
