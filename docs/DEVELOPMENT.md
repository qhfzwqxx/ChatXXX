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
The chat UI lives at `/`, while the admin UI for provider/LLM settings lives at `/admin` and only accepts admin accounts.
In the provider form, `request_mode` chooses between OpenAI `chat/completions` and `responses`; `response_format` is stored as raw JSON and forwarded to the chosen API shape.
For `responses`, the backend now supports a small internal tool loop. Built-in tools currently include `get_current_time`, `web_search`, and `web_reader`; tool output is passed back with `function_call_output` until the model finishes.
The model instructions prefer a search-then-read browsing flow for web/current/research questions: `web_search` discovers candidate pages, then `web_reader` reads the most relevant URLs before the assistant writes the final answer.
The admin search-tool mode is mutually exclusive at runtime: `unifuncs` mode exposes only `web_search` and `web_reader`, while `searching` mode exposes only `searching`. Switching modes keeps both sets of settings saved but makes the inactive mode unavailable to the model and rejected by backend execution.
Tool execution emits `tool_steps` SSE events and is also written to the assistant message `metadata.tool_steps`. Each step includes `content_offset`, allowing the chat frontend to insert the tool line at the point in the assistant text where the tool call happened; live runs and refreshed history use the same data shape.

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
- `UNIFUNCS_API_KEY`: UniFuncs API key shared by the `web_search` and `web_reader` tools
- `UNIFUNCS_BASE_URL`: UniFuncs API base URL, default `https://api.unifuncs.com`. The backend accepts the root host, `/api`, the full search endpoint, or the full reader endpoint and normalizes each tool to its own endpoint automatically.
- Searching LLM configuration is stored in admin settings: `searching_base_url`, `searching_api_key`, `searching_model`, and optional `searching_api_id`.

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
