import type { AttachmentPayload, Conversation, Memory, Message, Provider, User } from './types';

type Envelope<T> = {
  success: boolean;
  data?: T;
  code?: string;
  message?: string;
};

async function request<T>(url: string, options: RequestInit = {}): Promise<T> {
  const res = await fetch(url, {
    credentials: 'include',
    ...options,
    headers: {
      ...(options.body instanceof FormData ? {} : { 'Content-Type': 'application/json' }),
      ...(options.headers || {})
    }
  });
  const text = await res.text();
  const json = text ? (JSON.parse(text) as Envelope<T>) : null;
  if (!res.ok || !json || json.success === false) {
    throw new Error(json?.message || '请求失败');
  }
  return json.data as T;
}

export const api = {
  register: (payload: { email: string; name: string; password: string }) =>
    request<{ user: User }>('/api/auth/register', { method: 'POST', body: JSON.stringify(payload) }),
  login: (payload: { email: string; password: string }) =>
    request<{ user: User }>('/api/auth/login', { method: 'POST', body: JSON.stringify(payload) }),
  logout: () => request<{ ok: boolean }>('/api/auth/logout', { method: 'POST' }),
  me: () => request<{ user: User }>('/api/auth/me'),
  providers: () => request<{ providers: Provider[] }>('/api/admin/providers'),
  createProvider: (payload: Partial<Provider>) =>
    request<{ provider: Provider }>('/api/admin/providers', { method: 'POST', body: JSON.stringify(payload) }),
  updateProvider: (id: number, payload: Partial<Provider>) =>
    request<{ provider: Provider }>(`/api/admin/providers/${id}`, { method: 'PATCH', body: JSON.stringify(payload) }),
  deleteProvider: (id: number) => request<{ ok: boolean }>(`/api/admin/providers/${id}`, { method: 'DELETE' }),
  adminSettings: () => request<{ settings: Record<string, { key: string; value: string }> }>('/api/admin/settings'),
  updateAdminSettings: (payload: {
    search_tool_mode: string;
    unifuncs_api_key: string;
    unifuncs_base_url: string;
    web_search_card_result_count: string;
    searching_base_url: string;
    searching_api_key: string;
    searching_model: string;
    searching_api_id: string;
    image_tool_mode: string;
    image_tool_base_url: string;
    image_tool_api_key: string;
    image_generate_model: string;
    image_edit_model: string;
    image_responses_model: string;
    image_chat_model: string;
    image_default_size: string;
    image_edit_size: string;
    image_default_quality: string;
    image_response_format: string;
    title_provider_id?: string;
    memory_provider_id: string;
    embedding_provider_id: string;
    memory_recent_message_limit: string;
    memory_max_actions_per_run: string;
    memory_inject_limit: string;
    embedding_top_k: string;
  }) =>
    request<{ ok: boolean }>('/api/admin/settings', { method: 'PATCH', body: JSON.stringify(payload) }),
  clientSettings: () => request<{ settings: { web_search_card_result_count: number } }>('/api/settings'),
  providerCapabilities: () =>
    request<{ capabilities: { providers: Provider[]; default_provider_id: number } }>('/api/provider-capabilities'),
  conversations: (archived = false) =>
    request<{ conversations: Conversation[] }>('/api/conversations' + (archived ? '?archived=1' : '')),
  createConversation: () =>
    request<{ conversation: Conversation }>('/api/conversations', { method: 'POST', body: JSON.stringify({ title: '新对话' }) }),
  conversation: (id: number) => request<{ conversation: Conversation; messages: Message[] }>(`/api/conversations/${id}`),
  conversationBySession: (sessionID: string) =>
    request<{ conversation: Conversation; messages: Message[] }>(`/api/conversations/session/${encodeURIComponent(sessionID)}`),
  updateConversation: (id: number, payload: Partial<Conversation>) =>
    request<{ conversation: Conversation }>(`/api/conversations/${id}`, { method: 'PATCH', body: JSON.stringify(payload) }),
  clearConversations: () => request<{ ok: boolean; deleted: number }>('/api/conversations', { method: 'DELETE' }),
  deleteConversation: (id: number) => request<{ ok: boolean }>(`/api/conversations/${id}`, { method: 'DELETE' }),
  stopGeneration: (payload: { run_id?: string; assistant_message_id?: number; content?: string }) =>
    request<{ ok: boolean }>('/api/chat/stop', { method: 'POST', body: JSON.stringify(payload) }),
  memories: () =>
    request<{
      memories: Memory[];
      total: number;
      enabled_count: number;
      manual_count: number;
      auto_count: number;
      embedding: { enabled: boolean; model: string; pending_count: number };
    }>('/api/memories'),
  updateMemory: (id: number, payload: { enabled: boolean }) =>
    request<{ memory: Memory }>(`/api/memories/${id}`, { method: 'PATCH', body: JSON.stringify(payload) }),
  deleteMemory: (id: number) => request<{ ok: boolean }>(`/api/memories/${id}`, { method: 'DELETE' })
};

export async function streamChat(
  payload: {
    conversation_id: number;
    content: string;
    provider_id?: number;
    references?: unknown[];
    attachments?: AttachmentPayload[];
    mode?: string;
    message_id?: number;
  },
  onEvent: (event: string, data: any) => void,
  options: { signal?: AbortSignal } = {}
) {
  const res = await fetch('/api/chat/stream', {
    method: 'POST',
    credentials: 'include',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(payload),
    signal: options.signal
  });
  if (!res.ok || !res.body) {
    let message = '连接中断，正在同步会话';
    try {
      const text = await res.text();
      const json = text ? (JSON.parse(text) as Envelope<unknown>) : null;
      if (json?.message) message = json.message;
    } catch {
      // Keep the recovery-friendly default when the upstream sent a non-JSON error page.
    }
    throw new Error(message);
  }
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const parts = buffer.split('\n\n');
    buffer = parts.pop() || '';
    for (const part of parts) {
      let event = 'message';
      let data = '{}';
      for (const line of part.split('\n')) {
        if (line.startsWith('event:')) event = line.slice(6).trim();
        if (line.startsWith('data:')) data = line.slice(5).trim();
      }
      if (event === 'ping') continue;
      try {
        onEvent(event, JSON.parse(data));
      } catch {
        onEvent(event, {});
      }
    }
  }
}
