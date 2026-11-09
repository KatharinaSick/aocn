interface ImportMetaEnv {
  /** Whether this build counts visits with counter.dev. Set in astro.config.mjs. */
  readonly COUNT_VISITS: boolean;
  /** Where on-demand pages ask the API, from the server. Set in astro.config.mjs. */
  readonly API_URL: string;
}

/** schedule.yaml at the repository root, parsed at build time by plugins/schedule.ts. */
declare module 'virtual:schedule' {
  const schedule: import('./lib/schedule').Schedule;
  export default schedule;
}
