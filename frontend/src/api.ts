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
  providerCapabilities: () =>
    request<{ capabilities: { providers: Provider[]; default_provider_id: number } }>('/api/provider-capabilities'),
  conversations: (archived = false) =>
    request<{ conversations: Conversation[] }>('/api/conversations' + (archived ? '?archived=1' : '')),
  createConversation: () =>
    request<{ conversation: Conversation }>('/api/conversations', { method: 'POST', body: JSON.stringify({ title: '新对话' }) }),
  conversation: (id: number) => request<{ conversation: Conversation; messages: Message[] }>(`/api/conversations/${id}`),
  updateConversation: (id: number, payload: Partial<Conversation>) =>
    request<{ conversation: Conversation }>(`/api/conversations/${id}`, { method: 'PATCH', body: JSON.stringify(payload) }),
  deleteConversation: (id: number) => request<{ ok: boolean }>(`/api/conversations/${id}`, { method: 'DELETE' }),
  memories: () => request<{ memories: Memory[]; total: number; enabled_count: number }>('/api/memories'),
  createMemory: (content: string) => request<{ memory: Memory }>('/api/memories', { method: 'POST', body: JSON.stringify({ content }) })
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
    throw new Error('流式请求失败');
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
      try {
        onEvent(event, JSON.parse(data));
      } catch {
        onEvent(event, {});
      }
    }
  }
}
