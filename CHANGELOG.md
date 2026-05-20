# Changelog

## 0.1.1 - 2026-05-19

### Changed

- Added an admin search-tool mode switch between UniFuncs (`web_search` + `web_reader`) and Searching LLM (`searching`), with mutually exclusive runtime tool availability while keeping both configurations saved.
- Changed the default frontend dev command to serve the built preview on port 5178, preventing Vite hot reload from automatically refreshing the page during testing.
- Persisted stopped generations as `stopped` with the visible partial content so refreshing a conversation no longer restores the full upstream completion.
- Kept generation running on the backend after a browser refresh, with the chat view polling pending `streaming` messages until the completed answer is persisted.
- Fixed the mobile composer textarea so internal scrolling is handled directly and pulling past the top or bottom no longer triggers browser text stretch.
- Restyled markdown links in chat messages to use muted text-colored underlines instead of the browser's bright default blue.
- Added the `searching` Responses tool, backed by a configured web-enabled LLM API, and rendered it with the same spinner/bar/check timeline style as `web_reader`.
- Added the `web_reader` Responses tool backed by UniFuncs Web Reader, sharing the existing UniFuncs API key while normalizing to the `/api/web-reader/read` endpoint.
- Strengthened tool-use instructions so web/current/research questions prefer a `web_search` then `web_reader` workflow instead of answering without browsing.
- Rendered `web_reader` calls with the same light-gray spinner/bar/check timeline style as the current-time tool, labeled `浏览网页`.
- Added a graphical tool-call timeline inside assistant messages, including live `tool_steps` updates, `content_offset` placement, a borderless spinner-to-check time line, a UniFuncs-style web search card, and persisted history through message metadata.
- Rendered successful UniFuncs web-search outputs as real mini-browser result cards with host metadata, titles, snippets, and clickable source links.
- Added an admin setting for the web-search card result count, with chat-side rendering controlled by the saved value.
- Added a second Responses tool, `web_search`, backed by the UniFuncs Web Search API and configured through `UNIFUNCS_API_KEY`.
- Normalized UniFuncs search base URLs so `/api`, root host, and the full search endpoint all resolve without double `/api` 404s.
- Added the first Responses API tool loop with `tool_choice: "auto"`, local `get_current_time` execution, `function_call_output` replay, and `tool_steps` tracing.
- Fixed Responses streaming tool-call parsing for providers that emit `function_call` through `response.output_item.done` while leaving `response.completed.output` empty.
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
