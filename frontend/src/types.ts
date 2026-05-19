export type User = {
  id: number;
  email: string;
  name: string;
  role: 'admin' | 'user';
  status: string;
};

export type Provider = {
  id: number;
  name: string;
  provider_type: string;
  base_url: string;
  api_key?: string;
  model: string;
  capabilities: string;
  request_mode: string;
  response_format: string;
  context_window: number;
  max_output_tokens: number;
  is_default: boolean;
  is_visible: boolean;
  is_active: boolean;
};

export type Conversation = {
  id: number;
  user_id: number;
  title: string;
  system_prompt: string;
  summary: string;
  memory_enabled: boolean;
  pinned: boolean;
  archived: boolean;
  archive_category_id: number;
  created_at: string;
  updated_at: string;
};

export type Message = {
  id: number;
  conversation_id: number;
  user_id: number;
  role: 'user' | 'assistant' | 'system' | 'tool';
  content: string;
  reasoning_content: string;
  status: string;
  attachments: string;
  metadata: string;
  version_group_id: number;
  version_index: number;
  is_active_version: boolean;
  parent_user_message_id: number;
  sort_order: number;
  created_at: string;
  updated_at: string;
};

export type AttachmentPayload = {
  name: string;
  type: string;
  size: number;
  content?: string;
  error?: string;
};

export type Memory = {
  id: number;
  content: string;
  source: string;
  category: string;
  weight: number;
  enabled: boolean;
};
