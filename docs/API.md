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
Provider config also supports:

- `request_mode`: `chat_completions` or `responses`
- `response_format`: raw JSON object to pass through as `response_format` for `chat_completions`, or `text.format` for `responses`

When `request_mode=responses`, the backend uses the OpenAI Responses flow with `tools` and `tool_choice: "auto"`.
For web/current/research questions, the active search-tool mode controls which tools are available:

- `unifuncs`: exposes `web_search` and `web_reader`; `searching` is not provided to the model and is rejected by the backend if called.
- `searching`: exposes `searching`; `web_search` and `web_reader` are not provided to the model and are rejected by the backend if called.

In `unifuncs` mode, the model is instructed to prefer a combined workflow: call `web_search` first to discover candidate pages, then call `web_reader` on the most relevant result URLs before answering.
Built-in tools:

- `get_current_time`: accepts optional `timezone` and returns current time JSON.
- `web_search`: calls UniFuncs Web Search. Arguments are `query` required, plus optional `freshness`, `include_images`, `page`, and `count`. Requires `UNIFUNCS_API_KEY`.
- `web_reader`: calls UniFuncs Web Reader for a concrete webpage URL. Arguments are `url` required, plus optional `format`, `lite_mode`, `include_images`, `max_words`, and `topic`. It shares `UNIFUNCS_API_KEY` with `web_search` and uses the `/api/web-reader/read` endpoint.
- `searching`: calls the configured web-enabled search LLM API. Arguments are `query` required. It uses the configured Searching Base URL, API Key, Model, and optional API ID.

If the model emits `function_call` items, the backend executes the tool locally, emits `tool_steps`, appends `function_call_output`, and continues the Responses loop until the assistant returns a final message.

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

`tool_steps` carries the current tool execution step:

```json
{
  "step": {
    "name": "get_current_time",
    "call_id": "call_123",
    "status": "completed",
    "arguments": "{\"timezone\":\"Asia/Shanghai\"}",
    "output": "{\"ok\":true,\"timezone\":\"Asia/Shanghai\",\"local\":\"2026-05-19T12:00:00+08:00\"}",
    "timestamp": "2026-05-19T04:00:00Z",
    "content_offset": 0
  }
}
```

`content_offset` is the assistant text rune offset when the tool call happened. The assistant message returned by `message_end` stores the same records in `metadata.tool_steps` so the frontend can render tool lines in the right position after refresh.

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
