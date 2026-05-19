# API Draft

All JSON responses use this envelope:

```json
{
  "success": true,
  "data": {}
}
```

Errors use:

```json
{
  "success": false,
  "code": "ERROR_CODE",
  "message": "Human readable message"
}
```

## Auth

- `POST /api/auth/register`
- `POST /api/auth/login`
- `POST /api/auth/logout`
- `GET /api/auth/me`

## Providers

- `GET /api/provider-capabilities`
- `GET /api/admin/providers`
- `POST /api/admin/providers`
- `PATCH /api/admin/providers/{id}`
- `DELETE /api/admin/providers/{id}`

Providers are OpenAI-compatible by default.

## Conversations

- `GET /api/conversations`
- `GET /api/conversations?archived=1`
- `POST /api/conversations`
- `GET /api/conversations/{id}`
- `PATCH /api/conversations/{id}`
- `DELETE /api/conversations/{id}`

## Messages

- `DELETE /api/messages/{id}`
- `POST /api/messages/{id}/version`

## Streaming

`POST /api/chat/stream`

The endpoint returns `text/event-stream`.

Request body:

```json
{
  "conversation_id": 1,
  "content": "Hello",
  "provider_id": 0,
  "mode": "send",
  "message_id": 0
}
```

Modes:

- `send`: append a new user message and assistant reply.
- `regenerate`: pass an assistant `message_id`; the server removes that assistant reply and later messages, then generates a new reply from the previous user message.
- `edit`: pass a user `message_id` and replacement `content`; the server updates that user message, removes later messages, then generates a new assistant reply.

Events:

- `message_start`
- `thinking`
- `delta`
- `tool_steps`
- `heartbeat`
- `message_end`
- `conversation_title`
- `message_cancelled`
- `error`
- `done`

## Memories

- `GET /api/memories`
- `POST /api/memories`
- `PATCH /api/memories/{id}`
- `DELETE /api/memories/{id}`
- `POST /api/memories/import`
- `POST /api/memories/extract-conversation`
- `POST /api/memories/recompute-embeddings`

## Health

- `GET /api/health`
