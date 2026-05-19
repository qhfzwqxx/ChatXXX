import React, { useEffect, useMemo, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import {
  Archive,
  ArrowUp,
  Check,
  ChevronDown,
  Copy,
  Edit3,
  Home,
  LogOut,
  Menu,
  Plus,
  RotateCcw,
  Settings,
  SquarePen,
  Sparkles,
  X
} from 'lucide-react';
import { api, streamChat } from './api';
import type { AttachmentPayload, Conversation, Memory, Message, Provider, User } from './types';
import './styles.css';

type AuthMode = 'login' | 'register';

const MAX_ATTACHMENT_BYTES = 512 * 1024;

type ComposerAttachment = AttachmentPayload & {
  id: string;
};

function AuthScreen({
  onAuthed,
  allowRegister = true,
  title = '请先登录',
  description = '登录后可以保存多轮会话、长期记忆和模型配置。',
  adminOnly = false
}: {
  onAuthed: (user: User) => void;
  allowRegister?: boolean;
  title?: string;
  description?: string;
  adminOnly?: boolean;
}) {
  const [mode, setMode] = useState<AuthMode>('login');
  const [email, setEmail] = useState('');
  const [name, setName] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setError('');
    try {
      const result =
        mode === 'register'
          ? await api.register({ email, name, password })
          : await api.login({ email, password });
      if (adminOnly && result.user.role !== 'admin') {
        setError('仅管理员账号可以进入后台');
        return;
      }
      onAuthed(result.user);
    } catch (err) {
      setError(err instanceof Error ? err.message : '操作失败');
    }
  }

  return (
    <main className="auth-shell">
      <section className="auth-visual">
        <div className="brand-mark">
          <Sparkles size={22} />
          ChatXXX
        </div>
        <h1>{title}</h1>
        <p>{description}</p>
      </section>
      <form className="auth-card" onSubmit={submit}>
        <h2>{mode === 'login' ? '登录' : '创建账号'}</h2>
        <p>{allowRegister ? '第一个注册用户会自动成为管理员。' : '仅管理员账号可以进入后台。'}</p>
        <label>
          邮箱
          <input value={email} onChange={(event) => setEmail(event.target.value)} placeholder="you@example.com" />
        </label>
        {allowRegister && mode === 'register' && (
          <label>
            名称
            <input value={name} onChange={(event) => setName(event.target.value)} placeholder="你的名字" />
          </label>
        )}
        <label>
          密码
          <input
            type="password"
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            placeholder="至少 8 位"
          />
        </label>
        {error && <div className="error-line">{error}</div>}
        <button className="primary-btn" type="submit">
          {mode === 'login' ? '登录' : '注册并进入'}
        </button>
        {allowRegister && (
          <button className="text-btn" type="button" onClick={() => setMode(mode === 'login' ? 'register' : 'login')}>
            {mode === 'login' ? '还没有账号？注册' : '已有账号？登录'}
          </button>
        )}
      </form>
    </main>
  );
}

function App() {
  const [user, setUser] = useState<User | null>(null);
  const [booting, setBooting] = useState(true);
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [current, setCurrent] = useState<Conversation | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [providerId, setProviderId] = useState<number>(0);
  const [memories, setMemories] = useState<Memory[]>([]);
  const [draft, setDraft] = useState('');
  const [attachments, setAttachments] = useState<ComposerAttachment[]>([]);
  const [editingMessageId, setEditingMessageId] = useState<number | null>(null);
  const [editingDraft, setEditingDraft] = useState('');
  const [copiedMessageIds, setCopiedMessageIds] = useState<Record<number, boolean>>({});
  const [lingerMessageIds, setLingerMessageIds] = useState<Record<number, boolean>>({});
  const [streaming, setStreaming] = useState(false);
  const [status, setStatus] = useState('');
  const [streamController, setStreamController] = useState<AbortController | null>(null);
  const composerInputRef = useRef<HTMLTextAreaElement | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const copiedTimerRefs = useRef<Record<number, number>>({});
  const lingerTimerRefs = useRef<Record<number, number>>({});
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const [modelMenuOpen, setModelMenuOpen] = useState(false);
  const [memoryOpen, setMemoryOpen] = useState(false);

  useEffect(() => {
    api.me()
      .then((res) => setUser(res.user))
      .catch(() => setUser(null))
      .finally(() => setBooting(false));
  }, []);

  useEffect(() => {
    if (!user) return;
    void refreshAll();
  }, [user]);

  useEffect(() => {
    const mobileSidebarOpen = sidebarOpen && window.matchMedia('(max-width: 900px)').matches;
    const overlayOpen = mobileSidebarOpen || userMenuOpen || modelMenuOpen || memoryOpen;
    document.body.classList.toggle('chat-overlay-open', overlayOpen);
    return () => document.body.classList.remove('chat-overlay-open');
  }, [sidebarOpen, userMenuOpen, modelMenuOpen, memoryOpen]);

  useEffect(() => {
    return () => {
      Object.values(copiedTimerRefs.current).forEach((timer) => window.clearTimeout(timer));
      Object.values(lingerTimerRefs.current).forEach((timer) => window.clearTimeout(timer));
    };
  }, []);

  useEffect(() => {
    resizeComposerInput();
  }, [draft]);

  async function refreshAll() {
    const [convRes, capsRes, memRes] = await Promise.all([
      api.conversations(),
      api.providerCapabilities().catch(() => ({ capabilities: { providers: [], default_provider_id: 0 } })),
      api.memories().catch(() => ({ memories: [], total: 0, enabled_count: 0 }))
    ]);
    const nextProviders = capsRes.capabilities.providers || [];
    setConversations(convRes.conversations);
    setProviders(nextProviders);
    setProviderId((value) => value || capsRes.capabilities.default_provider_id || nextProviders[0]?.id || 0);
    setMemories(memRes.memories || []);
    if (!current && convRes.conversations[0]) {
      await openConversation(convRes.conversations[0]);
    }
  }

  async function openConversation(conversation: Conversation) {
    setCurrent(conversation);
    const res = await api.conversation(conversation.id);
    setMessages(res.messages);
    closeMobileSidebar();
  }

  async function newConversation() {
    const res = await api.createConversation();
    setConversations((items) => [res.conversation, ...items]);
    await openConversation(res.conversation);
  }

  async function send() {
    const content = draft.trim();
    if ((!content && !attachments.length) || streaming) return;
    await runStream({ content, attachments });
  }

  async function regenerate(message: Message) {
    if (streaming || !current || message.role !== 'assistant') return;
    await runStream({ content: '', mode: 'regenerate', messageId: message.id });
  }

  async function submitEdit(message: Message, content: string) {
    const nextContent = content.trim();
    if (streaming || !current || message.role !== 'user' || !nextContent) return;
    setEditingMessageId(null);
    setEditingDraft('');
    await runStream({ content: nextContent, mode: 'edit', messageId: message.id });
  }

  function startEdit(message: Message) {
    setEditingMessageId(message.id);
    setEditingDraft(message.content);
  }

  function showMessageActions(messageID: number) {
    if (lingerTimerRefs.current[messageID]) {
      window.clearTimeout(lingerTimerRefs.current[messageID]);
      delete lingerTimerRefs.current[messageID];
    }
    setLingerMessageIds((items) => ({ ...items, [messageID]: true }));
  }

  function lingerMessageActions(messageID: number) {
    if (lingerTimerRefs.current[messageID]) {
      window.clearTimeout(lingerTimerRefs.current[messageID]);
    }
    lingerTimerRefs.current[messageID] = window.setTimeout(() => {
      setLingerMessageIds((items) => {
        const next = { ...items };
        delete next[messageID];
        return next;
      });
      delete lingerTimerRefs.current[messageID];
    }, 1000);
  }

  async function copyMessage(message: Message) {
    const copied = await copyText(message.content);
    if (copied) {
      setCopiedMessageIds((items) => ({ ...items, [message.id]: true }));
      if (copiedTimerRefs.current[message.id]) {
        window.clearTimeout(copiedTimerRefs.current[message.id]);
      }
      copiedTimerRefs.current[message.id] = window.setTimeout(() => {
        setCopiedMessageIds((items) => {
          const next = { ...items };
          delete next[message.id];
          return next;
        });
        delete copiedTimerRefs.current[message.id];
      }, 1400);
    } else {
      setStatus('复制失败');
    }
  }

  function resizeComposerInput() {
    const input = composerInputRef.current;
    if (!input) return;
    const maxHeight = 124;
    input.style.height = '0px';
    const nextHeight = Math.min(input.scrollHeight, maxHeight);
    input.style.height = `${nextHeight}px`;
    input.style.overflowY = input.scrollHeight > maxHeight ? 'auto' : 'hidden';
  }

  function chooseFiles() {
    if (streaming) return;
    fileInputRef.current?.click();
  }

  async function handleFilesSelected(event: React.ChangeEvent<HTMLInputElement>) {
    const files = Array.from(event.target.files || []);
    event.target.value = '';
    if (!files.length) return;
    const nextAttachments = await Promise.all(files.map(readAttachmentFile));
    setAttachments((items) => [...items, ...nextAttachments]);
  }

  function removeAttachment(id: string) {
    setAttachments((items) => items.filter((item) => item.id !== id));
  }

  async function runStream({
    content,
    attachments: outgoingAttachments = [],
    mode = 'send',
    messageId
  }: {
    content: string;
    attachments?: ComposerAttachment[];
    mode?: 'send' | 'regenerate' | 'edit';
    messageId?: number;
  }) {
    let conversation = current;
    if (!conversation) {
      const res = await api.createConversation();
      conversation = res.conversation;
      setCurrent(conversation);
      setConversations((items) => [conversation!, ...items]);
    }

    if (mode === 'send') {
      setDraft('');
      setAttachments([]);
    }
    setStreaming(true);
    setStatus('');
    const controller = new AbortController();
    setStreamController(controller);
    const baseOrder = messages.find((message) => message.id === messageId)?.sort_order || messages.length * 10;
    const localUser: Message = {
      id: Date.now(),
      conversation_id: conversation.id,
      user_id: user!.id,
      role: 'user',
      content,
      reasoning_content: '',
      status: 'completed',
      attachments: JSON.stringify(outgoingAttachments.map(toAttachmentPayload)),
      metadata: '{}',
      version_group_id: Date.now(),
      version_index: 1,
      is_active_version: true,
      parent_user_message_id: 0,
      sort_order: mode === 'edit' ? baseOrder : messages.length * 10 + 10,
      created_at: new Date().toISOString(),
      updated_at: new Date().toISOString()
    };
    const displayUser =
      mode === 'regenerate'
        ? [...messages].reverse().find((message) => message.role === 'user' && message.sort_order < baseOrder)?.content || ''
        : content;
    const localAssistant: Message = {
      ...localUser,
      id: Date.now() + 1,
      role: 'assistant',
      content: '',
      attachments: '[]',
      status: 'streaming',
      sort_order: mode === 'edit' ? baseOrder + 10 : messages.length * 10 + 20
    };
    setMessages((items) => {
      if (mode === 'regenerate' && messageId) {
        const target = items.find((message) => message.id === messageId);
        const targetOrder = target?.sort_order ?? Number.MAX_SAFE_INTEGER;
        return [...items.filter((message) => message.sort_order < targetOrder), localAssistant];
      }
      if (mode === 'edit' && messageId) {
        const target = items.find((message) => message.id === messageId);
        const targetOrder = target?.sort_order ?? Number.MAX_SAFE_INTEGER;
        return items
          .filter((message) => message.sort_order <= targetOrder)
          .map((message) => (message.id === messageId ? { ...message, content } : message))
          .concat(localAssistant);
      }
      return [...items, localUser, localAssistant];
    });

    try {
      await streamChat(
        {
          conversation_id: conversation.id,
          content: mode === 'regenerate' ? displayUser : content,
          provider_id: providerId,
          attachments: mode === 'send' || mode === 'edit' ? outgoingAttachments.map(toAttachmentPayload) : undefined,
          mode,
          message_id: messageId
        },
        (event, data) => {
          if (event === 'delta') {
            setMessages((items) =>
              items.map((msg) => (msg.id === localAssistant.id ? { ...msg, content: msg.content + (data.text || '') } : msg))
            );
          }
          if (event === 'thinking') return;
          if (event === 'conversation_title') {
            setCurrent((item) => (item ? { ...item, title: data.title } : item));
            setConversations((items) => items.map((item) => (item.id === conversation!.id ? { ...item, title: data.title } : item)));
          }
          if (event === 'message_end') {
            setMessages((items) => items.map((msg) => (msg.id === localAssistant.id ? data.message : msg)));
          }
          if (event === 'error') setStatus(data.message || '生成失败');
        },
        { signal: controller.signal }
      );
      setStatus('');
      const res = await api.conversation(conversation.id);
      setMessages(res.messages);
      const latest = await api.conversations();
      setConversations(latest.conversations);
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') {
        setStatus('');
        setMessages((items) =>
          items.map((msg) => (msg.id === localAssistant.id ? { ...msg, status: 'stopped', content: msg.content || '已停止生成' } : msg))
        );
      } else {
        setStatus(err instanceof Error ? err.message : '生成失败');
      }
    } finally {
      setStreamController(null);
      setStreaming(false);
    }
  }

  function stopStreaming() {
    streamController?.abort();
  }

  async function addMemory(content: string) {
    if (!content.trim()) return;
    await api.createMemory(content.trim());
    const res = await api.memories();
    setMemories(res.memories);
  }

  async function deleteCurrentConversation() {
    if (!current) return;
    if (!window.confirm('确定删除当前对话吗？')) return;
    await api.deleteConversation(current.id);
    setUserMenuOpen(false);
    const latest = await api.conversations();
    setConversations(latest.conversations);
    setMessages([]);
    setCurrent(null);
    if (latest.conversations[0]) {
      await openConversation(latest.conversations[0]);
    }
  }

  function exportCurrentConversation() {
    if (!current) return;
    const blob = new Blob([JSON.stringify({ conversation: current, messages }, null, 2)], { type: 'application/json' });
    const href = URL.createObjectURL(blob);
    const link = document.createElement('a');
    link.href = href;
    link.download = `${current.title || 'chatxxx-conversation'}.json`;
    link.click();
    URL.revokeObjectURL(href);
    setUserMenuOpen(false);
  }

  async function logout() {
    await api.logout();
    setUser(null);
    setCurrent(null);
    setMessages([]);
    setConversations([]);
  }

  function closeMobileSidebar() {
    if (window.matchMedia('(max-width: 900px)').matches) {
      setSidebarOpen(false);
    }
  }

  const activeProvider = useMemo(() => providers.find((provider) => provider.id === providerId), [providers, providerId]);
  const providerName = activeProvider ? activeProvider.name : 'BT';
  const avatar = (user?.name || user?.email || '用').slice(0, 1).toUpperCase();

  if (booting) return <div className="boot">ChatXXX</div>;
  if (!user) return <AuthScreen onAuthed={setUser} />;

  return (
    <main className={'chat-app ' + (sidebarOpen ? 'sidebar-open' : '')}>
      <aside className="chat-sidebar">
        <div className="sidebar-top">
          <button
            className="sidebar-rail-btn sidebar-expand-btn"
            type="button"
            aria-label="展开侧边栏"
            title="展开 / 收起侧边栏"
            onClick={() => setSidebarOpen((value) => !value)}
          >
            <Menu size={19} />
          </button>
          <button className="new-chat-btn" type="button" onClick={newConversation}>
            <SquarePen size={18} strokeWidth={1.8} />
            <span>新对话</span>
          </button>
        </div>

        <div className="sidebar-archive-row">
          <button className="new-chat-btn archive-view-btn" type="button" onClick={() => setMemoryOpen(true)}>
            <Archive size={18} strokeWidth={1.8} />
            <span>记忆</span>
          </button>
        </div>

        <div className="conversation-list">
          {conversations.map((item) => (
            <button
              key={item.id}
              className={'conversation-item ' + (current?.id === item.id ? 'active' : '')}
              type="button"
              onClick={() => void openConversation(item)}
            >
              <span className="conversation-item-title">{item.title || '新对话'}</span>
              <span className="conversation-item-time">{new Date(item.updated_at).toLocaleDateString()}</span>
            </button>
          ))}
          {!conversations.length && <div className="conversation-empty">还没有会话</div>}
        </div>

        <div className="sidebar-bottom">
          <div className="user-menu">
            <button
              className="user-chip"
              type="button"
              aria-expanded={userMenuOpen}
              onClick={() => setUserMenuOpen((value) => !value)}
            >
              <span className="user-avatar">{avatar}</span>
              <span className="user-name">{user.name || user.email}</span>
            </button>
            {userMenuOpen && (
              <div className="user-action-menu">
                <button className="user-action-item" type="button" onClick={() => { setMemoryOpen(true); setUserMenuOpen(false); }}>
                  <Archive size={18} />
                  <span>记忆</span>
                </button>
                <button className="user-action-item" type="button" disabled={!current} onClick={exportCurrentConversation}>
                  <Archive size={18} />
                  <span>导出</span>
                </button>
                <button className="user-action-item danger" type="button" disabled={!current} onClick={() => void deleteCurrentConversation()}>
                  <X size={18} />
                  <span>删除</span>
                </button>
                <a className="user-action-item home-link" href="/">
                  <Home size={18} />
                  <span>返回首页</span>
                </a>
                <button className="user-action-item" type="button" onClick={() => void logout()}>
                  <LogOut size={18} />
                  <span>退出</span>
                </button>
              </div>
            )}
          </div>
        </div>
      </aside>

      <button className="sidebar-mask" type="button" aria-label="关闭会话列表" onClick={() => setSidebarOpen(false)} />

      <section className="chat-main">
        <header className="chat-header">
          <div className="header-left">
            <button className="header-icon-btn mobile-sidebar-btn" type="button" aria-label="打开会话列表" onClick={() => setSidebarOpen(true)}>
              <Menu size={20} />
            </button>
            <div className="model-menu">
              <button
                className={'model-switcher ' + (providers.length <= 1 ? 'is-single' : '')}
                type="button"
                aria-haspopup="listbox"
                aria-expanded={modelMenuOpen}
                onClick={() => setModelMenuOpen((value) => !value)}
                title="切换模型"
              >
                <span className="model-switcher__name">{providerName}</span>
                <ChevronDown className="model-switcher__caret" size={14} />
              </button>
              {modelMenuOpen && (
                <div className="model-menu__list" role="listbox">
                  <button
                    className={'model-menu__item ' + (!providerId ? 'is-current' : '')}
                    type="button"
                    onClick={() => { setProviderId(0); setModelMenuOpen(false); }}
                  >
                    <span className="model-menu__label">自动选择</span>
                    {!providerId && <span className="model-menu__check">✓</span>}
                  </button>
                  {providers.map((provider) => (
                    <button
                      key={provider.id}
                      className={'model-menu__item ' + (providerId === provider.id ? 'is-current' : '')}
                      type="button"
                      onClick={() => { setProviderId(provider.id); setModelMenuOpen(false); }}
                    >
                      <span className="model-menu__label">{provider.name}</span>
                      <span className="model-menu__meta">{provider.model}</span>
                      {providerId === provider.id && <span className="model-menu__check">✓</span>}
                    </button>
                  ))}
                </div>
              )}
            </div>
          </div>
        </header>

        <div className="message-list">
          {messages.map((message) => (
            <MessageBubble
              key={message.id}
              message={message}
              isEditing={editingMessageId === message.id}
              editingDraft={editingDraft}
              copied={!!copiedMessageIds[message.id]}
              linger={!!lingerMessageIds[message.id]}
              streaming={streaming}
              onEditingDraftChange={setEditingDraft}
              onCopy={() => void copyMessage(message)}
              onMouseEnter={() => showMessageActions(message.id)}
              onMouseLeave={() => lingerMessageActions(message.id)}
              onRegenerate={() => void regenerate(message)}
              onStartEdit={() => startEdit(message)}
              onCancelEdit={() => {
                setEditingMessageId(null);
                setEditingDraft('');
              }}
              onSubmitEdit={(content) => void submitEdit(message, content)}
            />
          ))}
          {!messages.length && (
            <div className="empty-state">
              <h1>有什么可以帮忙的？</h1>
            </div>
          )}
        </div>

        <footer className="composer-wrap">
          {status && <div className="status-pill">{status}</div>}
          <form
            className="composer-form"
            onSubmit={(event) => {
              event.preventDefault();
              void send();
            }}
          >
            {attachments.length > 0 && (
              <div className="composer-attachments">
                {attachments.map((attachment) => (
                  <div className={'attachment-chip ' + (attachment.error ? 'has-error' : '')} key={attachment.id}>
                    <span className="attachment-chip__name">{attachment.name}</span>
                    <span className="attachment-chip__meta">
                      {formatFileSize(attachment.size)}
                      {attachment.error ? ` · ${attachment.error}` : ''}
                    </span>
                    <button
                      className="attachment-chip__remove"
                      type="button"
                      aria-label={`移除 ${attachment.name}`}
                      title="移除附件"
                      onClick={() => removeAttachment(attachment.id)}
                    >
                      <X size={14} />
                    </button>
                  </div>
                ))}
              </div>
            )}
            <input ref={fileInputRef} className="file-input" type="file" multiple onChange={(event) => void handleFilesSelected(event)} />
            <button className="composer-plus-btn" type="button" aria-label="添加附件" title="添加附件" onClick={chooseFiles} disabled={streaming}>
              <Plus size={22} />
            </button>
            <textarea
              ref={composerInputRef}
              className="message-input"
              value={draft}
              onChange={(event) => setDraft(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter' && !event.shiftKey) {
                  event.preventDefault();
                  void send();
                }
              }}
              placeholder="给 ChatXXX 发送消息"
              rows={1}
            />
            <button
              className={'send-btn ' + (streaming ? 'is-stopping' : '')}
              type={streaming ? 'button' : 'submit'}
              disabled={!streaming && !draft.trim() && !attachments.length}
              aria-label={streaming ? '停止生成' : '发送'}
              onClick={streaming ? stopStreaming : undefined}
            >
              {streaming ? <span className="stop-square" aria-hidden="true" /> : <ArrowUp size={20} strokeWidth={2.4} />}
            </button>
          </form>
        </footer>
      </section>

      {memoryOpen && (
        <Modal title="记忆" onClose={() => setMemoryOpen(false)}>
          <MemoryPanel memories={memories} onAdd={addMemory} />
        </Modal>
      )}
    </main>
  );
}

function MessageBubble({
  message,
  isEditing,
  editingDraft,
  copied,
  linger,
  streaming,
  onEditingDraftChange,
  onCopy,
  onMouseEnter,
  onMouseLeave,
  onRegenerate,
  onStartEdit,
  onCancelEdit,
  onSubmitEdit
}: {
  message: Message;
  isEditing: boolean;
  editingDraft: string;
  copied: boolean;
  linger: boolean;
  streaming: boolean;
  onEditingDraftChange: (value: string) => void;
  onCopy: () => void;
  onMouseEnter: () => void;
  onMouseLeave: () => void;
  onRegenerate: () => void;
  onStartEdit: () => void;
  onCancelEdit: () => void;
  onSubmitEdit: (content: string) => void;
}) {
  const content = message.content || '';
  const canEdit = message.role === 'user' && !streaming;
  const canRegenerate = message.role === 'assistant' && !streaming && message.status !== 'streaming';

  return (
    <article
      className={'message-row ' + message.role + (message.status === 'streaming' ? ' is-streaming' : '') + (linger ? ' is-actions-visible' : '')}
      onMouseEnter={onMouseEnter}
      onMouseLeave={onMouseLeave}
    >
      <div className="message-body">
        {isEditing ? (
          <form
            className="message-inline-edit"
            onSubmit={(event) => {
              event.preventDefault();
              onSubmitEdit(editingDraft);
            }}
          >
            <textarea
              className="message-inline-edit__input"
              value={editingDraft}
              onChange={(event) => onEditingDraftChange(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter' && !event.shiftKey) {
                  event.preventDefault();
                  onSubmitEdit(editingDraft);
                }
              }}
              autoFocus
            />
            <div className="message-inline-edit__actions">
              <button className="message-inline-edit__btn" type="button" onClick={onCancelEdit}>
                取消
              </button>
              <button className="message-inline-edit__btn message-inline-edit__btn--send" type="submit" disabled={!editingDraft.trim()}>
                发送
              </button>
            </div>
          </form>
        ) : (
          <>
            <MessageAttachments attachments={message.attachments} />
            <div className="message-content">{content}</div>
          </>
        )}
        {!isEditing && (
          <div className="message-actions">
            <button className={'message-action-btn copy-action-btn ' + (copied ? 'is-copied' : '')} type="button" onClick={onCopy} title="复制" aria-label="复制">
              {copied ? <Check size={16} /> : <Copy size={16} />}
            </button>
            {message.role === 'assistant' && (
              <button className="message-action-btn" type="button" onClick={onRegenerate} disabled={!canRegenerate} title="重新生成" aria-label="重新生成">
                <RotateCcw size={16} />
              </button>
            )}
            {message.role === 'user' && (
              <button className="message-action-btn" type="button" onClick={onStartEdit} disabled={!canEdit} title="编辑" aria-label="编辑">
                <Edit3 size={16} />
              </button>
            )}
          </div>
        )}
      </div>
    </article>
  );
}

function MessageAttachments({ attachments }: { attachments: string }) {
  const items = parseAttachments(attachments);
  if (!items.length) return null;
  return (
    <div className="message-attachments">
      {items.map((attachment, index) => (
        <span className="message-attachment" key={`${attachment.name}-${index}`}>
          {attachment.name}
        </span>
      ))}
    </div>
  );
}

function parseAttachments(value: string): AttachmentPayload[] {
  if (!value) return [];
  try {
    const parsed = JSON.parse(value) as AttachmentPayload[];
    return Array.isArray(parsed) ? parsed.filter((item) => item && item.name) : [];
  } catch {
    return [];
  }
}

async function readAttachmentFile(file: File): Promise<ComposerAttachment> {
  const base = {
    id: `${file.name}-${file.size}-${file.lastModified}-${Math.random().toString(16).slice(2)}`,
    name: file.name,
    type: file.type || 'application/octet-stream',
    size: file.size
  };
  if (file.size > MAX_ATTACHMENT_BYTES) {
    return { ...base, error: '文件超过 512KB，未读取内容' };
  }
  try {
    const content = await file.text();
    return { ...base, content };
  } catch {
    return { ...base, error: '暂不支持读取此文件内容' };
  }
}

function toAttachmentPayload(attachment: ComposerAttachment): AttachmentPayload {
  const { id: _id, ...payload } = attachment;
  return payload;
}

function formatFileSize(size: number) {
  if (size < 1024) return `${size} B`;
  if (size < 1024 * 1024) return `${Math.round(size / 1024)} KB`;
  return `${(size / (1024 * 1024)).toFixed(1)} MB`;
}

async function copyText(text: string) {
  try {
    if (navigator.clipboard?.writeText && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // Fall back to the textarea path below for browsers that block clipboard access.
  }

  const textarea = document.createElement('textarea');
  textarea.value = text;
  textarea.setAttribute('readonly', '');
  textarea.style.position = 'fixed';
  textarea.style.left = '-9999px';
  textarea.style.top = '0';
  document.body.appendChild(textarea);
  textarea.focus();
  textarea.select();
  try {
    return document.execCommand('copy');
  } catch {
    return false;
  } finally {
    document.body.removeChild(textarea);
  }
}

function Modal({ title, onClose, children }: { title: string; onClose: () => void; children: React.ReactNode }) {
  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={onClose}>
      <section className="modal-card" role="dialog" aria-modal="true" aria-label={title} onMouseDown={(event) => event.stopPropagation()}>
        <header className="modal-head">
          <h2>{title}</h2>
          <button className="modal-close" type="button" onClick={onClose} aria-label="关闭">
            <X size={18} />
          </button>
        </header>
        {children}
      </section>
    </div>
  );
}

function MemoryPanel({ memories, onAdd }: { memories: Memory[]; onAdd: (content: string) => Promise<void> | void }) {
  const [draft, setDraft] = useState('');

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    await onAdd(draft);
    setDraft('');
  }

  return (
    <div className="side-panel">
      <form className="inline-form" onSubmit={submit}>
        <input value={draft} onChange={(event) => setDraft(event.target.value)} placeholder="添加一条记忆" />
        <button type="submit">
          <Plus size={16} />
          添加
        </button>
      </form>
      <div className="memory-list">
        {memories.map((memory) => (
          <div className="memory-card" key={memory.id}>
            {memory.content}
          </div>
        ))}
        {!memories.length && <div className="empty-hint">暂无记忆</div>}
      </div>
    </div>
  );
}

function AdminApp() {
  const [user, setUser] = useState<User | null>(null);
  const [booting, setBooting] = useState(true);

  useEffect(() => {
    api.me()
      .then((res) => setUser(res.user))
      .catch(() => setUser(null))
      .finally(() => setBooting(false));
  }, []);

  if (booting) return <div className="boot">ChatXXX</div>;
  if (!user) {
    return (
      <AuthScreen
        onAuthed={setUser}
        allowRegister={false}
        title="管理员后台"
        description="请使用管理员账号登录后继续。"
        adminOnly
      />
    );
  }
  if (user.role !== 'admin') return <ForbiddenScreen />;

  return <AdminDashboard user={user} onLogout={() => void api.logout().then(() => setUser(null))} />;
}

function ForbiddenScreen() {
  return (
    <main className="auth-shell">
      <section className="auth-visual">
        <div className="brand-mark">
          <Sparkles size={22} />
          ChatXXX
        </div>
        <h1>没有权限</h1>
        <p>这个后台只允许管理员账号访问。</p>
      </section>
      <div className="auth-card">
        <h2>403</h2>
        <p>请切换到管理员账号后再访问 /admin。</p>
      </div>
    </main>
  );
}

function AdminDashboard({ user, onLogout }: { user: User; onLogout: () => void }) {
  const [providers, setProviders] = useState<Provider[]>([]);
  const [activeTab, setActiveTab] = useState<'providers' | 'usage'>('providers');

  async function refreshProviders() {
    const res = await api.providers();
    setProviders(res.providers || []);
  }

  useEffect(() => {
    void refreshProviders();
  }, []);

  return (
    <main className="admin-shell">
      <aside className="admin-sidebar">
        <div className="admin-sidebar-head">
          <div>
            <div className="brand-mark">
              <Sparkles size={20} />
              Admin
            </div>
            <div className="admin-user">{user.name || user.email}</div>
          </div>
          <button className="ghost-btn" type="button" onClick={onLogout}>
            <LogOut size={16} />
            退出
          </button>
        </div>
        <button className={'admin-nav-item ' + (activeTab === 'providers' ? 'active' : '')} type="button" onClick={() => setActiveTab('providers')}>
          <Settings size={16} />
          LLM 配置
        </button>
        <button className={'admin-nav-item ' + (activeTab === 'usage' ? 'active' : '')} type="button" onClick={() => setActiveTab('usage')}>
          <Archive size={16} />
          使用情况
        </button>
      </aside>
      <section className="admin-main">
        <header className="admin-header">
          <h1>后台管理</h1>
          <p>/admin</p>
        </header>
        {activeTab === 'providers' ? <ProviderPanel providers={providers} onChanged={refreshProviders} /> : <UsagePanel />}
      </section>
    </main>
  );
}

function UsagePanel() {
  return (
    <div className="admin-panel">
      <h2>使用情况</h2>
      <p>这里以后可以放调用量、成本、错误率等统计。</p>
    </div>
  );
}

function ProviderPanel({ providers, onChanged }: { providers: Provider[]; onChanged: () => Promise<void> | void }) {
  const [form, setForm] = useState({
    name: '',
    base_url: '',
    api_key: '',
    model: '',
    request_mode: 'chat_completions',
    response_format: '',
    is_default: true,
    is_visible: true,
    is_active: true
  });
  const [error, setError] = useState('');

  async function save(event: React.FormEvent) {
    event.preventDefault();
    setError('');
    try {
      await api.createProvider({
        ...form,
        provider_type: 'openai_compatible',
        capabilities: '{"input":{"text":true},"output":{"text":true},"features":{"stream":true}}'
      });
      setForm({
        name: '',
        base_url: '',
        api_key: '',
        model: '',
        request_mode: 'chat_completions',
        response_format: '',
        is_default: true,
        is_visible: true,
        is_active: true
      });
      await onChanged();
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败');
    }
  }

  return (
    <div className="admin-panel">
      <form className="settings-grid" onSubmit={save}>
        <input placeholder="名称" value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} />
        <input
          placeholder="Base URL，例如 https://api.openai.com/v1"
          value={form.base_url}
          onChange={(event) => setForm({ ...form, base_url: event.target.value })}
        />
        <input placeholder="API Key" value={form.api_key} onChange={(event) => setForm({ ...form, api_key: event.target.value })} />
        <input placeholder="模型，例如 gpt-4o-mini" value={form.model} onChange={(event) => setForm({ ...form, model: event.target.value })} />
        <label className="field-block">
          请求接口
          <select value={form.request_mode} onChange={(event) => setForm({ ...form, request_mode: event.target.value })}>
            <option value="chat_completions">chat/completions</option>
            <option value="responses">responses</option>
          </select>
        </label>
        <label className="field-block">
          response_format
          <textarea
            rows={4}
            placeholder='例如 {"type":"json_schema","json_schema":{"name":"demo","strict":true,"schema":{"type":"object","properties":{}}}}'
            value={form.response_format}
            onChange={(event) => setForm({ ...form, response_format: event.target.value })}
          />
        </label>
        <p className="field-help">`responses` 模式会把这里的 JSON 原样透传到 `text.format`；`chat/completions` 会透传到 `response_format`。</p>
        {error && <div className="error-line">{error}</div>}
        <button className="primary-btn" type="submit">
          保存 Provider
        </button>
      </form>
      <div className="provider-list">
        {providers.map((provider) => (
          <div className="provider-card" key={provider.id}>
            <strong>{provider.name}</strong>
            <span>{provider.model}</span>
            <small>{provider.base_url}</small>
            <small>{provider.request_mode === 'responses' ? 'responses' : 'chat/completions'}</small>
            {provider.response_format && <small>{provider.response_format}</small>}
          </div>
        ))}
        {!providers.length && <div className="empty-hint">暂无 Provider</div>}
      </div>
    </div>
  );
}

function Root() {
  return window.location.pathname.startsWith('/admin') ? <AdminApp /> : <App />;
}

createRoot(document.getElementById('root')!).render(<Root />);
