# ChatXXX

ChatXXX is a standalone rewrite of the original `chat` subproject.

- Backend: Go
- Frontend: React + Vite + TypeScript
- Database: SQLite
- Runtime boundary: fully independent from the existing PHP main site
- Built-in Responses tools: current time, UniFuncs web search/web reader mode, and Searching LLM mode

The project borrows the product logic and interface patterns from the existing chat module, but does not reuse its PHP runtime, session system, or old database.

## Quick Start

Backend:

```bash
cd backend
cp .env.example .env
go mod tidy
go run ./cmd/server
```

Frontend:

```bash
cd frontend
npm install
npm run dev
```

Default URLs:

- Frontend: `http://127.0.0.1:5178`
- Backend: `http://127.0.0.1:8007`
- Health: `http://127.0.0.1:8007/api/health`

## First Login

Register a user from the frontend. The first registered user becomes `admin`; later users become normal users.

## Docs

- [Development Guide](docs/DEVELOPMENT.md)
- [API Draft](docs/API.md)
- [Changelog](CHANGELOG.md)
