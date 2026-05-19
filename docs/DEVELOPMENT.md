# Development Guide

## Architecture

`chatxxx` is intentionally independent from the PHP main site.

```text
chatxxx/
  backend/    Go API, SQLite, SSE streaming
  frontend/   React/Vite UI
  docs/       Development and API notes
```

The frontend talks to the backend through `/api/*`. In development, Vite proxies `/api` to the Go server.
The development frontend listens on `0.0.0.0:5178`, so it can be opened from another browser with the server IP and port when firewall/security-group rules allow it.

## Backend

Default command:

```bash
cd backend
cp .env.example .env
go mod tidy
go run ./cmd/server
```

Important environment variables:

- `APP_PORT`: backend port, default `8007`
- `DB_PATH`: SQLite database path, default `../data/chatxxx.sqlite`
- `SESSION_SECRET`: signed-session secret
- `CORS_ORIGIN`: frontend origin, default `http://127.0.0.1:5178`

The first registered account receives the `admin` role.

## Frontend

Default command:

```bash
cd frontend
npm install
npm run dev
```

The UI is a new React implementation, not a copy of the old DOM structure. The current PHP chat UI remains the product reference for interaction and layout.

## Milestones

1. Core standalone loop: auth, providers, conversations, SSE chat.
2. Chat quality: message versions, references, stop/retry, title generation.
3. Memory system: manual memories, extraction, embeddings.
4. Tools: internal tools, MCP adapter, generated files.
5. Production hardening: backups, audit logs, rate limits, deployment scripts.

## Coding Notes

- Keep all new code inside `chatxxx/`.
- Do not depend on PHP classes, PHP sessions, or the old main database.
- Preserve the existing SSE event names where possible to make future migration easier.
