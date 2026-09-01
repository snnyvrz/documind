# AGENTS.md

DocuMind: a PDF-document upload service. Two independent apps under a single repo, wired together only by `compose.yaml`. No monorepo tooling, no CI, no tests in frontend.

## Layout

- `api/` — Go 1.26 backend. Module name is `api` (not a host path). HTTP server on `:1323` using **labstack/echo v5** (note: context params are `*echo.Context`, not v4's value type), GORM + Postgres.
- `frontend/` — React 19 + Vite 8 + Tailwind v4 + shadcn/ui. **Bun** is the package manager (`bun.lock` committed; never add a `package-lock.json`). Path alias `@/*` → `src/*`. React Compiler is enabled via `@rolldown/plugin-babel` + `reactCompilerPreset` in `vite.config.ts`.
- `compose.yaml` — full-stack run: Postgres (`db`, port 5432), `api` (1323), `frontend` under nginx (8080, proxies `/documents` → `api`). Uploads land in the `data/` volume.

## Commands

API (from `api/`):
- Test: `go test ./...` — runs standalone, uses an in-memory `memoryDocumentStore` (no Postgres needed).
- Live reload: `air` (config in `.air.toml`, builds to `api/tmp/main`). Requires Postgres and env; see below.

Frontend (from `frontend/`):
- `bun install` (or `bun install --frozen-lockfile` like the Dockerfile)
- `bun run dev` — Vite dev server, proxies `/documents` → `http://127.0.0.1:1323`, so the API must be running locally.
- `bun run build` — `tsc -b && vite build`. `bun run lint` — ESLint. No frontend test script.

Full stack: `docker compose up --build`.

## API local setup

- Copy `api/.env.example` → `api/.env` and start Postgres first: `docker compose up -d db`. `main.go` loads `.env` via godotenv; `DATABASE_URL` is required (panics if unset/unreachable) and `AutoMigrate` runs on startup. `UPLOAD_DIRECTORY` defaults per XDG spec if unset.

## Gotchas

- Tailwind is v4: CSS-first config (`@import "tailwindcss"` in `src/index.css`), `@tailwindcss/vite` plugin, no `tailwind.config.*` file.
- shadcn/ui uses registry style `base-rhea` (`components.json`); shadcn components live in `src/components/ui/`.
- API upload limit is 20 MB (`http.MaxBytesReader`) and PDFs are validated by `%PDF-` magic bytes — keep this in sync with nginx `client_max_body_size` (currently local to the container nginx config).
- `data/`, `api/.env`, and `api/tmp/` are gitignored working-state (uploads, local env, air build artifacts).