import React, { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import {
  Archive,
  ArrowUp,
  Activity,
  BookMarked,
  Check,
  ChevronDown,
  ChevronLeft,
  CircleCheck,
  Copy,
  Edit3,
  LayoutDashboard,
  LogOut,
  Menu,
  Plus,
  RotateCcw,
  Search,
  Settings,
  SquarePen,
  Sparkles,
  Trash2,
  Wrench,
  X
} from 'lucide-react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import remarkMath from 'remark-math';
import rehypeKatex from 'rehype-katex';
import 'katex/dist/katex.min.css';
import { api, streamChat } from './api';
import type { AttachmentPayload, Conversation, Memory, MemoryHit, MemoryHitPayload, Message, Provider, User } from './types';
import './styles.css';

type AuthMode = 'login' | 'register';

const MAX_ATTACHMENT_BYTES = 512 * 1024;
const MAX_IMAGE_ATTACHMENT_BYTES = 4 * 1024 * 1024;
const IMAGE_ATTACHMENT_TARGET_SIZE = 1024;
const DEFAULT_PROVIDER_CAPABILITIES = '{"input":{"text":true},"output":{"text":true},"features":{"stream":true}}';

type ComposerAttachment = AttachmentPayload & {
  id: string;
};

type ActiveStream = {
  controller: AbortController;
  runID: string;
  conversationID: number;
  localAssistantID: number;
  assistantMessageID: number;
  detached?: boolean;
};

type RecoveredStreamBuffer = {
  queue: string[];
  targetContent: string;
  finalMessage?: Message;
};

type RecoveredStreamPatch = {
  messageID: number;
  text: string;
  finalMessage?: Message;
};

type ConfirmDialogState = {
  title: string;
  message: string;
  confirmLabel?: string;
  tone?: 'default' | 'danger';
  onConfirm: () => Promise<void> | void;
};

type AdminSettings = {
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
  title_provider_id: string;
  memory_provider_id: string;
  embedding_provider_id: string;
  memory_recent_message_limit: string;
  memory_max_actions_per_run: string;
  memory_inject_limit: string;
  embedding_top_k: string;
};

type AdminTab = 'overview' | 'models' | 'tools' | 'memory' | 'runtime';

const emptyAdminSettings: AdminSettings = {
  search_tool_mode: 'unifuncs',
  unifuncs_api_key: '',
  unifuncs_base_url: '',
  web_search_card_result_count: '4',
  searching_base_url: '',
  searching_api_key: '',
  searching_model: '',
  searching_api_id: '',
  image_tool_mode: 'image_api',
  image_tool_base_url: 'https://api.tu-zi.com',
  image_tool_api_key: '',
  image_generate_model: 'gpt-image-2',
  image_edit_model: 'gpt-image-1.5',
  image_responses_model: 'gpt-5.5',
  image_chat_model: 'gpt-4o-image',
  image_default_size: '1024x1024',
  image_edit_size: '1:1',
  image_default_quality: 'auto',
  image_response_format: 'url',
  title_provider_id: '0',
  memory_provider_id: '0',
  embedding_provider_id: '0',
  memory_recent_message_limit: '12',
  memory_max_actions_per_run: '5',
  memory_inject_limit: '20',
  embedding_top_k: '8'
};

const recoveredStreamDelay = 35;
const recoveredStreamInitialPollDelay = 700;
const recoveredStreamPollDelay = 700;

type ToolStep = {
  name: string;
  call_id: string;
  status: string;
  arguments?: string;
  output?: string;
  timestamp?: string;
  content_offset?: number;
};

type WebSearchOutput = {
  ok?: boolean;
  query?: string;
  results?: WebSearchResult[];
  images?: unknown[];
  error?: string;
  message?: string;
};

type WebSearchResult = {
  title?: string;
  url?: string;
  display_url?: string;
  displayUrl?: string;
  snippet?: string;
  site_icon?: string;
  siteIcon?: string;
  site_name?: string;
  siteName?: string;
  date?: string;
};

type ImageToolOutput = {
  ok?: boolean;
  tool?: string;
  created?: number;
  images?: Array<{ url?: string; b64_json?: string }>;
  error?: string;
};

type ProviderFormState = {
  name: string;
  base_url: string;
  api_key: string;
  model: string;
  request_mode: string;
  response_format: string;
  is_default: boolean;
  is_visible: boolean;
  is_active: boolean;
};

const emptyProviderForm: ProviderFormState = {
  name: '',
  base_url: '',
  api_key: '',
  model: '',
  request_mode: 'chat_completions',
  response_format: '',
  is_default: true,
  is_visible: true,
  is_active: true
};

function isWeChatBrowser() {
  return /micromessenger/i.test(window.navigator.userAgent);
}

function WeChatBrowserBlocker() {
  const currentUrl = window.location.href;
  const [copied, setCopied] = useState(false);

  async function copyCurrentUrl() {
    const copiedUrl = await copyText(currentUrl);
    if (!copiedUrl) return;
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1400);
  }

  return (
    <main className="wechat-blocker">
      <section className="wechat-blocker__panel">
        <div className="brand-mark wechat-blocker__brand">
          <Sparkles size={22} />
          ChatXXX
        </div>
        <h1>请在浏览器中打开</h1>
        <p>微信内置浏览器可能导致字体、滚动和控件表现异常。为了获得完整体验，请使用手机自带浏览器、Chrome、Edge 或 Safari 打开。</p>
        <div className="wechat-blocker__steps">
          <span>点击右上角菜单</span>
          <span>选择“在浏览器打开”</span>
        </div>
        <button className="primary-btn wechat-blocker__copy" type="button" onClick={() => void copyCurrentUrl()}>
          <Copy size={17} />
          {copied ? '已复制链接' : '复制当前链接'}
        </button>
      </section>
    </main>
  );
}

function conversationSessionFromPath() {
  const match = window.location.pathname.match(/^\/chat\/([a-f0-9]{32,64})\/?$/i);
  return match ? match[1].toLowerCase() : null;
}

function conversationPath(conversation: Conversation | null) {
  return conversation?.session_id ? `/chat/${conversation.session_id}` : '/';
}

function updateConversationURL(conversation: Conversation | null, options: { replace?: boolean } = {}) {
  if (window.location.pathname.startsWith('/admin')) return;
  const nextPath = conversationPath(conversation);
  if (window.location.pathname === nextPath) return;
  const nextURL = nextPath + window.location.search + window.location.hash;
  if (options.replace) {
    window.history.replaceState({}, '', nextURL);
  } else {
    window.history.pushState({}, '', nextURL);
  }
}

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
  const [toolStepsByMessageId, setToolStepsByMessageId] = useState<Record<number, ToolStep[]>>({});
  const [memoryHitsByMessageId, setMemoryHitsByMessageId] = useState<Record<number, MemoryHitPayload>>({});
  const [webSearchCardResultCount, setWebSearchCardResultCount] = useState(4);
  const [streaming, setStreaming] = useState(false);
  const [streamingConversationIds, setStreamingConversationIds] = useState<Record<number, boolean>>({});
  const [status, setStatus] = useState('');
  const [streamController, setStreamController] = useState<AbortController | null>(null);
  const activeStreamRef = useRef<ActiveStream | null>(null);
  const currentSessionRef = useRef<string>('');
  const messagesRef = useRef<Message[]>([]);
  const composerInputRef = useRef<HTMLTextAreaElement | null>(null);
  const composerScrollRef = useRef<HTMLDivElement | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const messageListRef = useRef<HTMLDivElement | null>(null);
  const shouldStickToBottomRef = useRef(true);
  const forceScrollToBottomRef = useRef(true);
  const userInteractingWithMessagesRef = useRef(false);
  const lastMessageScrollTopRef = useRef(0);
  const lastMessageTouchYRef = useRef<number | null>(null);
  const composerTouchYRef = useRef<number | null>(null);
  const composerScrollLockedRef = useRef(false);
  const messageListScrollLockedRef = useRef(false);
  const copiedTimerRefs = useRef<Record<number, number>>({});
  const refreshPendingTimerRef = useRef<number | null>(null);
  const memoryRefreshTimerRefs = useRef<number[]>([]);
  const conversationRefreshTimerRefs = useRef<number[]>([]);
  const recoveredStreamBuffersRef = useRef<Record<number, RecoveredStreamBuffer>>({});
  const recoveredStreamTimerRefs = useRef<Record<number, number>>({});
  const messageListAutoScrollFrameRef = useRef<number | null>(null);
  const messageListAutoScrollingRef = useRef(false);
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const [modelMenuOpen, setModelMenuOpen] = useState(false);
  const [memoryOpen, setMemoryOpen] = useState(false);
  const [confirmDialog, setConfirmDialog] = useState<ConfirmDialogState | null>(null);
  const [confirming, setConfirming] = useState(false);
  const currentConversationStreaming = !!current && !!streamingConversationIds[current.id];
  const currentConversationStreamingMessage = useMemo(
    () => messages.find((message) => message.role === 'assistant' && message.status === 'streaming'),
    [messages]
  );
  const hasCurrentStreamingMessage = messages.some((message) => message.status === 'streaming');
  const composerStopping = currentConversationStreaming || !!currentConversationStreamingMessage;
  const activeProvider = useMemo(() => providers.find((provider) => provider.id === providerId), [providers, providerId]);
  const providerName = activeProvider ? activeProvider.name : 'BT';
  const avatar = (user?.name || user?.email || '用').slice(0, 1).toUpperCase();

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
    if (!user) return;
    function handlePopState() {
      const sessionID = conversationSessionFromPath();
      void openConversationFromSession(sessionID, { replace: true });
    }
    window.addEventListener('popstate', handlePopState);
    return () => window.removeEventListener('popstate', handlePopState);
  }, [user]);

  useEffect(() => {
    const mobileSidebarOpen = sidebarOpen && window.matchMedia('(max-width: 900px)').matches;
    const overlayOpen = mobileSidebarOpen || userMenuOpen || modelMenuOpen || memoryOpen;
    document.body.classList.toggle('chat-overlay-open', overlayOpen);
    return () => document.body.classList.remove('chat-overlay-open');
  }, [sidebarOpen, userMenuOpen, modelMenuOpen, memoryOpen]);

  useEffect(() => {
    function isEditableTarget(target: EventTarget | null) {
      const element = target instanceof HTMLElement ? target : null;
      return !!element?.closest('input, textarea, select, [contenteditable="true"]');
    }
    function preventTextSelection(event: Event) {
      if (isEditableTarget(event.target)) return;
      event.preventDefault();
    }
    document.addEventListener('selectstart', preventTextSelection);
    document.addEventListener('contextmenu', preventTextSelection);
    return () => {
      document.removeEventListener('selectstart', preventTextSelection);
      document.removeEventListener('contextmenu', preventTextSelection);
    };
  }, []);

  // Block global rubber-band overscroll: only allow scroll inside our containers
  useEffect(() => {
    function handleTouchMove(e: TouchEvent) {
      const target = e.target as HTMLElement | null;
      if (target && (target.closest('.message-list') || target.closest('.composer-form') || target.closest('.modal-card') || target.closest('.conversation-list') || target.closest('.side-panel') || target.closest('.admin-panel') || target.closest('.admin-sidebar'))) {
        return;
      }
      e.preventDefault();
    }
    document.addEventListener('touchmove', handleTouchMove, { passive: false });
    return () => document.removeEventListener('touchmove', handleTouchMove);
  }, []);

  useEffect(() => {
    return () => {
      Object.values(copiedTimerRefs.current).forEach((timer) => window.clearTimeout(timer));
      if (refreshPendingTimerRef.current) window.clearTimeout(refreshPendingTimerRef.current);
      memoryRefreshTimerRefs.current.forEach((timer) => window.clearTimeout(timer));
      memoryRefreshTimerRefs.current = [];
      conversationRefreshTimerRefs.current.forEach((timer) => window.clearTimeout(timer));
      conversationRefreshTimerRefs.current = [];
      Object.values(recoveredStreamTimerRefs.current).forEach((timer) => window.clearTimeout(timer));
      recoveredStreamTimerRefs.current = {};
      recoveredStreamBuffersRef.current = {};
      if (messageListAutoScrollFrameRef.current) window.cancelAnimationFrame(messageListAutoScrollFrameRef.current);
      messageListAutoScrollFrameRef.current = null;
    };
  }, []);

  useEffect(() => {
    messagesRef.current = messages;
  }, [messages]);

  useEffect(() => {
    resizeComposerInput();
  }, [draft]);

  useEffect(() => {
    if (!composerStopping || !current || !hasCurrentStreamingMessage) return;
    const active = activeStreamRef.current;
    if (active && !active.detached && active.conversationID === current.id) return;
    if (refreshPendingTimerRef.current) return;
    refreshPendingTimerRef.current = window.setTimeout(() => {
      void refreshConversationUntilSettled(current.id);
    }, recoveredStreamInitialPollDelay);
    return () => {
      if (refreshPendingTimerRef.current) {
        window.clearTimeout(refreshPendingTimerRef.current);
        refreshPendingTimerRef.current = null;
      }
    };
  }, [current?.id, composerStopping, hasCurrentStreamingMessage]);

  useEffect(() => {
    if (!current) return;
    setStreamingConversationIds((items) => {
      const hasStreamingMessage = messages.some((message) => message.role === 'assistant' && message.status === 'streaming');
      if (hasStreamingMessage) {
        if (items[current.id]) return items;
        return { ...items, [current.id]: true };
      }
      if (!items[current.id]) return items;
      const next = { ...items };
      delete next[current.id];
      return next;
    });
  }, [current?.id, messages]);

  useLayoutEffect(() => {
    const list = messageListRef.current;
    if (!list) return;
    if (forceScrollToBottomRef.current || (shouldStickToBottomRef.current && !userInteractingWithMessagesRef.current)) {
      scheduleMessageListScrollToBottom();
    }
  }, [current?.id, messages]);

  async function refreshAll() {
    const [convRes, capsRes, memRes, settingsRes] = await Promise.all([
      api.conversations(),
      api.providerCapabilities().catch(() => ({ capabilities: { providers: [], default_provider_id: 0 } })),
      api.memories().catch(() => ({ memories: [], total: 0, enabled_count: 0 })),
      api.clientSettings().catch(() => ({ settings: { web_search_card_result_count: 4 } }))
    ]);
    const nextProviders = capsRes.capabilities.providers || [];
    setConversations(convRes.conversations);
    setProviders(nextProviders);
    setProviderId((value) => value || capsRes.capabilities.default_provider_id || nextProviders[0]?.id || 0);
    setMemories(memRes.memories || []);
    setWebSearchCardResultCount(settingsRes.settings.web_search_card_result_count || 4);
    if (!current) {
      const sessionID = conversationSessionFromPath();
      if (sessionID) {
        const opened = await openConversationFromSession(sessionID, { replace: true, conversations: convRes.conversations });
        if (opened) return;
      }
      if (convRes.conversations[0]) {
        await openConversation(convRes.conversations[0], { replace: true });
      } else {
        updateConversationURL(null, { replace: true });
      }
    }
  }

  async function refreshClientSettings() {
    const res = await api.clientSettings();
    setWebSearchCardResultCount(res.settings.web_search_card_result_count || 4);
  }

  async function refreshConversations() {
    const latest = await api.conversations();
    setConversations(latest.conversations);
    setCurrent((item) => {
      if (!item) return item;
      return latest.conversations.find((conversation) => conversation.id === item.id) || item;
    });
  }

  async function refreshConversationUntilSettled(conversationID: number) {
    try {
      const res = await api.conversation(conversationID);
      if (current?.id === conversationID) {
        const active = activeStreamRef.current;
        if (active?.conversationID === conversationID && active.assistantMessageID > 0) {
          setMessages((items) => mergeStreamingRefreshMessages(items, res.messages, active));
        } else {
          applyRecoveredConversationMessages(res.messages);
        }
      }
      const hasStreaming = res.messages.some((message) => message.status === 'streaming');
    if (!hasStreaming) {
      refreshPendingTimerRef.current = null;
      if (current?.id === conversationID) {
        setStatus('');
      }
      setStreamingConversationIds((items) => {
          if (!items[conversationID]) return items;
          const next = { ...items };
          delete next[conversationID];
          return next;
        });
        void refreshConversations();
        scheduleConversationRefreshes();
        return;
      }
      refreshPendingTimerRef.current = window.setTimeout(() => {
        void refreshConversationUntilSettled(conversationID);
      }, recoveredStreamPollDelay);
    } catch {
      refreshPendingTimerRef.current = window.setTimeout(() => {
        void refreshConversationUntilSettled(conversationID);
      }, 1500);
    }
  }

  async function openConversation(conversation: Conversation, options: { replace?: boolean } = {}) {
    detachActiveStreamForOtherConversation(conversation.id);
    requestScrollToBottom();
    currentSessionRef.current = conversation.session_id || '';
    setCurrent(conversation);
    updateConversationURL(conversation, { replace: options.replace });
    const res = await api.conversation(conversation.id);
    currentSessionRef.current = res.conversation.session_id || conversation.session_id || '';
    setCurrent(res.conversation);
    updateConversationURL(res.conversation, { replace: true });
    setMessages(res.messages);
    hydrateTimelineMetadata(res.messages);
    setMemoryHitsByMessageId({});
    if (res.messages.some((message) => message.role === 'assistant' && message.status === 'streaming')) {
      void refreshConversationUntilSettled(res.conversation.id);
    }
    closeMobileSidebar();
  }

  async function openConversationFromSession(
    sessionID: string | null,
    options: { replace?: boolean; conversations?: Conversation[] } = {}
  ) {
    if (!sessionID) {
      const fallback = options.conversations?.[0] || conversations[0];
      if (fallback) {
        await openConversation(fallback, { replace: options.replace });
        return true;
      }
      setCurrent(null);
      setMessages([]);
      currentSessionRef.current = '';
      updateConversationURL(null, { replace: options.replace });
      return false;
    }
    if (currentSessionRef.current === sessionID) return true;
    try {
      requestScrollToBottom();
      const res = await api.conversationBySession(sessionID);
      detachActiveStreamForOtherConversation(res.conversation.id);
      currentSessionRef.current = res.conversation.session_id || sessionID;
      setCurrent(res.conversation);
      setMessages(res.messages);
      hydrateTimelineMetadata(res.messages);
      setMemoryHitsByMessageId({});
      if (res.messages.some((message) => message.role === 'assistant' && message.status === 'streaming')) {
        void refreshConversationUntilSettled(res.conversation.id);
      }
      updateConversationURL(res.conversation, { replace: options.replace });
      closeMobileSidebar();
      return true;
    } catch {
      setStatus('会话不存在或无权访问');
      updateConversationURL(null, { replace: true });
      return false;
    }
  }

  async function newConversation() {
    detachActiveStreamForOtherConversation(null);
    const res = await api.createConversation();
    requestScrollToBottom();
    currentSessionRef.current = res.conversation.session_id || '';
    setCurrent(res.conversation);
    updateConversationURL(res.conversation);
    setMessages([]);
    setMemoryHitsByMessageId({});
    setDraft('');
    setAttachments([]);
    setEditingMessageId(null);
    setEditingDraft('');
    closeMobileSidebar();
    setConversations((items) => [res.conversation, ...items.filter((item) => item.id !== res.conversation.id)]);
    void api.conversations().then((latest) => setConversations(latest.conversations));
  }

  async function send() {
    if (composerStopping) {
      stopStreaming();
      return;
    }
    const content = draft.trim();
    if (!content && !attachments.length) return;
    void refreshClientSettings();
    await runStream({ content, attachments });
  }

  async function regenerate(message: Message) {
    if (composerStopping || !current || message.role !== 'assistant') return;
    void refreshClientSettings();
    await runStream({ content: '', mode: 'regenerate', messageId: message.id });
  }

  async function submitEdit(message: Message, content: string) {
    const nextContent = content.trim();
    if (composerStopping || !current || message.role !== 'user' || !nextContent) return;
    setEditingMessageId(null);
    setEditingDraft('');
    void refreshClientSettings();
    await runStream({ content: nextContent, mode: 'edit', messageId: message.id });
  }

  function startEdit(message: Message) {
    setEditingMessageId(message.id);
    setEditingDraft(message.content);
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
    const scroller = composerScrollRef.current;
    if (!input) return;
    const maxHeight = 124;
    input.style.height = '0px';
    const contentHeight = input.scrollHeight;
    const cappedHeight = Math.min(contentHeight, maxHeight);
    input.style.height = `${cappedHeight}px`;
    input.style.overflowY = contentHeight > maxHeight ? 'auto' : 'hidden';
    if (scroller) {
      scroller.style.height = `${cappedHeight}px`;
      scroller.style.overflowY = 'hidden';
    }
  }

  useEffect(() => {
    const scrollBox = composerInputRef.current;
    if (!scrollBox) return;
    const composerBox = scrollBox;

    function touchStart(event: TouchEvent) {
      composerTouchYRef.current = event.touches[0]?.clientY ?? null;
      composerScrollLockedRef.current = false;
    }

    function touchMove(event: TouchEvent) {
      const previousY = composerTouchYRef.current;
      const currentY = event.touches[0]?.clientY ?? null;
      if (currentY == null) return;
      if (previousY == null) {
        composerTouchYRef.current = currentY;
        return;
      }
      const deltaY = currentY - previousY;
      composerTouchYRef.current = currentY;
      const maxScrollTop = Math.max(0, composerBox.scrollHeight - composerBox.clientHeight);
      if (maxScrollTop <= 1) {
        composerBox.scrollTop = 0;
        event.preventDefault();
        return;
      }
      const atTop = composerBox.scrollTop <= 0;
      const atBottom = composerBox.scrollTop >= maxScrollTop - 1;
      if (composerScrollLockedRef.current || (atTop && deltaY > 0) || (atBottom && deltaY < 0)) {
        composerScrollLockedRef.current = true;
        composerBox.scrollTop = atTop ? 0 : maxScrollTop;
        event.preventDefault();
      }
    }

    function touchEnd() {
      composerTouchYRef.current = null;
      composerScrollLockedRef.current = false;
    }

    composerBox.addEventListener('touchstart', touchStart, { passive: true });
    composerBox.addEventListener('touchmove', touchMove, { passive: false });
    composerBox.addEventListener('touchend', touchEnd, { passive: true });
    composerBox.addEventListener('touchcancel', touchEnd, { passive: true });
    return () => {
      composerBox.removeEventListener('touchstart', touchStart);
      composerBox.removeEventListener('touchmove', touchMove);
      composerBox.removeEventListener('touchend', touchEnd);
      composerBox.removeEventListener('touchcancel', touchEnd);
    };
  }, []);

  function isMessageListNearBottom(list: HTMLDivElement) {
    const distanceToBottom = list.scrollHeight - list.scrollTop - list.clientHeight;
    return distanceToBottom <= 96;
  }

  function requestScrollToBottom() {
    shouldStickToBottomRef.current = true;
    forceScrollToBottomRef.current = true;
  }

  function scheduleMessageListScrollToBottom() {
    if (messageListAutoScrollFrameRef.current) return;
    messageListAutoScrollFrameRef.current = window.requestAnimationFrame(() => {
      messageListAutoScrollFrameRef.current = null;
      const list = messageListRef.current;
      if (!list) return;
      messageListAutoScrollingRef.current = true;
      list.scrollTop = Math.max(0, list.scrollHeight - list.clientHeight);
      lastMessageScrollTopRef.current = list.scrollTop;
      shouldStickToBottomRef.current = true;
      forceScrollToBottomRef.current = false;
      window.requestAnimationFrame(() => {
        messageListAutoScrollingRef.current = false;
      });
    });
  }

  function applyRecoveredConversationMessages(refreshedMessages: Message[]) {
    hydrateTimelineMetadata(refreshedMessages);
    const merged = mergeRecoveredStreamingMessages(
      messagesRef.current,
      refreshedMessages,
      (messageID) => recoveredStreamBuffersRef.current[messageID]?.targetContent
    );
    setMessages(merged.messages);
    merged.patches.forEach(queueRecoveredStreamPatch);
  }

  function hydrateTimelineMetadata(nextMessages: Message[]) {
    const nextToolSteps: Record<number, ToolStep[]> = {};
    const nextMemoryHits: Record<number, MemoryHitPayload> = {};
    nextMessages.forEach((message) => {
      const steps = parseToolSteps(message.metadata);
      if (steps.length) nextToolSteps[message.id] = steps;
      const hits = parseMemoryHits(message.metadata);
      if (hits?.memories?.length) nextMemoryHits[message.id] = hits;
    });
    setToolStepsByMessageId(nextToolSteps);
    setMemoryHitsByMessageId(nextMemoryHits);
  }

  function queueRecoveredStreamPatch(patch: RecoveredStreamPatch) {
    const existing = recoveredStreamBuffersRef.current[patch.messageID];
    const currentContent =
      existing?.targetContent ||
      messagesRef.current.find((message) => message.id === patch.messageID)?.content ||
      '';
    const next: RecoveredStreamBuffer = existing || {
      queue: [],
      targetContent: currentContent
    };
    if (patch.text) {
      next.queue.push(...Array.from(patch.text));
      next.targetContent = currentContent + patch.text;
    }
    if (patch.finalMessage) {
      next.finalMessage = patch.finalMessage;
      next.targetContent = patch.finalMessage.content || next.targetContent;
    }
    recoveredStreamBuffersRef.current[patch.messageID] = next;
    if (next.queue.length === 0) {
      finishRecoveredStreamPatch(patch.messageID);
      return;
    }
    if (!recoveredStreamTimerRefs.current[patch.messageID]) {
      recoveredStreamTimerRefs.current[patch.messageID] = window.setTimeout(() => playRecoveredStreamPatch(patch.messageID), recoveredStreamDelay);
    }
  }

  function playRecoveredStreamPatch(messageID: number) {
    const entry = recoveredStreamBuffersRef.current[messageID];
    if (!entry) {
      delete recoveredStreamTimerRefs.current[messageID];
      return;
    }
    const chunk = entry.queue.splice(0, recoveredStreamChunkRunes(entry.queue.length)).join('');
    if (chunk) {
      setMessages((items) =>
        items.map((message) =>
          message.id === messageID ? { ...message, content: message.content + chunk, status: 'streaming' } : message
        )
      );
    }
    if (entry.queue.length > 0) {
      recoveredStreamTimerRefs.current[messageID] = window.setTimeout(() => playRecoveredStreamPatch(messageID), recoveredStreamDelay);
      return;
    }
    finishRecoveredStreamPatch(messageID);
  }

  function finishRecoveredStreamPatch(messageID: number) {
    const entry = recoveredStreamBuffersRef.current[messageID];
    if (!entry) return;
    const finalMessage = entry.finalMessage;
    delete recoveredStreamBuffersRef.current[messageID];
    if (recoveredStreamTimerRefs.current[messageID]) {
      window.clearTimeout(recoveredStreamTimerRefs.current[messageID]);
      delete recoveredStreamTimerRefs.current[messageID];
    }
    if (finalMessage) {
      setMessages((items) =>
        items.map((message) =>
          message.id === messageID ? { ...finalMessage, content: finalMessage.content || message.content } : message
        )
      );
    }
  }

  function detachActiveStreamForOtherConversation(nextConversationID: number | null) {
    const active = activeStreamRef.current;
    if (!active || active.conversationID === nextConversationID) return;
    active.detached = true;
    active.controller.abort();
    activeStreamRef.current = active;
    setStreaming(false);
    setStreamController(null);
  }

  function pauseAutoFollow() {
    shouldStickToBottomRef.current = false;
    forceScrollToBottomRef.current = false;
  }

  function handleMessageListScroll() {
    const list = messageListRef.current;
    if (!list) return;
    if (messageListAutoScrollingRef.current) {
      lastMessageScrollTopRef.current = list.scrollTop;
      shouldStickToBottomRef.current = isMessageListNearBottom(list);
      return;
    }
    const scrollingTowardHistory = list.scrollTop < lastMessageScrollTopRef.current - 1;
    shouldStickToBottomRef.current = scrollingTowardHistory ? false : isMessageListNearBottom(list);
    lastMessageScrollTopRef.current = list.scrollTop;
  }

  useEffect(() => {
    const messageList = messageListRef.current;
    if (!messageList) return;
    const list = messageList as HTMLDivElement;

    function touchStart() {
      userInteractingWithMessagesRef.current = true;
      messageListScrollLockedRef.current = false;
    }

    function touchMove(event: TouchEvent) {
      const previousY = lastMessageTouchYRef.current;
      const currentY = event.touches[0]?.clientY ?? null;
      if (currentY == null) return;
      if (previousY == null) {
        lastMessageTouchYRef.current = currentY;
        return;
      }
      const deltaY = currentY - previousY; // >0 finger down, scroll toward top
      lastMessageTouchYRef.current = currentY;

      if (deltaY > 2) {
        pauseAutoFollow();
      }

      const maxScrollTop = Math.max(0, list.scrollHeight - list.clientHeight);
      if (maxScrollTop <= 1) {
        list.scrollTop = 0;
        event.preventDefault();
        return;
      }
      const atTop = list.scrollTop <= 0;
      const atBottom = list.scrollTop >= maxScrollTop - 1;
      if (messageListScrollLockedRef.current || (atTop && deltaY > 0) || (atBottom && deltaY < 0)) {
        messageListScrollLockedRef.current = true;
        list.scrollTop = atTop ? 0 : maxScrollTop;
        event.preventDefault();
      }
    }

    function touchEnd() {
      messageListScrollLockedRef.current = false;
      userInteractingWithMessagesRef.current = false;
      if (isMessageListNearBottom(list)) {
        shouldStickToBottomRef.current = true;
      }
      lastMessageTouchYRef.current = null;
    }

    function wheelLock(event: WheelEvent) {
      if (event.deltaY < -1) {
        pauseAutoFollow();
      }
      const maxScrollTop = Math.max(0, list.scrollHeight - list.clientHeight);
      if (maxScrollTop <= 1) return;
      const atTop = list.scrollTop <= 0;
      const atBottom = list.scrollTop >= maxScrollTop - 1;
      if ((atTop && event.deltaY < 0) || (atBottom && event.deltaY > 0)) {
        event.preventDefault();
      }
    }

    list.addEventListener('touchstart', touchStart);
    list.addEventListener('touchmove', touchMove, { passive: false });
    list.addEventListener('touchend', touchEnd);
    list.addEventListener('touchcancel', touchEnd);
    list.addEventListener('wheel', wheelLock, { passive: false });
    return () => {
      list.removeEventListener('touchstart', touchStart);
      list.removeEventListener('touchmove', touchMove);
      list.removeEventListener('touchend', touchEnd);
      list.removeEventListener('touchcancel', touchEnd);
      list.removeEventListener('wheel', wheelLock);
    };
  }, []);

  function chooseFiles() {
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
      currentSessionRef.current = conversation.session_id || '';
      setCurrent(conversation);
      updateConversationURL(conversation);
      setConversations((items) => [conversation!, ...items]);
    }
    detachActiveStreamForOtherConversation(conversation.id);

    if (mode === 'send') {
      setDraft('');
      setAttachments([]);
    }
    requestScrollToBottom();
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
    activeStreamRef.current = {
      controller,
      runID: '',
      conversationID: conversation.id,
      localAssistantID: localAssistant.id,
      assistantMessageID: 0
    };
    setStreamingConversationIds((items) => ({ ...items, [conversation!.id]: true }));
    setToolStepsByMessageId((items) => {
      const next = { ...items, [localAssistant.id]: [] };
      if (mode === 'regenerate' && messageId) {
        delete next[messageId];
      }
      return next;
    });
    setMemoryHitsByMessageId((items) => {
      const next = { ...items };
      next[localAssistant.id] = { method: '', model: '', dim: 0, memories: [] };
      if (mode === 'regenerate' && messageId) {
        delete next[messageId];
      }
      return next;
    });
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
    scheduleConversationRefreshes();

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
          if (event === 'message_start') {
            const userMessage = data.user_message as Message | undefined;
            const assistantMessage = data.assistant_message as Message | undefined;
            const assistantMessageID = typeof assistantMessage?.id === 'number' ? assistantMessage.id : 0;
            activeStreamRef.current = {
              controller,
              runID: typeof data.run_id === 'string' ? data.run_id : '',
              conversationID: conversation!.id,
              localAssistantID: localAssistant.id,
              assistantMessageID
            };
            if (userMessage?.id) {
              setMessages((items) =>
                items.map((message) => (message.id === localUser.id ? { ...userMessage } : message.id === userMessage.id ? { ...userMessage } : message))
              );
            }
            if (assistantMessageID > 0 && assistantMessage) {
              setMessages((items) => bindStreamingAssistantMessage(items, localAssistant.id, assistantMessage));
            }
          }
          if (event === 'delta') {
            const active = activeStreamRef.current;
            const assistantIDs = streamingAssistantIDs(active, localAssistant.id);
            setMessages((items) =>
              items.map((msg) => (assistantIDs.has(msg.id) ? { ...msg, content: msg.content + (data.text || '') } : msg))
            );
          }
          if (event === 'thinking') return;
          if (event === 'tool_steps') {
            const step = normalizeToolStep(data.step);
            if (step) {
              const active = activeStreamRef.current;
              const targetID = active?.assistantMessageID || localAssistant.id;
              setToolStepsByMessageId((items) => ({
                ...items,
                [targetID]: mergeToolStep(items[targetID] || items[localAssistant.id] || [], step)
              }));
            }
          }
          if (event === 'memory_hits') {
            const payload = normalizeMemoryHitPayload(data);
            if (payload && payload.memories.length) {
              const active = activeStreamRef.current;
              const targetID = active?.assistantMessageID || localAssistant.id;
              setMemoryHitsByMessageId((items) => ({ ...items, [targetID]: payload }));
            }
          }
          if (event === 'conversation_title') {
            setCurrent((item) => (item ? { ...item, title: data.title } : item));
            setConversations((items) => items.map((item) => (item.id === conversation!.id ? { ...item, title: data.title } : item)));
          }
          if (event === 'message_end') {
            const message = data.message as Message;
            activeStreamRef.current = null;
            setStreamingConversationIds((items) => {
              const next = { ...items };
              delete next[conversation!.id];
              return next;
            });
            const persistedSteps = parseToolSteps(message?.metadata);
            const persistedMemoryHits = parseMemoryHits(message?.metadata);
            setToolStepsByMessageId((items) => {
              const next = { ...items };
              const existing = message?.id ? next[message.id] : undefined;
              delete next[localAssistant.id];
              if (message?.id && persistedSteps.length) {
                next[message.id] = persistedSteps;
              } else if (message?.id && existing?.length) {
                next[message.id] = existing;
              }
              return next;
            });
            setMemoryHitsByMessageId((items) => {
              const next = { ...items };
              const hits = persistedMemoryHits || (message?.id ? next[message.id] : undefined) || next[localAssistant.id];
              delete next[localAssistant.id];
              if (message?.id && hits?.memories?.length) {
                next[message.id] = hits;
              }
              return next;
            });
            setMessages((items) => items.map((msg) => (msg.id === localAssistant.id || msg.id === message?.id ? data.message : msg)));
          }
          if (event === 'error') setStatus(data.message || '生成失败');
        },
        { signal: controller.signal }
      );
      if (activeStreamRef.current?.detached && activeStreamRef.current.conversationID === conversation.id) {
        return;
      }
      setStatus('');
      const res = await api.conversation(conversation.id);
      setMessages(res.messages);
      await refreshConversations();
      scheduleConversationRefreshes();
      scheduleMemoryRefreshes();
    } catch (err) {
      const detached = activeStreamRef.current?.detached && activeStreamRef.current.conversationID === conversation.id;
      if (err instanceof DOMException && err.name === 'AbortError' && detached) {
        return;
      }
      if (err instanceof DOMException && err.name === 'AbortError') {
        setStatus('');
        setMessages((items) =>
          items.map((msg) => (msg.id === localAssistant.id ? { ...msg, status: 'stopped', content: msg.content || '已停止生成' } : msg))
        );
      } else {
        const active = activeStreamRef.current;
        const canRecover = active?.conversationID === conversation.id && (active.assistantMessageID > 0 || active.localAssistantID === localAssistant.id);
        if (canRecover) {
          setStatus('连接刚刚断开，正在自动同步结果');
          setStreamingConversationIds((items) => ({ ...items, [conversation.id]: true }));
          void refreshConversationUntilSettled(conversation.id);
        } else {
          setStatus(friendlyStreamErrorMessage(err));
        }
      }
    } finally {
      const active = activeStreamRef.current;
      const detached = active?.detached && active.conversationID === conversation.id;
      if (!detached) {
        setStreamingConversationIds((items) => {
          const next = { ...items };
          delete next[conversation!.id];
          return next;
        });
      }
      if (!active || active.conversationID === conversation.id) {
        activeStreamRef.current = null;
      }
      setStreamController(null);
      setStreaming(false);
    }
  }

  async function stopStreaming() {
    const active = activeStreamRef.current;
    const fallbackMessage = currentConversationStreamingMessage;
    const currentContent =
      messages.find((message) => message.id === active?.localAssistantID)?.content ||
      messages.find((message) => message.id === active?.assistantMessageID)?.content ||
      fallbackMessage?.content ||
      '';
    if (active) {
      setMessages((items) =>
        items.map((msg) =>
          msg.id === active.localAssistantID || msg.id === active.assistantMessageID
            ? { ...msg, status: 'stopped', content: msg.content || '已停止生成' }
            : msg
        )
      );
      void api.stopGeneration({
        run_id: active.runID,
        assistant_message_id: active.assistantMessageID,
        content: currentContent || '已停止生成'
      });
      active.controller.abort();
      return;
    }
    if (current && fallbackMessage) {
      setMessages((items) =>
        items.map((msg) =>
          msg.id === fallbackMessage.id ? { ...msg, status: 'stopped', content: msg.content || '已停止生成' } : msg
        )
      );
      setStreamingConversationIds((items) => {
        const next = { ...items };
        delete next[current.id];
        return next;
      });
      void api.stopGeneration({
        assistant_message_id: fallbackMessage.id,
        content: currentContent || '已停止生成'
      });
      return;
    }
    streamController?.abort();
  }

  async function setMemoryEnabled(id: number, enabled: boolean) {
    await api.updateMemory(id, { enabled });
    const res = await api.memories();
    setMemories(res.memories);
  }

  async function deleteMemory(id: number) {
    await api.deleteMemory(id);
    const res = await api.memories();
    setMemories(res.memories);
  }

  function requestConfirm(dialog: ConfirmDialogState) {
    setConfirmDialog(dialog);
  }

  async function handleConfirmDialog() {
    if (!confirmDialog) return;
    setConfirming(true);
    try {
      await confirmDialog.onConfirm();
      setConfirmDialog(null);
    } finally {
      setConfirming(false);
    }
  }

  async function refreshMemories() {
    try {
      const res = await api.memories();
      setMemories(res.memories || []);
    } catch {
      // The memory list is auxiliary; keep the chat flow quiet if this refresh fails.
    }
  }

  function scheduleMemoryRefreshes() {
    memoryRefreshTimerRefs.current.forEach((timer) => window.clearTimeout(timer));
    memoryRefreshTimerRefs.current = [];
    void refreshMemories();
    [1200, 3000, 6000, 10000].forEach((delay) => {
      const timer = window.setTimeout(() => {
        void refreshMemories();
      }, delay);
      memoryRefreshTimerRefs.current.push(timer);
    });
  }

  function scheduleConversationRefreshes() {
    conversationRefreshTimerRefs.current.forEach((timer) => window.clearTimeout(timer));
    conversationRefreshTimerRefs.current = [];
    [800, 1800, 3600].forEach((delay) => {
      const timer = window.setTimeout(() => {
        void refreshConversations();
      }, delay);
      conversationRefreshTimerRefs.current.push(timer);
    });
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
    currentSessionRef.current = '';
    updateConversationURL(null, { replace: true });
  }

  async function clearHistoryConversations() {
    if (!conversations.length && !current) {
      setUserMenuOpen(false);
      return;
    }
    requestConfirm({
      title: '清除历史对话',
      message: '确定要清除所有历史对话吗？此操作不可恢复，但不会删除长期记忆。',
      confirmLabel: '清除',
      tone: 'danger',
      onConfirm: async () => {
        stopStreaming();
        await api.clearConversations();
        setCurrent(null);
        setMessages([]);
        setConversations([]);
        setStreamingConversationIds({});
        currentSessionRef.current = '';
        updateConversationURL(null, { replace: true });
        setUserMenuOpen(false);
        closeMobileSidebar();
      }
    });
  }

  function closeMobileSidebar() {
    if (window.matchMedia('(max-width: 900px)').matches) {
      setSidebarOpen(false);
    }
  }

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

        <button
          className="sidebar-collapse-handle"
          type="button"
          aria-label="收起侧边栏"
          title="收起侧边栏"
          onClick={() => setSidebarOpen(false)}
        >
          <ChevronLeft size={17} strokeWidth={2.2} />
        </button>

        <div className="sidebar-archive-row">
          <button className="new-chat-btn archive-view-btn" type="button" onClick={() => setMemoryOpen(true)}>
            <BookMarked size={18} strokeWidth={1.8} />
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
                <button className="user-action-item" type="button" disabled={!current} onClick={exportCurrentConversation}>
                  <Archive size={18} />
                  <span>导出</span>
                </button>
                <button className="user-action-item" type="button" disabled={!conversations.length && !current} onClick={() => void clearHistoryConversations()}>
                  <Trash2 size={18} />
                  <span>清除历史对话</span>
                </button>
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
                  {providers.map((provider) => (
                    <button
                      key={provider.id}
                      className={'model-menu__item ' + (providerId === provider.id ? 'is-current' : '')}
                      type="button"
                      onClick={() => { setProviderId(provider.id); setModelMenuOpen(false); }}
                    >
                      <span className="model-menu__text">
                        <span className="model-menu__label">{provider.name}</span>
                        <span className="model-menu__meta">{provider.model}</span>
                      </span>
                      {providerId === provider.id && (
                        <span className="model-menu__check" aria-label="当前模型">
                          <CircleCheck size={16} strokeWidth={2.2} />
                        </span>
                      )}
                    </button>
                  ))}
                </div>
              )}
            </div>
          </div>
        </header>

        <div
          className="message-list"
          ref={messageListRef}
          onScroll={handleMessageListScroll}
        >
          {messages.map((message) => (
            <MessageBubble
              key={message.id}
              message={message}
              isEditing={editingMessageId === message.id}
              editingDraft={editingDraft}
              copied={!!copiedMessageIds[message.id]}
              streaming={composerStopping}
              toolSteps={toolStepsByMessageId[message.id] || parseToolSteps(message.metadata)}
              memoryHits={memoryHitsByMessageId[message.id] || parseMemoryHits(message.metadata)}
              webSearchCardResultCount={webSearchCardResultCount}
              onEditingDraftChange={setEditingDraft}
              onCopy={() => void copyMessage(message)}
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
              if (composerStopping) {
                stopStreaming();
                return;
              }
              void send();
            }}
          >
            {attachments.length > 0 && (
              <div className="composer-attachments">
                {attachments.map((attachment) => (
                  <div className={'attachment-chip ' + (attachment.error ? 'has-error' : '')} key={attachment.id}>
                    {attachment.preview && (
                      <span className="attachment-chip__thumb">
                        <img src={attachment.preview} alt="" />
                      </span>
                    )}
                    <span className="attachment-chip__name">{attachment.name}</span>
                    <span className="attachment-chip__meta">
                      {formatFileSize(attachment.size)}
                      {attachment.width && attachment.height ? ` · ${attachment.width}x${attachment.height}` : ''}
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
            <button className="composer-plus-btn" type="button" aria-label="添加附件" title="添加附件" onClick={chooseFiles}>
              <Plus size={22} />
            </button>
            <div className="message-input-scroll" ref={composerScrollRef}>
              <textarea
                ref={composerInputRef}
                className="message-input"
                value={draft}
                onChange={(event) => setDraft(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' && !event.shiftKey) {
                    event.preventDefault();
                    if (composerStopping) {
                      stopStreaming();
                      return;
                    }
                    void send();
                  }
                }}
                placeholder="给 ChatXXX 发送消息"
                rows={1}
              />
            </div>
            <button
              className={'send-btn ' + (composerStopping ? 'is-stopping' : '')}
              type={composerStopping ? 'button' : 'submit'}
              disabled={!composerStopping && !draft.trim() && !attachments.length}
              aria-label={composerStopping ? '停止生成' : '发送'}
              onClick={(event) => {
                if (!composerStopping) return;
                event.preventDefault();
                event.stopPropagation();
                stopStreaming();
              }}
            >
              {composerStopping ? <span className="stop-square" aria-hidden="true" /> : <ArrowUp className="send-arrow" size={20} strokeWidth={2.4} />}
            </button>
          </form>
        </footer>
      </section>

      {memoryOpen && (
        <Modal title="记忆" onClose={() => setMemoryOpen(false)} className="memory-modal-card" backdropClassName="memory-modal-backdrop">
          <MemoryPanel
            memories={memories}
            onToggle={(memory) => {
              const nextEnabled = !memory.enabled;
              requestConfirm({
                title: nextEnabled ? '启用记忆' : '停用记忆',
                message: nextEnabled
                  ? '确定要重新启用这条长期记忆吗？之后它会参与相关对话的记忆检索。'
                  : '确定要停用这条长期记忆吗？停用后它不会参与后续对话的记忆检索。',
                confirmLabel: nextEnabled ? '启用' : '停用',
                tone: nextEnabled ? 'default' : 'danger',
                onConfirm: () => setMemoryEnabled(memory.id, nextEnabled)
              });
            }}
            onDelete={(memory) => {
              requestConfirm({
                title: '删除记忆',
                message: '确定要删除这条长期记忆吗？此操作不可恢复。',
                confirmLabel: '删除',
                tone: 'danger',
                onConfirm: () => deleteMemory(memory.id)
              });
            }}
          />
        </Modal>
      )}
      {confirmDialog && (
        <ConfirmDialog
          dialog={confirmDialog}
          confirming={confirming}
          onCancel={() => {
            if (!confirming) setConfirmDialog(null);
          }}
          onConfirm={() => void handleConfirmDialog()}
        />
      )}
    </main>
  );
}

function MessageBubble({
  message,
  isEditing,
  editingDraft,
  copied,
  streaming,
  toolSteps,
  memoryHits,
  webSearchCardResultCount,
  onEditingDraftChange,
  onCopy,
  onRegenerate,
  onStartEdit,
  onCancelEdit,
  onSubmitEdit
}: {
  message: Message;
  isEditing: boolean;
  editingDraft: string;
  copied: boolean;
  streaming: boolean;
  toolSteps: ToolStep[];
  memoryHits?: MemoryHitPayload;
  webSearchCardResultCount: number;
  onEditingDraftChange: (value: string) => void;
  onCopy: () => void;
  onRegenerate: () => void;
  onStartEdit: () => void;
  onCancelEdit: () => void;
  onSubmitEdit: (content: string) => void;
}) {
  const content = message.content || '';
  const isAssistantStreaming = message.role === 'assistant' && message.status === 'streaming';
  const canEdit = message.role === 'user' && !streaming;
  const canRegenerate = message.role === 'assistant' && !streaming && message.status !== 'streaming';
  const showActions = !isEditing && !(message.role === 'assistant' && message.status === 'streaming');

  return (
    <article
      className={'message-row ' + message.role + (message.status === 'streaming' ? ' is-streaming' : '')}
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
            {message.role === 'assistant' ? (
              <AssistantMessageTimeline
                content={content}
                streaming={isAssistantStreaming}
                toolSteps={toolSteps}
                memoryHits={memoryHits}
                webSearchCardResultCount={webSearchCardResultCount}
              />
            ) : (
              <MarkdownContent content={content} />
            )}
          </>
        )}
        {showActions && (
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

function MarkdownContent({ content, streamingCursor = false }: { content: string; streamingCursor?: boolean }) {
  const normalized = useMemo(() => normalizeMathMarkup(content), [content]);
  return (
    <div className={'message-content markdown-body' + (streamingCursor ? ' message-content--streaming' : '')}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm, remarkMath]}
        rehypePlugins={[rehypeKatex]}
        components={{
          a: ({ children, ...props }) => (
            <a {...props} target="_blank" rel="noreferrer">
              {children}
            </a>
          ),
          code: ({ className, children, ...props }) => {
            const inline = !className;
            return inline ? (
              <code className="markdown-inline-code" {...props}>
                {children}
              </code>
            ) : (
              <code className={className} {...props}>
                {children}
              </code>
            );
          }
        }}
      >
        {normalized}
      </ReactMarkdown>
    </div>
  );
}

function AssistantMessageTimeline({
  content,
  streaming,
  toolSteps,
  memoryHits,
  webSearchCardResultCount
}: {
  content: string;
  streaming: boolean;
  toolSteps: ToolStep[];
  memoryHits?: MemoryHitPayload;
  webSearchCardResultCount: number;
}) {
  const parts = useMemo(() => buildAssistantTimelineParts(content, toolSteps), [content, toolSteps]);
  const lastPart = parts[parts.length - 1];
  const lastTextIndex = streaming && lastPart?.type === 'text' ? parts.length - 1 : -1;
  const showToolWaitingDot = streaming && lastPart?.type === 'tools' && lastPart.steps.every((step) => toolStepStatus(step).kind !== 'running');
  const showFallbackThinkingDot = streaming && content.trim() && lastTextIndex === -1 && lastPart?.type !== 'tools';
  const hasMemoryHits = !!memoryHits?.memories?.length;

  if (streaming && !content.trim() && !toolSteps.length && !hasMemoryHits) {
    return <AssistantThinkingDot />;
  }

  return (
    <>
      {/* {hasMemoryHits && <MemoryHitNotice hits={memoryHits} />} */}
      {parts.map((part, index) =>
        part.type === 'text' ? (
          <MarkdownContent content={part.content} streamingCursor={index === lastTextIndex} key={`text-${index}`} />
        ) : (
          <ToolTimeline steps={part.steps} webSearchCardResultCount={webSearchCardResultCount} key={`tools-${part.offset}-${index}`} />
        )
      )}
      {showToolWaitingDot && <AssistantThinkingDot compact />}
      {streaming && !content.trim() && !toolSteps.length && hasMemoryHits && <AssistantThinkingDot compact />}
      {showFallbackThinkingDot && <AssistantThinkingDot compact />}
    </>
  );
}

function MemoryHitNotice({ hits }: { hits: MemoryHitPayload }) {
  const items = hits.memories.slice(0, 10);
  const reranked = hits.method === 'vector_rerank';
  return (
    <section className="memory-hit-notice" aria-label="记忆检索命中">
      <div className="memory-hit-notice__head">
        <span className="memory-hit-notice__title">
          <Sparkles size={14} strokeWidth={2.4} />
          记忆检索命中
        </span>
        <span className="memory-hit-notice__meta">
          {reranked ? '向量召回 + LLM 重排' : '仅向量召回'} · {hits.model || 'embedding'} · {hits.dim || '-'} 维 · {hits.memories.length} 条
        </span>
      </div>
      <div className="memory-hit-notice__items">
        {items.map((memory) => (
          <div className="memory-hit-notice__item" key={memory.id}>
            <div className="memory-hit-notice__row">
              <span className="memory-hit-notice__content">{memory.content}</span>
              <span className="memory-hit-notice__score">{formatMemoryScores(memory)}</span>
            </div>
            {reranked && memory.reason && <span className="memory-hit-notice__reason">{memory.reason}</span>}
            <span className="memory-hit-notice__tags">
              {memory.category && <span>{memory.category}</span>}
              <span>{memory.embedding_status === 'ready' ? '向量就绪' : memoryStatusLabel(memory.embedding_status)}</span>
            </span>
          </div>
        ))}
      </div>
    </section>
  );
}

function formatMemoryScores(memory: MemoryHit) {
  const vector = Number.isFinite(memory.vector_score) ? memory.vector_score : memory.score;
  const parts = [`向量 ${formatScore(vector)}`];
  if (typeof memory.rerank_score === 'number' && Number.isFinite(memory.rerank_score)) {
    parts.push(`重排 ${formatScore(memory.rerank_score)}`);
  }
  return parts.join(' · ');
}

function formatScore(score: number) {
  if (!Number.isFinite(score)) return '-';
  return score.toFixed(3);
}

function AssistantThinkingDot({ compact = false }: { compact?: boolean }) {
  return (
    <span className={'assistant-thinking-dot' + (compact ? ' assistant-thinking-dot--compact' : '')} aria-label="正在生成">
      <span aria-hidden="true" />
    </span>
  );
}

function ToolTimeline({ steps, webSearchCardResultCount }: { steps: ToolStep[]; webSearchCardResultCount: number }) {
  const compactSteps = useMemo(() => compactToolSteps(steps), [steps]);
  if (!compactSteps.length) return null;

  return (
    <div className="tool-timeline" aria-label="工具调用过程">
      {compactSteps.map((step) => {
        const key = step.call_id || `${step.name}-${step.timestamp || ''}`;
        const status = toolStepStatus(step);
        if (step.name === 'web_search') {
          return <WebSearchToolCard key={key} step={step} status={status} resultCount={webSearchCardResultCount} />;
        }
        if (step.name === 'image_generate' || step.name === 'image_edit') {
          return <ImageToolCard key={key} step={step} status={status} />;
        }
        return (
          <section className={'tool-card tool-card--' + status.kind} key={key}>
            <div className="tool-card__head" title={toolStepSummary(step)}>
              <span className="tool-card__indicator" aria-hidden="true">
                <span className="tool-card__orb">
                  {status.kind === 'running' && <span className="tool-card__spinner" />}
                  {status.kind === 'completed' && <Check size={14} strokeWidth={2.6} />}
                  {status.kind === 'error' && <X size={13} strokeWidth={2.5} />}
                </span>
                <span className="tool-card__bar" />
              </span>
              <span className="tool-card__main">
                <span className="tool-card__title">{toolDisplayName(step.name)}</span>
              </span>
            </div>
          </section>
        );
      })}
    </div>
  );
}

function ImageToolCard({ step, status }: { step: ToolStep; status: { kind: string; label: string } }) {
  const [now, setNow] = useState(() => Date.now());
  const output = parseImageToolOutput(step.output);
  const images = output?.images?.map(imageToolSource).filter(Boolean) as string[] || [];
  const title = step.name === 'image_edit' ? '图片编辑' : '图片生成';
  const runningText = step.name === 'image_edit' ? '正在编辑图片' : '正在生成图片';
  const error = output?.error || (status.kind === 'error' ? '图片工具调用失败' : '');
  useEffect(() => {
    if (status.kind !== 'running') return;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [status.kind, step.call_id]);
  const showSlowHint = status.kind === 'running' && isStepOlderThan(step, 30_000, now);
  return (
    <section className={'image-tool-card image-tool-card--' + status.kind} title={toolStepSummary(step)}>
      <div className="image-tool-card__head">
        <span>{status.kind === 'running' ? runningText : title}</span>
        <small>{status.label}</small>
      </div>
      {status.kind === 'running' ? (
        <>
          <div className="image-tool-card__loading" aria-hidden="true">
            <span className="image-tool-card__spinner" />
          </div>
          {showSlowHint && <div className="image-tool-card__hint">当前生图较慢，请耐心等待</div>}
        </>
      ) : error ? (
        <div className="image-tool-card__error">{error}</div>
      ) : images.length ? (
        <div className="image-tool-card__grid">
          {images.map((src, index) => (
            <a href={src} target="_blank" rel="noreferrer" key={`${src}-${index}`}>
              <img src={src} alt="" loading="lazy" referrerPolicy="no-referrer" />
            </a>
          ))}
        </div>
      ) : (
        <div className="image-tool-card__error">没有返回图片</div>
      )}
    </section>
  );
}

function isStepOlderThan(step: ToolStep, thresholdMs: number, now = Date.now()) {
  if (!step.timestamp) return false;
  const startedAt = Date.parse(step.timestamp);
  if (!Number.isFinite(startedAt)) return false;
  return now - startedAt >= thresholdMs;
}

function imageToolSource(item: { url?: string; b64_json?: string }) {
  if (item.url) return item.url;
  if (item.b64_json) return item.b64_json.startsWith('data:') ? item.b64_json : `data:image/png;base64,${item.b64_json}`;
  return '';
}

function WebSearchToolCard({ step, status, resultCount }: { step: ToolStep; status: { kind: string; label: string }; resultCount: number }) {
  const args = parseJSONValue(step.arguments);
  const output = parseWebSearchOutput(step.output);
  const query = jsonStringField(args, 'query') || output?.query || 'UniFuncs';
  const displayCount = clampWebSearchResultCount(resultCount);
  const results = output?.results?.filter((item) => item.title || item.url || item.snippet).slice(0, displayCount) || [];
  const hasResults = status.kind !== 'running' && results.length > 0;
  return (
    <section className={'web-search-tool web-search-tool--' + status.kind} title={toolStepSummary(step)}>
      <div className="web-search-tool__search">
        <span>{query}</span>
        <Search size={14} strokeWidth={2} />
      </div>
      {hasResults ? <WebSearchResults results={results} /> : <WebSearchSkeleton />}
    </section>
  );
}

function WebSearchResults({ results }: { results: WebSearchResult[] }) {
  return (
    <div className="web-search-tool__results web-search-tool__results--real">
      {results.map((result, index) => {
        const title = result.title || result.url || '搜索结果';
        const url = result.url || '#';
        const host = hostFromURL(result.url);
        const displayHost = result.site_name || result.siteName || result.display_url || result.displayUrl || host || '网页';
        const siteIcon = result.site_icon || result.siteIcon || '';
        return (
          <a className="web-search-tool__real-result" href={url} target="_blank" rel="noreferrer" key={`${url}-${index}`}>
            <WebSearchFavicon icon={siteIcon} label={displayHost || title} />
            <span className="web-search-tool__real-meta">
              <span>{displayHost}</span>
              {result.date && <span>{formatSearchDate(result.date)}</span>}
            </span>
            <strong>{title}</strong>
            {result.snippet && <span className="web-search-tool__snippet">{result.snippet}</span>}
          </a>
        );
      })}
    </div>
  );
}

function WebSearchFavicon({ icon, label }: { icon?: string; label: string }) {
  const [failed, setFailed] = useState(false);
  const fallback = label.trim().slice(0, 1).toUpperCase() || 'S';
  if (!icon || failed) {
    return <span className="web-search-tool__favicon">{fallback}</span>;
  }
  return (
    <span className="web-search-tool__favicon web-search-tool__favicon--image">
      <img src={icon} alt="" loading="lazy" referrerPolicy="no-referrer" onError={() => setFailed(true)} />
    </span>
  );
}

function WebSearchSkeleton() {
  return (
    <div className="web-search-tool__results" aria-hidden="true">
      {[0, 1, 2].map((item) => (
        <div className="web-search-tool__result" key={item}>
          <span className="web-search-tool__avatar" />
          <span className="web-search-tool__meta web-search-tool__meta--short" />
          <span className="web-search-tool__meta" />
          <span className="web-search-tool__title" />
          <span className="web-search-tool__source" />
          <span className="web-search-tool__line web-search-tool__line--long" />
          <span className="web-search-tool__line web-search-tool__line--mid" />
          <span className="web-search-tool__line web-search-tool__line--short" />
        </div>
      ))}
    </div>
  );
}

function normalizeMathMarkup(value: string) {
  if (!value) return value;
  return value
    .replace(/\\\[((?:[\s\S]*?))\\\]/g, (_, body: string) => `\n$$\n${body.trim()}\n$$\n`)
    .replace(/\\\(((?:[\s\S]*?))\\\)/g, (_, body: string) => `$${body.trim()}$`);
}

function parseToolSteps(metadata: string): ToolStep[] {
  if (!metadata) return [];
  try {
    const parsed = JSON.parse(metadata) as { tool_steps?: unknown };
    if (!Array.isArray(parsed.tool_steps)) return [];
    return parsed.tool_steps.map(normalizeToolStep).filter((step): step is ToolStep => !!step);
  } catch {
    return [];
  }
}

function parseMemoryHits(metadata: string): MemoryHitPayload | undefined {
  if (!metadata) return undefined;
  try {
    const parsed = JSON.parse(metadata) as { memory_hits?: unknown };
    return normalizeMemoryHitPayload(parsed.memory_hits) || undefined;
  } catch {
    return undefined;
  }
}

function parseImageToolOutput(value?: string): ImageToolOutput | null {
  const parsed = parseJSONValue(value);
  if (!parsed || typeof parsed !== 'object') return null;
  const item = parsed as Record<string, unknown>;
  const rawImages = Array.isArray(item.images) ? item.images : [];
  const images = rawImages
    .map((entry) => {
      if (!entry || typeof entry !== 'object') return null;
      const image = entry as Record<string, unknown>;
      const url = typeof image.url === 'string' ? image.url : '';
      const b64 = typeof image.b64_json === 'string' ? image.b64_json : '';
      if (!url && !b64) return null;
      return { url, b64_json: b64 };
    })
    .filter((entry): entry is { url: string; b64_json: string } => !!entry);
  return {
    ok: typeof item.ok === 'boolean' ? item.ok : undefined,
    tool: typeof item.tool === 'string' ? item.tool : undefined,
    created: typeof item.created === 'number' ? item.created : undefined,
    images,
    error: typeof item.error === 'string' ? item.error : undefined
  };
}

function normalizeMemoryHitPayload(value: unknown): MemoryHitPayload | null {
  if (!value || typeof value !== 'object') return null;
  const item = value as Record<string, unknown>;
  const rawMemories = Array.isArray(item.memories) ? item.memories : [];
  const memories = rawMemories
    .map((entry) => normalizeMemoryHit(entry))
    .filter((entry): entry is MemoryHit => !!entry);
  if (!memories.length) return null;
  return {
    method: typeof item.method === 'string' ? item.method : 'vector',
    model: typeof item.model === 'string' ? item.model : '',
    dim: typeof item.dim === 'number' ? item.dim : 0,
    memories
  };
}

function normalizeMemoryHit(value: unknown): MemoryHit | null {
  if (!value || typeof value !== 'object') return null;
  const item = value as Record<string, unknown>;
  const id = typeof item.id === 'number' ? item.id : 0;
  const content = typeof item.content === 'string' ? item.content.trim() : '';
  if (!id || !content) return null;
  return {
    id,
    user_id: typeof item.user_id === 'number' ? item.user_id : 0,
    content,
    source: typeof item.source === 'string' ? item.source : '',
    category: typeof item.category === 'string' ? item.category : '',
    origin: typeof item.origin === 'string' ? item.origin : '',
    tokens: typeof item.tokens === 'number' ? item.tokens : 0,
    enabled: typeof item.enabled === 'boolean' ? item.enabled : true,
    embedding_model: typeof item.embedding_model === 'string' ? item.embedding_model : '',
    embedding_dim: typeof item.embedding_dim === 'number' ? item.embedding_dim : 0,
    embedding_updated_at: typeof item.embedding_updated_at === 'string' ? item.embedding_updated_at : '',
    embedding_status: item.embedding_status === 'disabled' || item.embedding_status === 'pending' || item.embedding_status === 'stale' ? item.embedding_status : 'ready',
    score: typeof item.score === 'number' ? item.score : 0,
    vector_score: typeof item.vector_score === 'number' ? item.vector_score : typeof item.score === 'number' ? item.score : 0,
    rerank_score: typeof item.rerank_score === 'number' ? item.rerank_score : undefined,
    reason: typeof item.reason === 'string' ? item.reason : undefined,
    created_at: typeof item.created_at === 'string' ? item.created_at : '',
    updated_at: typeof item.updated_at === 'string' ? item.updated_at : ''
  };
}

function normalizeToolStep(value: unknown): ToolStep | null {
  if (!value || typeof value !== 'object') return null;
  const item = value as Record<string, unknown>;
  const name = typeof item.name === 'string' ? item.name : '';
  if (!name) return null;
  return {
    name,
    call_id: typeof item.call_id === 'string' ? item.call_id : '',
    status: typeof item.status === 'string' ? item.status : 'running',
    arguments: typeof item.arguments === 'string' ? item.arguments : undefined,
    output: typeof item.output === 'string' ? item.output : undefined,
    timestamp: typeof item.timestamp === 'string' ? item.timestamp : undefined,
    content_offset: typeof item.content_offset === 'number' ? item.content_offset : undefined
  };
}

function mergeToolStep(steps: ToolStep[], step: ToolStep) {
  const key = step.call_id || `${step.name}-${step.timestamp || ''}`;
  const index = steps.findIndex((item) => (item.call_id || `${item.name}-${item.timestamp || ''}`) === key);
  if (index < 0) return [...steps, step];
  return steps.map((item, itemIndex) => (itemIndex === index ? { ...item, ...step } : item));
}

function compactToolSteps(steps: ToolStep[]) {
  return steps.reduce<ToolStep[]>((items, step) => mergeToolStep(items, step), []);
}

type AssistantTimelinePart =
  | { type: 'text'; content: string }
  | { type: 'tools'; offset: number; steps: ToolStep[] };

function buildAssistantTimelineParts(content: string, steps: ToolStep[]): AssistantTimelinePart[] {
  const compactSteps = compactToolSteps(steps);
  if (!compactSteps.length) return [{ type: 'text', content }];
  const contentRunes = Array.from(content);
  const stepsByOffset = new Map<number, ToolStep[]>();
  compactSteps.forEach((step) => {
    const offset = clampToolOffset(step.content_offset, contentRunes.length);
    stepsByOffset.set(offset, [...(stepsByOffset.get(offset) || []), step]);
  });
  const offsets = Array.from(stepsByOffset.keys()).sort((a, b) => a - b);
  const parts: AssistantTimelinePart[] = [];
  let cursor = 0;
  offsets.forEach((offset) => {
    if (offset > cursor) {
      parts.push({ type: 'text', content: contentRunes.slice(cursor, offset).join('') });
    }
    parts.push({ type: 'tools', offset, steps: stepsByOffset.get(offset) || [] });
    cursor = offset;
  });
  if (cursor < contentRunes.length || parts.length === 0) {
    parts.push({ type: 'text', content: contentRunes.slice(cursor).join('') });
  }
  return parts.filter((part) => part.type === 'tools' || part.content !== '');
}

function clampToolOffset(offset: number | undefined, contentLength: number) {
  if (typeof offset !== 'number' || !Number.isFinite(offset)) return contentLength;
  return Math.max(0, Math.min(contentLength, Math.floor(offset)));
}

function toolDisplayName(name: string) {
  if (name === 'get_current_time') return '读取当前时间';
  if (name === 'web_search') return '搜索网页';
  if (name === 'web_reader') return '浏览网页';
  if (name === 'searching') return '联网搜索';
  return name.replace(/_/g, ' ');
}

function toolStepStatus(step: ToolStep) {
  if (step.status === 'running') return { kind: 'running', label: '运行中' };
  const output = parseJSONValue(step.output);
  if (step.status === 'error' || (output && typeof output === 'object' && (output as { ok?: boolean }).ok === false)) {
    return { kind: 'error', label: '异常' };
  }
  return { kind: 'completed', label: '已完成' };
}

function toolStepSummary(step: ToolStep) {
  const args = parseJSONValue(step.arguments);
  const output = parseJSONValue(step.output);
  const timezone = jsonStringField(args, 'timezone') || jsonStringField(output, 'timezone');
  if (step.name === 'get_current_time') {
    const local = jsonStringField(output, 'local');
    if (local) return `${timezone || '本地时区'} · ${formatToolTime(local)}`;
    if (timezone) return `目标时区 ${timezone}`;
    return '准备读取系统时间';
  }
  if (step.name === 'web_search') {
    const query = jsonStringField(args, 'query') || jsonStringField(output, 'query');
    if (query) return `搜索 ${query}`;
    return '准备搜索网页';
  }
  if (step.name === 'web_reader') {
    const url = jsonStringField(args, 'url') || jsonStringField(output, 'url');
    if (url) return `浏览 ${url}`;
    return '准备浏览网页';
  }
  if (step.name === 'searching') {
    const query = jsonStringField(args, 'query') || jsonStringField(output, 'query');
    if (query) return `联网搜索 ${query}`;
    return '准备联网搜索';
  }
  if (step.name === 'image_generate') {
    const prompt = jsonStringField(args, 'prompt');
    if (prompt) return `生成图片：${prompt}`;
    return '准备生成图片';
  }
  if (step.name === 'image_edit') {
    const prompt = jsonStringField(args, 'prompt');
    if (prompt) return `编辑图片：${prompt}`;
    return '准备编辑图片';
  }
  if (toolStepStatus(step).kind === 'running') return '正在执行';
  return '工具结果已返回';
}

function parseJSONValue(value?: string) {
  if (!value) return null;
  try {
    return JSON.parse(value) as unknown;
  } catch {
    return null;
  }
}

function parseWebSearchOutput(value?: string): WebSearchOutput | null {
  const parsed = parseJSONValue(value);
  if (!parsed || typeof parsed !== 'object') return null;
  const item = parsed as WebSearchOutput;
  return item;
}

function jsonStringField(value: unknown, key: string) {
  if (!value || typeof value !== 'object') return '';
  const field = (value as Record<string, unknown>)[key];
  return typeof field === 'string' ? field : '';
}

function hostFromURL(value?: string) {
  if (!value) return '';
  try {
    return new URL(value).hostname.replace(/^www\./, '');
  } catch {
    return value;
  }
}

function formatSearchDate(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleDateString();
}

function clampWebSearchResultCount(value: number) {
  if (value <= 2) return 2;
  if (value <= 4) return 4;
  if (value <= 6) return 6;
  return 10;
}

function formatToolTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString([], {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit'
  });
}

function MessageAttachments({ attachments }: { attachments: string }) {
  const items = parseAttachments(attachments);
  if (!items.length) return null;
  return (
    <div className="message-attachments">
      {items.map((attachment, index) => (
        attachment.url || attachment.preview || (attachment.type || '').startsWith('image/') ? (
          <a className="message-attachment message-attachment--image" href={attachment.url || attachment.preview || attachment.content} target="_blank" rel="noreferrer" key={`${attachment.name}-${index}`}>
            <img src={attachment.url || attachment.preview || attachment.content} alt="" />
            <span>{attachment.name}</span>
          </a>
        ) : (
          <span className="message-attachment" key={`${attachment.name}-${index}`}>
            {attachment.name}
          </span>
        )
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
  if ((file.type || '').startsWith('image/')) {
    return readImageAttachmentFile(file, base);
  }
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

async function readImageAttachmentFile(file: File, base: { id: string; name: string; type: string; size: number }): Promise<ComposerAttachment> {
  try {
    const processed = await processImageForTool(file);
    return {
      ...base,
      name: processed.name,
      type: 'image/png',
      size: processed.size,
      content: processed.dataUrl,
      preview: processed.dataUrl,
      width: processed.width,
      height: processed.height,
      original_name: file.name,
      original_type: file.type || 'application/octet-stream',
      original_size: file.size
    };
  } catch (err) {
    return { ...base, error: err instanceof Error ? err.message : '图片处理失败' };
  }
}

async function processImageForTool(file: File) {
  const bitmap = await createImageBitmap(file);
  try {
    let side = Math.max(bitmap.width, bitmap.height);
    side = Math.max(16, Math.min(side, IMAGE_ATTACHMENT_TARGET_SIZE));
    side = Math.floor(side / 16) * 16 || 16;
    for (let attempt = 0; attempt < 8; attempt += 1) {
      const canvas = document.createElement('canvas');
      canvas.width = side;
      canvas.height = side;
      const ctx = canvas.getContext('2d');
      if (!ctx) throw new Error('无法处理图片');
      ctx.clearRect(0, 0, side, side);
      const scale = Math.min(side / bitmap.width, side / bitmap.height);
      const width = Math.max(1, Math.round(bitmap.width * scale));
      const height = Math.max(1, Math.round(bitmap.height * scale));
      const left = Math.floor((side - width) / 2);
      const top = Math.floor((side - height) / 2);
      ctx.drawImage(bitmap, left, top, width, height);
      const dataUrl = canvas.toDataURL('image/png');
      const size = dataURLByteLength(dataUrl);
      if (size <= MAX_IMAGE_ATTACHMENT_BYTES || side <= 256) {
        return {
          name: pngFileName(file.name),
          dataUrl,
          size,
          width: side,
          height: side
        };
      }
      side = Math.max(256, Math.floor((side * 0.82) / 16) * 16);
    }
    throw new Error('图片超过 4MB，压缩失败');
  } finally {
    bitmap.close();
  }
}

function dataURLByteLength(dataUrl: string) {
  const comma = dataUrl.indexOf(',');
  const b64 = comma >= 0 ? dataUrl.slice(comma + 1) : dataUrl;
  const padding = b64.endsWith('==') ? 2 : b64.endsWith('=') ? 1 : 0;
  return Math.max(0, Math.floor((b64.length * 3) / 4) - padding);
}

function pngFileName(name: string) {
  const trimmed = name.trim() || 'image';
  return trimmed.replace(/\.[^.]+$/, '') + '.png';
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

function Modal({
  title,
  onClose,
  children,
  className = '',
  backdropClassName = ''
}: {
  title: string;
  onClose: () => void;
  children: React.ReactNode;
  className?: string;
  backdropClassName?: string;
}) {
  return (
    <div className={'modal-backdrop ' + backdropClassName} role="presentation" onMouseDown={onClose}>
      <section className={'modal-card ' + className} role="dialog" aria-modal="true" aria-label={title} onMouseDown={(event) => event.stopPropagation()}>
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

function ConfirmDialog({
  dialog,
  confirming,
  onCancel,
  onConfirm
}: {
  dialog: ConfirmDialogState;
  confirming: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const danger = dialog.tone === 'danger';
  return (
    <div className="confirm-backdrop" role="presentation" onMouseDown={onCancel}>
      <section className="confirm-card" role="dialog" aria-modal="true" aria-label={dialog.title} onMouseDown={(event) => event.stopPropagation()}>
        <div className={'confirm-icon ' + (danger ? 'is-danger' : '')}>
          {danger ? <Trash2 size={20} strokeWidth={2.2} /> : <CircleCheck size={20} strokeWidth={2.2} />}
        </div>
        <div className="confirm-copy">
          <h2>{dialog.title}</h2>
          <p>{dialog.message}</p>
        </div>
        <div className="confirm-actions">
          <button className="confirm-btn" type="button" onClick={onCancel} disabled={confirming}>
            取消
          </button>
          <button className={'confirm-btn confirm-btn--primary ' + (danger ? 'is-danger' : '')} type="button" onClick={onConfirm} disabled={confirming}>
            {confirming ? '处理中...' : dialog.confirmLabel || '确认'}
          </button>
        </div>
      </section>
    </div>
  );
}

function MemoryPanel({
  memories,
  onToggle,
  onDelete
}: {
  memories: Memory[];
  onToggle: (memory: Memory) => void;
  onDelete: (memory: Memory) => void;
}) {
  return (
    <div className="side-panel memory-panel">
      <div className="memory-note">
        <strong>自动长期记忆</strong>
        <span>系统会在回复完成后自动提炼稳定偏好和事实。这里可以查看、停用或删除。</span>
      </div>
      <div className="memory-list">
        {memories.map((memory) => (
          <div className={'memory-card ' + (!memory.enabled ? 'is-disabled' : '')} key={memory.id}>
            <div className="memory-card__badges">
              <span>{memory.source === 'auto' ? '自动' : memory.source}</span>
              {memory.category && <span>{memory.category}</span>}
              <span>{memoryStatusLabel(memory.embedding_status)}</span>
            </div>
            <p>{memory.content}</p>
            <div className="memory-card__footer">
              <small>更新于 {memory.updated_at || '-'}</small>
              <div>
                <button className="text-btn" type="button" onClick={() => onToggle(memory)}>
                  {memory.enabled ? '停用' : '启用'}
                </button>
                <button className="text-btn danger" type="button" onClick={() => onDelete(memory)}>
                  删除
                </button>
              </div>
            </div>
          </div>
        ))}
        {!memories.length && <div className="empty-hint">暂无记忆</div>}
      </div>
    </div>
  );
}

function memoryStatusLabel(status: Memory['embedding_status']) {
  switch (status) {
    case 'ready':
      return '向量就绪';
    case 'pending':
      return '待向量';
    case 'stale':
      return '需重算';
    default:
      return '未启用向量';
  }
}

function streamingAssistantIDs(active: ActiveStream | null, fallbackID: number) {
  const ids = new Set<number>([fallbackID]);
  if (active?.localAssistantID) ids.add(active.localAssistantID);
  if (active?.assistantMessageID) ids.add(active.assistantMessageID);
  return ids;
}

function bindStreamingAssistantMessage(items: Message[], localAssistantID: number, assistant: Message) {
  const local = items.find((message) => message.id === localAssistantID);
  const existing = items.find((message) => message.id === assistant.id);
  const merged = {
    ...assistant,
    content: local?.content || existing?.content || assistant.content,
    status: local?.status === 'streaming' ? 'streaming' : assistant.status
  };
  if (existing) {
    return items
      .filter((message) => message.id !== localAssistantID)
      .map((message) => (message.id === assistant.id ? { ...message, ...merged } : message));
  }
  return items.map((message) => (message.id === localAssistantID ? { ...message, ...merged } : message));
}

function mergeStreamingRefreshMessages(currentItems: Message[], refreshedItems: Message[], active: ActiveStream) {
  const live = currentItems.find((message) => message.id === active.assistantMessageID || message.id === active.localAssistantID);
  if (!live) return refreshedItems;
  return refreshedItems.map((message) =>
    message.id === active.assistantMessageID && message.status === 'streaming'
      ? { ...message, content: live.content || message.content, status: live.status || message.status }
      : message
  );
}

function mergeRecoveredStreamingMessages(
  currentItems: Message[],
  refreshedItems: Message[],
  targetContentForMessage: (messageID: number) => string | undefined
) {
  const patches: RecoveredStreamPatch[] = [];
  const currentByID = new Map(currentItems.map((message) => [message.id, message]));
  const messages = refreshedItems.map((message) => {
    if (message.role !== 'assistant') return message;
    const current = currentByID.get(message.id);
    if (!current) return message;
    const visibleContent = current.content || '';
    const targetContent = targetContentForMessage(message.id) || visibleContent;
    const incomingContent = message.content || '';
    if (targetContent.length > visibleContent.length && incomingContent.startsWith(targetContent)) {
      return { ...message, content: visibleContent, status: 'streaming' };
    }
    const baseContent = incomingContent.startsWith(targetContent) ? targetContent : visibleContent;
    if (incomingContent.length > baseContent.length && incomingContent.startsWith(baseContent)) {
      patches.push({
        messageID: message.id,
        text: incomingContent.slice(baseContent.length),
        finalMessage: message.status === 'streaming' ? undefined : message
      });
      return { ...message, content: visibleContent, status: 'streaming' };
    }
    if (message.status !== 'streaming' && targetContentForMessage(message.id)) {
      patches.push({ messageID: message.id, text: '', finalMessage: message });
      return { ...message, content: visibleContent, status: 'streaming' };
    }
    return message;
  });
  return { messages, patches };
}

function recoveredStreamChunkRunes(remaining: number) {
  if (remaining > 80) return 4;
  if (remaining > 30) return 3;
  return 1;
}

function friendlyStreamErrorMessage(err: unknown) {
  if (err instanceof DOMException && err.name === 'AbortError') return '';
  const message = err instanceof Error ? err.message : '';
  if (!message) return '连接中断，正在同步会话';
  if (/network|failed to fetch|load failed|cancelled|canceled|terminated|connection/i.test(message)) {
    return '连接中断，正在同步会话';
  }
  return message;
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
  const [adminSettings, setAdminSettings] = useState<AdminSettings>(emptyAdminSettings);
  const [activeTab, setActiveTab] = useState<AdminTab>('overview');

  async function refreshProviders() {
    const res = await api.providers();
    setProviders(res.providers || []);
  }

  async function refreshSettings() {
    const res = await api.adminSettings();
    const settings = res.settings || {};
    setAdminSettings({
      search_tool_mode: settings.search_tool_mode?.value || 'unifuncs',
      unifuncs_api_key: settings.unifuncs_api_key?.value || '',
      unifuncs_base_url: settings.unifuncs_base_url?.value || '',
      web_search_card_result_count: settings.web_search_card_result_count?.value || '4',
      searching_base_url: settings.searching_base_url?.value || '',
      searching_api_key: settings.searching_api_key?.value || '',
      searching_model: settings.searching_model?.value || '',
      searching_api_id: settings.searching_api_id?.value || '',
      image_tool_mode: settings.image_tool_mode?.value || 'image_api',
      image_tool_base_url: settings.image_tool_base_url?.value || 'https://api.tu-zi.com',
      image_tool_api_key: settings.image_tool_api_key?.value || '',
      image_generate_model: settings.image_generate_model?.value || 'gpt-image-2',
      image_edit_model: settings.image_edit_model?.value || 'gpt-image-1.5',
      image_responses_model: settings.image_responses_model?.value || 'gpt-5.5',
      image_chat_model: settings.image_chat_model?.value || 'gpt-4o-image',
      image_default_size: settings.image_default_size?.value || '1024x1024',
      image_edit_size: settings.image_edit_size?.value || '1:1',
      image_default_quality: settings.image_default_quality?.value || 'auto',
      image_response_format: settings.image_response_format?.value || 'url',
      title_provider_id: settings.title_provider_id?.value || '0',
      memory_provider_id: settings.memory_provider_id?.value || '0',
      embedding_provider_id: settings.embedding_provider_id?.value || '0',
      memory_recent_message_limit: settings.memory_recent_message_limit?.value || '12',
      memory_max_actions_per_run: settings.memory_max_actions_per_run?.value || '5',
      memory_inject_limit: settings.memory_inject_limit?.value || '20',
      embedding_top_k: settings.embedding_top_k?.value || '8'
    });
  }

  useEffect(() => {
    void refreshProviders();
    void refreshSettings();
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
        <nav className="admin-nav" aria-label="后台分类">
          <button className={'admin-nav-item ' + (activeTab === 'overview' ? 'active' : '')} type="button" onClick={() => setActiveTab('overview')}>
            <LayoutDashboard size={16} />
            概览
          </button>
          <button className={'admin-nav-item ' + (activeTab === 'models' ? 'active' : '')} type="button" onClick={() => setActiveTab('models')}>
            <Settings size={16} />
            模型
          </button>
          <button className={'admin-nav-item ' + (activeTab === 'tools' ? 'active' : '')} type="button" onClick={() => setActiveTab('tools')}>
            <Wrench size={16} />
            工具
          </button>
          <button className={'admin-nav-item ' + (activeTab === 'memory' ? 'active' : '')} type="button" onClick={() => setActiveTab('memory')}>
            <BookMarked size={16} />
            记忆
          </button>
          <button className={'admin-nav-item ' + (activeTab === 'runtime' ? 'active' : '')} type="button" onClick={() => setActiveTab('runtime')}>
            <Activity size={16} />
            运行
          </button>
        </nav>
      </aside>
      <section className="admin-main">
        <header className="admin-header">
          <h1>后台管理</h1>
          <p>/admin</p>
        </header>
        {activeTab === 'overview' ? (
          <OverviewPanel providers={providers} settings={adminSettings} onNavigate={setActiveTab} />
        ) : activeTab === 'models' ? (
          <ProviderPanel providers={providers} settings={adminSettings} onSettingsChanged={refreshSettings} onChanged={refreshProviders} />
        ) : activeTab === 'tools' ? (
          <UniFuncsPanel settings={adminSettings} onChanged={refreshSettings} />
        ) : activeTab === 'memory' ? (
          <MemorySettingsPanel providers={providers} settings={adminSettings} onChanged={refreshSettings} />
        ) : (
          <UsagePanel />
        )}
      </section>
    </main>
  );
}

function OverviewPanel({ providers, settings, onNavigate }: { providers: Provider[]; settings: AdminSettings; onNavigate: (tab: AdminTab) => void }) {
  const activeProviders = providers.filter((provider) => provider.is_active);
  const visibleProviders = providers.filter((provider) => provider.is_visible);
  const defaultProvider = providers.find((provider) => provider.is_default);
  const titleProvider = providers.find((provider) => String(provider.id) === settings.title_provider_id);
  const memoryProvider = providers.find((provider) => String(provider.id) === settings.memory_provider_id);
  const embeddingProvider = providers.find((provider) => String(provider.id) === settings.embedding_provider_id);
  const toolMode = settings.search_tool_mode === 'searching' ? 'Searching LLM' : 'UniFuncs';

  return (
    <div className="admin-panel">
      <section className="admin-section-card admin-overview-hero">
        <div className="provider-form-head">
          <div>
            <h2>系统概览</h2>
            <p>快速确认模型、工具、记忆和运行模块是否已经处在可用状态。</p>
          </div>
        </div>
        <div className="admin-status-grid">
          <button className="admin-status-card" type="button" onClick={() => onNavigate('models')}>
            <span>模型</span>
            <strong>{activeProviders.length ? `${activeProviders.length} 个启用` : '未启用'}</strong>
            <small>{visibleProviders.length ? `${visibleProviders.length} 个前台可见` : '暂无前台可见模型'}</small>
          </button>
          <button className="admin-status-card" type="button" onClick={() => onNavigate('tools')}>
            <span>工具</span>
            <strong>{toolMode}</strong>
            <small>{settings.search_tool_mode === 'searching' ? '当前使用 searching 工具' : '当前使用 web_search / web_reader'}</small>
          </button>
          <button className="admin-status-card" type="button" onClick={() => onNavigate('memory')}>
            <span>记忆</span>
            <strong>{memoryProvider && embeddingProvider ? '可用' : '未完整配置'}</strong>
            <small>{memoryProvider ? `LLM ${memoryProvider.name}` : '未配置记忆 LLM'}</small>
          </button>
          <button className="admin-status-card" type="button" onClick={() => onNavigate('runtime')}>
            <span>运行</span>
            <strong>待接入</strong>
            <small>后续放统计、日志、成本和错误率</small>
          </button>
        </div>
      </section>
      <section className="admin-section-card">
        <div className="provider-form-head">
          <div>
            <h2>关键配置</h2>
            <p>这里展示当前会直接影响前台对话体验的配置。</p>
          </div>
        </div>
        <div className="admin-summary-list">
          <div>
            <span>默认模型</span>
            <strong>{defaultProvider ? `${defaultProvider.name} · ${defaultProvider.model}` : '未设置'}</strong>
          </div>
          <div>
            <span>对话标题 LLM</span>
            <strong>{titleProvider ? `${titleProvider.name} · ${titleProvider.model}` : '复用记忆 LLM'}</strong>
          </div>
          <div>
            <span>Embedding Provider</span>
            <strong>{embeddingProvider ? `${embeddingProvider.name} · ${embeddingProvider.model}` : '未配置'}</strong>
          </div>
          <div>
            <span>搜索卡片条数</span>
            <strong>{settings.web_search_card_result_count || '4'} 条</strong>
          </div>
        </div>
      </section>
    </div>
  );
}

function UsagePanel() {
  return (
    <div className="admin-panel">
      <section className="admin-section-card">
        <div className="provider-form-head">
          <div>
            <h2>运行情况</h2>
            <p>这里以后可以放调用量、成本、错误率、请求日志等统计。</p>
          </div>
        </div>
        <div className="admin-placeholder-grid">
          <div>
            <strong>调用量</strong>
            <span>待接入</span>
          </div>
          <div>
            <strong>成本</strong>
            <span>待接入</span>
          </div>
          <div>
            <strong>错误率</strong>
            <span>待接入</span>
          </div>
          <div>
            <strong>请求日志</strong>
            <span>待接入</span>
          </div>
        </div>
      </section>
    </div>
  );
}

function UniFuncsPanel({ settings, onChanged }: { settings: AdminSettings; onChanged: () => Promise<void> | void }) {
  const [form, setForm] = useState<AdminSettings>(settings);
  const [error, setError] = useState('');

  useEffect(() => {
    setForm(settings);
  }, [settings]);

  async function save(event: React.FormEvent) {
    event.preventDefault();
    setError('');
    try {
      await api.updateAdminSettings(form);
      await onChanged();
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败');
    }
  }

  async function saveForm(nextForm: AdminSettings) {
    setForm(nextForm);
    setError('');
    try {
      await api.updateAdminSettings(nextForm);
      await onChanged();
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败');
    }
  }

  async function copySearchingToImageTool() {
    const nextForm = {
      ...form,
      image_tool_base_url: form.searching_base_url,
      image_tool_api_key: form.searching_api_key
    };
    await saveForm(nextForm);
  }

  const mode = form.search_tool_mode === 'searching' ? 'searching' : 'unifuncs';
  const imageMode = form.image_tool_mode === 'responses' || form.image_tool_mode === 'chat_completions' ? form.image_tool_mode : 'image_api';

  return (
    <div className="admin-panel">
      <section className="admin-section-card">
        <div className="provider-form-head">
          <div>
            <h2>工具模式</h2>
            <p>选择前台对话可以调用的搜索工具类型。</p>
          </div>
        </div>
        <div className="tool-mode-switch" role="group" aria-label="搜索工具模式">
          <button
            className={'tool-mode-switch__item ' + (mode === 'unifuncs' ? 'active' : '')}
            type="button"
            onClick={() => void saveForm({ ...form, search_tool_mode: 'unifuncs' })}
          >
            UniFuncs 联动
          </button>
          <button
            className={'tool-mode-switch__item ' + (mode === 'searching' ? 'active' : '')}
            type="button"
            onClick={() => void saveForm({ ...form, search_tool_mode: 'searching' })}
          >
            Searching LLM
          </button>
        </div>
      </section>
      <form className="settings-grid admin-section-card" onSubmit={save}>
        <div className="provider-form-head">
          <div>
            <h2>{mode === 'unifuncs' ? 'UniFuncs Web Search + Web Reader' : 'Searching LLM API'}</h2>
            <p>
              {mode === 'unifuncs'
                ? '当前只启用 web_search 和 web_reader，searching 不可使用。'
                : '当前只启用 searching，web_search 和 web_reader 不可使用。'}
            </p>
          </div>
        </div>
        {mode === 'unifuncs' ? (
          <>
            <input
              placeholder="UniFuncs API Key"
              value={form.unifuncs_api_key}
              onChange={(event) => setForm({ ...form, unifuncs_api_key: event.target.value })}
            />
            <input
              placeholder="UniFuncs Base URL，例如 https://api.unifuncs.com 或 https://api.unifuncs.com/api"
              value={form.unifuncs_base_url}
              onChange={(event) => setForm({ ...form, unifuncs_base_url: event.target.value })}
            />
            <label className="settings-field">
              <span>搜索卡片显示条数</span>
              <select
                value={form.web_search_card_result_count || '4'}
                onChange={(event) => void saveForm({ ...form, web_search_card_result_count: event.target.value })}
              >
                <option value="2">2 条</option>
                <option value="4">4 条</option>
                <option value="6">6 条</option>
                <option value="10">10 条</option>
              </select>
            </label>
            <p className="field-help">切换到 Searching LLM 后，这套配置会保留，但 web_search 和 web_reader 不会被提供给模型。</p>
          </>
        ) : (
          <>
            <input
              placeholder="Searching Base URL，例如 https://api.example.com 或 https://api.example.com/v1"
              value={form.searching_base_url}
              onChange={(event) => setForm({ ...form, searching_base_url: event.target.value })}
            />
            <input
              placeholder="Searching API Key"
              value={form.searching_api_key}
              onChange={(event) => setForm({ ...form, searching_api_key: event.target.value })}
            />
            <input
              placeholder="Searching Model"
              value={form.searching_model}
              onChange={(event) => setForm({ ...form, searching_model: event.target.value })}
            />
            <input
              placeholder="ID / API ID，可选"
              value={form.searching_api_id}
              onChange={(event) => setForm({ ...form, searching_api_id: event.target.value })}
            />
            <p className="field-help">切换回 UniFuncs 后，这套配置会保留，但 searching 不会被提供给模型。</p>
          </>
        )}
        {error && <div className="error-line">{error}</div>}
        <div className="provider-form-actions">
          <button className="primary-btn" type="submit">
            保存搜索工具配置
          </button>
        </div>
      </form>
      <form className="settings-grid admin-section-card" onSubmit={save}>
        <div className="provider-form-head">
          <div>
            <h2>图片工具</h2>
            <p>配置主 LLM 可调用的 image_generate 和 image_edit。</p>
          </div>
          <button className="text-btn" type="button" onClick={() => void copySearchingToImageTool()}>
            复制 Searching 配置
          </button>
        </div>
        <div className="tool-mode-switch" role="group" aria-label="图片工具模式">
          <button
            className={'tool-mode-switch__item ' + (imageMode === 'image_api' ? 'active' : '')}
            type="button"
            onClick={() => setForm({ ...form, image_tool_mode: 'image_api' })}
          >
            Image API
          </button>
          <button
            className={'tool-mode-switch__item ' + (imageMode === 'responses' ? 'active' : '')}
            type="button"
            onClick={() => setForm({ ...form, image_tool_mode: 'responses' })}
          >
            Responses
          </button>
          <button
            className={'tool-mode-switch__item ' + (imageMode === 'chat_completions' ? 'active' : '')}
            type="button"
            onClick={() => setForm({ ...form, image_tool_mode: 'chat_completions' })}
          >
            Chat Completions
          </button>
        </div>
        <input
          placeholder="图片工具 Base URL，例如 https://api.tu-zi.com"
          value={form.image_tool_base_url}
          onChange={(event) => setForm({ ...form, image_tool_base_url: event.target.value })}
        />
        <input
          placeholder="图片工具 API Key"
          value={form.image_tool_api_key}
          onChange={(event) => setForm({ ...form, image_tool_api_key: event.target.value })}
        />
        {imageMode === 'image_api' && (
          <div className="admin-field-grid">
            <label className="field-block">
              生图模型
              <select value={form.image_generate_model} onChange={(event) => setForm({ ...form, image_generate_model: event.target.value })}>
                <option value="gpt-image-2">gpt-image-2</option>
                <option value="gpt-image-1.5">gpt-image-1.5</option>
                <option value="gpt-image-1">gpt-image-1</option>
                <option value="gpt-4o-image-vip">gpt-4o-image-vip</option>
                <option value="gpt-4o-image">gpt-4o-image</option>
              </select>
            </label>
            <label className="field-block">
              编辑模型
              <select value={form.image_edit_model} onChange={(event) => setForm({ ...form, image_edit_model: event.target.value })}>
                <option value="gpt-image-1.5">gpt-image-1.5</option>
                <option value="gpt-image-2">gpt-image-2</option>
                <option value="gpt-image-1">gpt-image-1</option>
                <option value="gpt-4o-image-vip">gpt-4o-image-vip</option>
                <option value="gpt-4o-image">gpt-4o-image</option>
              </select>
            </label>
          </div>
        )}
        {imageMode === 'responses' && (
          <label className="field-block">
            Responses 模型
            <input value={form.image_responses_model} onChange={(event) => setForm({ ...form, image_responses_model: event.target.value })} />
          </label>
        )}
        {imageMode === 'chat_completions' && (
          <label className="field-block">
            Chat Completions 模型
            <input value={form.image_chat_model} onChange={(event) => setForm({ ...form, image_chat_model: event.target.value })} />
          </label>
        )}
        <div className="admin-field-grid">
          <label className="field-block">
            生图默认尺寸
            <input value={form.image_default_size} onChange={(event) => setForm({ ...form, image_default_size: event.target.value })} />
          </label>
          <label className="field-block">
            编辑默认尺寸
            <select value={form.image_edit_size} onChange={(event) => setForm({ ...form, image_edit_size: event.target.value })}>
              <option value="1:1">1:1</option>
              <option value="2:3">2:3</option>
              <option value="3:2">3:2</option>
            </select>
          </label>
        </div>
        <div className="admin-field-grid">
          <label className="field-block">
            默认质量
            <select value={form.image_default_quality} onChange={(event) => setForm({ ...form, image_default_quality: event.target.value })}>
              <option value="auto">auto</option>
              <option value="low">low</option>
              <option value="medium">medium</option>
              <option value="high">high</option>
            </select>
          </label>
          <label className="field-block">
            返回格式
            <select value={form.image_response_format} onChange={(event) => setForm({ ...form, image_response_format: event.target.value })}>
              <option value="url">url</option>
              <option value="b64_json">b64_json</option>
            </select>
          </label>
        </div>
        <p className="field-help">复制 Searching 配置只是把已有 URL/Key 抄到图片工具配置里用于测试；image_generate 和 image_edit 仍然调用独立图片接口。</p>
        {error && <div className="error-line">{error}</div>}
        <div className="provider-form-actions">
          <button className="primary-btn" type="submit">
            保存图片工具配置
          </button>
        </div>
      </form>
      <section className="admin-section-card">
        <div className="provider-form-head">
          <div>
            <h2>后续工具能力</h2>
            <p>后续新增的外部 API 工具、插件工具和能力开关都放在这里统一管理。</p>
          </div>
        </div>
        <div className="provider-badges">
          <span>web_search</span>
          <span>web_reader</span>
          <span>searching</span>
          <span>image_generate</span>
          <span>image_edit</span>
        </div>
      </section>
    </div>
  );
}

function MemorySettingsPanel({
  providers,
  settings,
  onChanged
}: {
  providers: Provider[];
  settings: AdminSettings;
  onChanged: () => Promise<void> | void;
}) {
  const [form, setForm] = useState<AdminSettings>(settings);
  const [error, setError] = useState('');

  useEffect(() => {
    setForm(settings);
  }, [settings]);

  async function save(event: React.FormEvent) {
    event.preventDefault();
    setError('');
    try {
      await api.updateAdminSettings(form);
      await onChanged();
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败');
    }
  }

  return (
    <div className="admin-panel">
      <form className="settings-grid admin-section-card" onSubmit={save}>
        <div className="provider-form-head">
          <div>
            <h2>自动长期记忆</h2>
            <p>回复完成后由记忆模型自动判断新增、更新或忽略，并用 Embedding 模型检索相关记忆注入上下文。</p>
          </div>
        </div>
        <label className="field-block">
          记忆 LLM
          <select value={form.memory_provider_id} onChange={(event) => setForm({ ...form, memory_provider_id: event.target.value })}>
            <option value="0">未配置</option>
            {providers.map((provider) => (
              <option value={String(provider.id)} key={provider.id}>
                {provider.name} · {provider.model}
              </option>
            ))}
          </select>
        </label>
        <label className="field-block">
          Embedding Provider
          <select value={form.embedding_provider_id} onChange={(event) => setForm({ ...form, embedding_provider_id: event.target.value })}>
            <option value="0">未配置</option>
            {providers.map((provider) => (
              <option value={String(provider.id)} key={provider.id}>
                {provider.name} · {provider.model}
              </option>
            ))}
          </select>
        </label>
        <div className="admin-form-divider" />
        <div className="provider-form-head">
          <div>
            <h2>写入与检索限制</h2>
            <p>控制记忆模型每次看多少上下文，以及单轮最多产生多少记忆动作。</p>
          </div>
        </div>
        <label className="field-block">
          记忆模型查看的最近消息数
          <input value={form.memory_recent_message_limit} onChange={(event) => setForm({ ...form, memory_recent_message_limit: event.target.value })} />
        </label>
        <label className="field-block">
          单轮最多记忆动作
          <input value={form.memory_max_actions_per_run} onChange={(event) => setForm({ ...form, memory_max_actions_per_run: event.target.value })} />
        </label>
        <label className="field-block">
          注入记忆上限
          <input value={form.memory_inject_limit} onChange={(event) => setForm({ ...form, memory_inject_limit: event.target.value })} />
        </label>
        <label className="field-block">
          Embedding 检索 Top K
          <input value={form.embedding_top_k} onChange={(event) => setForm({ ...form, embedding_top_k: event.target.value })} />
        </label>
        <p className="field-help">用户提问时固定注入向量相似度最高的 10 条记忆；未配置记忆 LLM 时不会自动写入记忆，未配置 Embedding 时不会注入长期记忆。</p>
        {error && <div className="error-line">{error}</div>}
        <div className="provider-form-actions">
          <button className="primary-btn" type="submit">
            保存记忆配置
          </button>
        </div>
      </form>
    </div>
  );
}

function ProviderPanel({
  providers,
  settings,
  onSettingsChanged,
  onChanged
}: {
  providers: Provider[];
  settings: AdminSettings;
  onSettingsChanged: () => Promise<void> | void;
  onChanged: () => Promise<void> | void;
}) {
  const [form, setForm] = useState<ProviderFormState>(emptyProviderForm);
  const [settingsForm, setSettingsForm] = useState<AdminSettings>(settings);
  const [editingProviderId, setEditingProviderId] = useState<number | null>(null);
  const [error, setError] = useState('');

  const editingProvider = providers.find((provider) => provider.id === editingProviderId);
  const defaultProvider = providers.find((provider) => provider.is_default);
  const visibleProviderCount = providers.filter((provider) => provider.is_visible).length;
  const activeProviderCount = providers.filter((provider) => provider.is_active).length;

  useEffect(() => {
    setSettingsForm(settings);
  }, [settings]);

  function resetForm() {
    setForm(emptyProviderForm);
    setEditingProviderId(null);
    setError('');
  }

  function editProvider(provider: Provider) {
    setEditingProviderId(provider.id);
    setError('');
    setForm({
      name: provider.name,
      base_url: provider.base_url,
      api_key: '',
      model: provider.model,
      request_mode: provider.request_mode || 'chat_completions',
      response_format: provider.response_format || '',
      is_default: provider.is_default,
      is_visible: provider.is_visible,
      is_active: provider.is_active
    });
  }

  async function save(event: React.FormEvent) {
    event.preventDefault();
    setError('');
    try {
      const payload = {
        ...form,
        provider_type: 'openai_compatible',
        capabilities: DEFAULT_PROVIDER_CAPABILITIES
      };
      if (editingProviderId) {
        await api.updateProvider(editingProviderId, payload);
      } else {
        await api.createProvider(payload);
      }
      resetForm();
      await onChanged();
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败');
    }
  }

  async function deleteProvider(provider: Provider) {
    if (!window.confirm(`确定删除「${provider.name}」吗？`)) return;
    setError('');
    try {
      await api.deleteProvider(provider.id);
      if (editingProviderId === provider.id) {
        resetForm();
      }
      await onChanged();
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除失败');
    }
  }

  async function saveTitleProvider(nextProviderID: string) {
    const nextForm = { ...settingsForm, title_provider_id: nextProviderID };
    setSettingsForm(nextForm);
    setError('');
    try {
      await api.updateAdminSettings(nextForm);
      await onSettingsChanged();
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败');
    }
  }

  return (
    <div className="admin-panel">
      <section className="admin-section-card">
        <div className="provider-form-head">
          <div>
            <h2>模型状态</h2>
            <p>这里汇总前台模型选择会直接使用到的 Provider 状态。</p>
          </div>
        </div>
        <div className="admin-summary-list admin-summary-list--compact">
          <div>
            <span>默认模型</span>
            <strong>{defaultProvider ? `${defaultProvider.name} · ${defaultProvider.model}` : '未设置'}</strong>
          </div>
          <div>
            <span>前台可见</span>
            <strong>{visibleProviderCount} 个</strong>
          </div>
          <div>
            <span>启用模型</span>
            <strong>{activeProviderCount} 个</strong>
          </div>
        </div>
      </section>
      <section className="admin-section-card">
        <div className="provider-form-head">
          <div>
            <h2>对话标题 LLM</h2>
            <p>新对话发出第一条消息后，用这个模型根据用户第一条消息生成侧边栏标题；未选择时会复用记忆 LLM。</p>
          </div>
        </div>
        <label className="field-block">
          标题生成模型
          <select value={settingsForm.title_provider_id} onChange={(event) => void saveTitleProvider(event.target.value)}>
            <option value="0">未单独配置，复用记忆 LLM</option>
            {providers.map((provider) => (
              <option value={String(provider.id)} key={provider.id}>
                {provider.name} · {provider.model}
              </option>
            ))}
          </select>
        </label>
      </section>
      <form className="settings-grid admin-section-card" onSubmit={save}>
        <div className="provider-form-head">
          <div>
            <h2>{editingProvider ? '编辑 LLM 配置' : '新增 LLM 配置'}</h2>
            <p>{editingProvider ? `正在编辑 ${editingProvider.name}` : '配置 OpenAI-compatible 模型提供方。'}</p>
          </div>
          {editingProvider && (
            <button className="text-btn" type="button" onClick={resetForm}>
              取消编辑
            </button>
          )}
        </div>
        <input placeholder="名称" value={form.name} onChange={(event) => setForm({ ...form, name: event.target.value })} />
        <input
          placeholder="Base URL，例如 https://api.openai.com/v1"
          value={form.base_url}
          onChange={(event) => setForm({ ...form, base_url: event.target.value })}
        />
        <input
          placeholder={editingProvider ? 'API Key，留空则不修改' : 'API Key'}
          value={form.api_key}
          onChange={(event) => setForm({ ...form, api_key: event.target.value })}
        />
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
        <div className="provider-flags">
          <label>
            <input type="checkbox" checked={form.is_default} onChange={(event) => setForm({ ...form, is_default: event.target.checked })} />
            默认
          </label>
          <label>
            <input type="checkbox" checked={form.is_visible} onChange={(event) => setForm({ ...form, is_visible: event.target.checked })} />
            前台可见
          </label>
          <label>
            <input type="checkbox" checked={form.is_active} onChange={(event) => setForm({ ...form, is_active: event.target.checked })} />
            启用
          </label>
        </div>
        {error && <div className="error-line">{error}</div>}
        <div className="provider-form-actions">
          <button className="primary-btn" type="submit">
            {editingProvider ? '保存修改' : '保存 Provider'}
          </button>
          {editingProvider && (
            <button className="text-btn" type="button" onClick={resetForm}>
              取消
            </button>
          )}
        </div>
      </form>
      <section className="admin-section-card">
        <div className="provider-form-head">
          <div>
            <h2>Provider 列表</h2>
            <p>给前台模型选择、标题生成、记忆和工具使用的模型提供方都在这里统一维护。</p>
          </div>
        </div>
        <div className="provider-list">
        {providers.map((provider) => (
          <div className="provider-card" key={provider.id}>
            <div className="provider-card__head">
              <div>
                <strong>{provider.name}</strong>
                <span>{provider.model}</span>
              </div>
              <div className="provider-card__actions">
                <button className="icon-text-btn" type="button" onClick={() => editProvider(provider)}>
                  <Edit3 size={15} />
                  编辑
                </button>
                <button className="icon-text-btn danger" type="button" onClick={() => void deleteProvider(provider)}>
                  <X size={15} />
                  删除
                </button>
              </div>
            </div>
            <small>{provider.base_url}</small>
            <div className="provider-badges">
              <span>{provider.request_mode === 'responses' ? 'responses' : 'chat/completions'}</span>
              {provider.is_default && <span>默认</span>}
              {provider.is_visible ? <span>可见</span> : <span>隐藏</span>}
              {provider.is_active ? <span>启用</span> : <span>停用</span>}
            </div>
            {provider.response_format && <small>{provider.response_format}</small>}
          </div>
        ))}
        {!providers.length && <div className="empty-hint">暂无 Provider</div>}
        </div>
      </section>
    </div>
  );
}

function Root() {
  if (isWeChatBrowser()) return <WeChatBrowserBlocker />;
  return window.location.pathname.startsWith('/admin') ? <AdminApp /> : <App />;
}

createRoot(document.getElementById('root')!).render(<Root />);
