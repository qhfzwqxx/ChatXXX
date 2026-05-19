# Changelog

## 0.1.1 - 2026-05-19

### Changed

- Added admin-side provider controls for OpenAI `chat/completions` vs `responses`, plus raw `response_format` passthrough.
- Reworked the React chat frontend to closely match the original `/public/chat` UI: collapsible rail sidebar, old-style conversation list, model switcher, centered message stream, rounded composer, and user action menu.
- Moved memories and provider settings into modal panels opened from the user menu, keeping the primary screen focused on chat.
- Improved mobile layout parity with the original chat UI by using a drawer sidebar, overlay mask, constrained composer width, and stronger long-text wrapping.
- Updated composer controls to use the old-style circular arrow send button and black square stop button during streaming.
- Added per-message action buttons: assistant messages can be copied or regenerated, and user messages can be copied or edited inline.
- Implemented backend stream modes for regeneration and user-message editing so retries truncate old follow-up messages and persist the new answer.
- Split the app into `/` for chat and `/admin` for admin-only provider/LLM configuration.

## 0.1.0 - 2026-05-19

### Added

- Created standalone `chatxxx` project under the repository root.
- Added Go backend scaffold with SQLite persistence, local account auth, sessions, conversations, messages, memories, providers, and SSE streaming.
- Added React/Vite frontend scaffold that mirrors the original chat product shape: login, sidebar, message stream, composer, model switcher, memories, and provider settings.
- Added development documentation and API draft.

### Notes

- This version starts from an empty database and does not migrate the old `private/data/chat.sqlite`.
- Tool calling, MCP, Office generation, embeddings, and image generation are represented as extension points and can be expanded after the core chat loop stabilizes.
