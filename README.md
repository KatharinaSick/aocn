# aocn.dev

**Advent of Cloud Native** is a 24-day advent calendar for Cloud Native, in the spirit of Advent of Code.

From December 1, a new door opens every other day. Each door has a short interview with a CNCF maintainer and a
hands-on challenge. The challenge starts with something broken on a Kubernetes cluster. You fix it and check your
fix with the `aocn` CLI.

## What's in this repo

This repo holds the website, its API and the `aocn` CLI. The challenges live in their own repository.

```
web/    the website (Astro)
api/    the API (Go)
cli/    the aocn CLI (Go)
infra/  the Google Cloud setup (OpenTofu)
```

## Development

You need [devbox](https://www.jetify.com/devbox). It installs Go, Node, OpenTofu and Postgres for this project.

```sh
devbox shell
npm install --prefix web
devbox services up -b   # Postgres
devbox run web          # website on http://localhost:4321
devbox run api          # API on http://localhost:8080
```

Run the tests with `devbox run test`, and a full build with `devbox run build`. Both include the CLI.

## Deployment

Everything deploys from `main`.

- **Website:** Netlify builds `web/` and hosts it. The build runs the tests, so a failing test stops the deploy.
- **API:** a GitHub Actions workflow applies `infra/`, runs the tests and rolls out the API to Cloud Run.
- Netlify forwards `/api/*` to the API, so the browser only talks to `aocn.dev`.

See [infra/README.md](infra/README.md) for the platform setup.
