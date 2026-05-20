import React, { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import {
  Archive,
  ArrowUp,
  Check,
  ChevronDown,
  ChevronLeft,
  Copy,
  Edit3,
  LogOut,
  Menu,
  Plus,
  RotateCcw,
  Search,
  Settings,
  SquarePen,
  Sparkles,
  X
} from 'lucide-react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import remarkMath from 'remark-math';
import rehypeKatex from 'rehype-katex';
import 'katex/dist/katex.min.css';
import { api, streamChat } from './api';
import type { AttachmentPayload, Conversation, Memory, Message, Provider, User } from './types';
import './styles.css';

type AuthMode = 'login' | 'register';

const MAX_ATTACHMENT_BYTES = 512 * 1024;
const DEFAULT_PROVIDER_CAPABILITIES = '{"input":{"text":true},"output":{"text":true},"features":{"stream":true}}';

type ComposerAttachment = AttachmentPayload & {
  id: string;
};

type ActiveStream = {
  controller: AbortController;
  runID: string;
  localAssistantID: number;
  assistantMessageID: number;
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
};

const emptyAdminSettings: AdminSettings = {
  search_tool_mode: 'unifuncs',
  unifuncs_api_key: '',
  unifuncs_base_url: '',
  web_search_card_result_count: '4',
  searching_base_url: '',
  searching_api_key: '',
  searching_model: '',
  searching_api_id: ''
};

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
  const [webSearchCardResultCount, setWebSearchCardResultCount] = useState(4);
  const [streaming, setStreaming] = useState(false);
  const [status, setStatus] = useState('');
  const [streamController, setStreamController] = useState<AbortController | null>(null);
  const activeStreamRef = useRef<ActiveStream | null>(null);
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
    };
  }, []);

  useEffect(() => {
    resizeComposerInput();
  }, [draft]);

  useEffect(() => {
    if (streaming || !current || !messages.some((message) => message.status === 'streaming')) return;
    if (refreshPendingTimerRef.current) window.clearTimeout(refreshPendingTimerRef.current);
    refreshPendingTimerRef.current = window.setTimeout(() => {
      void refreshConversationUntilSettled(current.id);
    }, 1200);
    return () => {
      if (refreshPendingTimerRef.current) {
        window.clearTimeout(refreshPendingTimerRef.current);
        refreshPendingTimerRef.current = null;
      }
    };
  }, [current?.id, messages, streaming]);

  useLayoutEffect(() => {
    const list = messageListRef.current;
    if (!list) return;
    if (forceScrollToBottomRef.current || (shouldStickToBottomRef.current && !userInteractingWithMessagesRef.current)) {
      list.scrollTop = list.scrollHeight;
      lastMessageScrollTopRef.current = list.scrollTop;
      shouldStickToBottomRef.current = true;
      forceScrollToBottomRef.current = false;
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
    if (!current && convRes.conversations[0]) {
      await openConversation(convRes.conversations[0]);
    }
  }

  async function refreshClientSettings() {
    const res = await api.clientSettings();
    setWebSearchCardResultCount(res.settings.web_search_card_result_count || 4);
  }

  async function refreshConversationUntilSettled(conversationID: number) {
    try {
      const res = await api.conversation(conversationID);
      setMessages((items) => (current?.id === conversationID ? res.messages : items));
      const hasStreaming = res.messages.some((message) => message.status === 'streaming');
      if (!hasStreaming) {
        refreshPendingTimerRef.current = null;
        return;
      }
      refreshPendingTimerRef.current = window.setTimeout(() => {
        void refreshConversationUntilSettled(conversationID);
      }, 1800);
    } catch {
      refreshPendingTimerRef.current = window.setTimeout(() => {
        void refreshConversationUntilSettled(conversationID);
      }, 2500);
    }
  }

  async function openConversation(conversation: Conversation) {
    requestScrollToBottom();
    setCurrent(conversation);
    const res = await api.conversation(conversation.id);
    setMessages(res.messages);
    closeMobileSidebar();
  }

  async function newConversation() {
    const res = await api.createConversation();
    requestScrollToBottom();
    setCurrent(res.conversation);
    setMessages([]);
    setDraft('');
    setAttachments([]);
    setEditingMessageId(null);
    setEditingDraft('');
    closeMobileSidebar();
    setConversations((items) => [res.conversation, ...items.filter((item) => item.id !== res.conversation.id)]);
    void api.conversations().then((latest) => setConversations(latest.conversations));
  }

  async function send() {
    const content = draft.trim();
    if ((!content && !attachments.length) || streaming) return;
    void refreshClientSettings();
    await runStream({ content, attachments });
  }

  async function regenerate(message: Message) {
    if (streaming || !current || message.role !== 'assistant') return;
    void refreshClientSettings();
    await runStream({ content: '', mode: 'regenerate', messageId: message.id });
  }

  async function submitEdit(message: Message, content: string) {
    const nextContent = content.trim();
    if (streaming || !current || message.role !== 'user' || !nextContent) return;
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

  function pauseAutoFollow() {
    shouldStickToBottomRef.current = false;
    forceScrollToBottomRef.current = false;
  }

  function handleMessageListScroll() {
    const list = messageListRef.current;
    if (!list) return;
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
      setCurrent(conversation);
      setConversations((items) => [conversation!, ...items]);
    }

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
      localAssistantID: localAssistant.id,
      assistantMessageID: 0
    };
    setToolStepsByMessageId((items) => {
      const next = { ...items, [localAssistant.id]: [] };
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
            activeStreamRef.current = {
              controller,
              runID: typeof data.run_id === 'string' ? data.run_id : '',
              localAssistantID: localAssistant.id,
              assistantMessageID: typeof data.assistant_message?.id === 'number' ? data.assistant_message.id : 0
            };
          }
          if (event === 'delta') {
            setMessages((items) =>
              items.map((msg) => (msg.id === localAssistant.id ? { ...msg, content: msg.content + (data.text || '') } : msg))
            );
          }
          if (event === 'thinking') return;
          if (event === 'tool_steps') {
            const step = normalizeToolStep(data.step);
            if (step) {
              setToolStepsByMessageId((items) => ({
                ...items,
                [localAssistant.id]: mergeToolStep(items[localAssistant.id] || [], step)
              }));
            }
          }
          if (event === 'conversation_title') {
            setCurrent((item) => (item ? { ...item, title: data.title } : item));
            setConversations((items) => items.map((item) => (item.id === conversation!.id ? { ...item, title: data.title } : item)));
          }
          if (event === 'message_end') {
            const message = data.message as Message;
            activeStreamRef.current = null;
            const persistedSteps = parseToolSteps(message?.metadata);
            setToolStepsByMessageId((items) => {
              const next = { ...items };
              delete next[localAssistant.id];
              if (message?.id && persistedSteps.length) {
                next[message.id] = persistedSteps;
              }
              return next;
            });
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
      activeStreamRef.current = null;
      setStreamController(null);
      setStreaming(false);
    }
  }

  async function stopStreaming() {
    const active = activeStreamRef.current;
    const currentContent =
      messages.find((message) => message.id === active?.localAssistantID)?.content ||
      messages.find((message) => message.id === active?.assistantMessageID)?.content ||
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
    streamController?.abort();
  }

  async function addMemory(content: string) {
    if (!content.trim()) return;
    await api.createMemory(content.trim());
    const res = await api.memories();
    setMemories(res.memories);
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
                <button className="user-action-item" type="button" disabled={!current} onClick={exportCurrentConversation}>
                  <Archive size={18} />
                  <span>导出</span>
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
                  <button
                    className={'model-menu__item ' + (!providerId ? 'is-current' : '')}
                    type="button"
                    onClick={() => { setProviderId(0); setModelMenuOpen(false); }}
                  >
                    <span className="model-menu__text">
                      <span className="model-menu__label">自动选择</span>
                    </span>
                    {!providerId && <span className="model-menu__check">✓</span>}
                  </button>
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
                      {providerId === provider.id && <span className="model-menu__check">✓</span>}
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
              streaming={streaming}
              toolSteps={toolStepsByMessageId[message.id] || parseToolSteps(message.metadata)}
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
                    void send();
                  }
                }}
                placeholder="给 ChatXXX 发送消息"
                rows={1}
              />
            </div>
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
  streaming,
  toolSteps,
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
  webSearchCardResultCount
}: {
  content: string;
  streaming: boolean;
  toolSteps: ToolStep[];
  webSearchCardResultCount: number;
}) {
  const parts = useMemo(() => buildAssistantTimelineParts(content, toolSteps), [content, toolSteps]);
  const lastTextIndex = streaming ? parts.reduce((last, part, index) => (part.type === 'text' ? index : last), -1) : -1;

  if (streaming && !content.trim() && !toolSteps.length) {
    return <AssistantThinkingDot />;
  }

  return (
    <>
      {parts.map((part, index) =>
        part.type === 'text' ? (
          <MarkdownContent content={part.content} streamingCursor={index === lastTextIndex} key={`text-${index}`} />
        ) : (
          <ToolTimeline steps={part.steps} webSearchCardResultCount={webSearchCardResultCount} key={`tools-${part.offset}-${index}`} />
        )
      )}
      {streaming && content.trim() && lastTextIndex === -1 && <AssistantThinkingDot compact />}
    </>
  );
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
  const [adminSettings, setAdminSettings] = useState<AdminSettings>(emptyAdminSettings);
  const [activeTab, setActiveTab] = useState<'providers' | 'unifuncs' | 'usage'>('providers');

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
      searching_api_id: settings.searching_api_id?.value || ''
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
        <button className={'admin-nav-item ' + (activeTab === 'providers' ? 'active' : '')} type="button" onClick={() => setActiveTab('providers')}>
          <Settings size={16} />
          LLM 配置
        </button>
        <button className={'admin-nav-item ' + (activeTab === 'unifuncs' ? 'active' : '')} type="button" onClick={() => setActiveTab('unifuncs')}>
          <Search size={16} />
          搜索工具
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
        {activeTab === 'providers' ? (
          <ProviderPanel providers={providers} onChanged={refreshProviders} />
        ) : activeTab === 'unifuncs' ? (
          <UniFuncsPanel settings={adminSettings} onChanged={refreshSettings} />
        ) : (
          <UsagePanel />
        )}
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

  const mode = form.search_tool_mode === 'searching' ? 'searching' : 'unifuncs';

  return (
    <div className="admin-panel">
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
      <form className="settings-grid" onSubmit={save}>
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
    </div>
  );
}

function ProviderPanel({ providers, onChanged }: { providers: Provider[]; onChanged: () => Promise<void> | void }) {
  const [form, setForm] = useState<ProviderFormState>(emptyProviderForm);
  const [editingProviderId, setEditingProviderId] = useState<number | null>(null);
  const [error, setError] = useState('');

  const editingProvider = providers.find((provider) => provider.id === editingProviderId);

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

  return (
    <div className="admin-panel">
      <form className="settings-grid" onSubmit={save}>
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
    </div>
  );
}

function Root() {
  if (isWeChatBrowser()) return <WeChatBrowserBlocker />;
  return window.location.pathname.startsWith('/admin') ? <AdminApp /> : <App />;
}

createRoot(document.getElementById('root')!).render(<Root />);
