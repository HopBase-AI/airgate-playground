import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type CSSProperties,
  type ReactNode,
} from 'react';
import { useTranslation } from 'react-i18next';
import { api, chatCompletionsStream } from '../api';
import type {
  BlobUrlRegistry,
  Conversation,
  ImagePreviewState,
  Message,
  MessageContentOptions,
  ModelInfo,
  PendingFile,
  PendingImage,
  ReasoningEffort,
  RetryRequest,
  SelectOption,
  StreamAssistantOptions,
  UserInfo,
} from './types';
import {
  ACTIVE_CONVERSATION_STORAGE_KEY,
  DRAFT_CONVERSATION_ID,
  MOBILE_BREAKPOINT,
  SELECTED_MODEL_STORAGE_KEY,
  THINKING_VISIBLE_STORAGE_KEY,
} from './constants';
import {
  copyMessageContent,
  dataUrlToBlob,
  defaultModelOptionValue,
  getStoredActiveConversationId,
  getStoredSelectedModel,
  isTailOutputLimited,
  isTailRecoverable,
  messageContentWithAttachments,
  modelOptionValue,
  readLocalStorageValue,
  replaceBase64WithBlobUrls,
  replaceBlobUrlsWithBase64,
  revokeBlobRegistry,
  supportsReasoning,
  titleFromMessageContent,
  toChatMessageContent,
  writeLocalStorageValue,
} from './utils';
import { processAttachments } from './attachments/processor';
import { formatAttachmentErrors, formatAttachmentIssue } from './attachments/issues';
import { styles } from './styles';
import { CHAT_MODEL_REGISTRY } from './modelConfig';
import { appendStreamPart, upsertToolPart, type StreamPart, type ToolCallStatus } from './aui/streamState';
import { persistedToolCallsFromStream } from './aui/convert';
import type { PersistedToolCall } from '../api';

declare global {
  interface Window {
    airgate?: {
      confirm?: (message: string, options?: { title?: string; danger?: boolean }) => Promise<boolean>;
    };
  }
}

// ChatRuntimeProvider 注入的 composer 文本操作口：PlaygroundContext 在
// 粘贴回填/发送失败恢复时经它读写 assistant-ui composer 的草稿文本。
export interface ComposerTextApi {
  getText: () => string;
  setText: (text: string) => void;
}

export interface PlaygroundContextValue {
  t: (key: string, options?: Record<string, unknown>) => string;

  conversations: Conversation[];
  sidebarConversations: Conversation[];
  activeId: number | null;
  messages: Message[];
  isStreaming: boolean;
  streamParts: readonly StreamPart[];
  streamConversationId: number | null;
  isActiveConversationStreaming: boolean;
  hasRecoverableUserMessage: boolean;
  hasOutputLimitReached: boolean;

  selectedModel: string;
  setSelectedModel: React.Dispatch<React.SetStateAction<string>>;
  selectedModelInfo: ModelInfo | undefined;
  selectedModelID: string;
  selectedPlatform: string;
  selectedModelSupportsReasoning: boolean;
  modelOptions: SelectOption[];
  /** 自定义模型选择器用:选项值 + 完整模型信息(归组、上下文、平台) */
  modelChoices: Array<{ value: string; model: ModelInfo }>;
  isDraggingFiles: boolean;

  pendingImages: PendingImage[];
  pendingFiles: PendingFile[];
  isProcessingAttachments: boolean;
  // 平台/模型已就绪且当前无流式、无附件处理中——不含"内容非空"判断
  // （草稿文本在 composer runtime 里，由 Composer 组件合入判断）。
  canSubmit: boolean;

  error: string;
  retryRequest: RetryRequest | null;
  interactionNotice: string;
  previewImage: ImagePreviewState | null;
  setPreviewImage: React.Dispatch<React.SetStateAction<ImagePreviewState | null>>;
  userInfo: UserInfo | null;
  reasoningEffort: ReasoningEffort;
  setReasoningEffort: React.Dispatch<React.SetStateAction<ReasoningEffort>>;
  thinkingVisible: boolean;
  setThinkingVisible: React.Dispatch<React.SetStateAction<boolean>>;

  isMobile: boolean;
  sidebarOpen: boolean;
  setSidebarOpen: React.Dispatch<React.SetStateAction<boolean>>;

  inputRef: React.RefObject<HTMLTextAreaElement | null>;
  fileInputRef: React.RefObject<HTMLInputElement | null>;
  composerApiRef: React.MutableRefObject<ComposerTextApi | null>;

  createConversation: () => void;
  openConversation: (id: number) => void;
  deleteConversation: (id: number) => Promise<void>;
  submitUserMessage: (text: string) => Promise<void>;
  stopStreaming: () => void;
  regenerateLastResponse: () => void;
  regenerateUnfinishedResponse: () => void;
  continueLastResponse: () => void;
  handleMessageCopy: (content: string) => void;
  showImagePreview: (images: Array<{ url: string; alt: string }>, index: number) => void;
  showNextPreviewImage: (direction: number) => void;
  removePendingImage: (id: string) => void;
  removePendingFile: (id: string) => void;
  triggerImagePicker: () => void;
  handleAttachmentChange: (event: React.ChangeEvent<HTMLInputElement>) => Promise<void>;
  handlePaste: (event: React.ClipboardEvent<HTMLTextAreaElement>) => void;
  renderNativeSelect: (props: {
    id: string;
    value: string;
    options: SelectOption[];
    onChange: (value: string) => void;
    ariaLabel: string;
    style?: CSSProperties;
  }) => ReactNode;
  interactiveMessageOptions: MessageContentOptions;
}

const PlaygroundContext = createContext<PlaygroundContextValue | null>(null);

export function usePlayground() {
  const ctx = useContext(PlaygroundContext);
  if (!ctx) throw new Error('usePlayground must be used within PlaygroundProvider');
  return ctx;
}

export function PlaygroundProvider({ children }: { children: ReactNode }) {
  const { t } = useTranslation();

  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [conversationsLoaded, setConversationsLoaded] = useState(false);
  const [activeId, setActiveId] = useState<number | null>(null);
  const [messages, setMessagesRaw] = useState<Message[]>([]);
  const [streamConversationId, setStreamConversationId] = useState<number | null>(null);
  const [streamParts, setStreamParts] = useState<readonly StreamPart[]>([]);
  const [isStreaming, setIsStreaming] = useState(false);
  const [pendingImages, setPendingImages] = useState<PendingImage[]>([]);
  const [pendingFiles, setPendingFiles] = useState<PendingFile[]>([]);
  const [isProcessingAttachments, setIsProcessingAttachments] = useState(false);
  const [isDraggingFiles, setIsDraggingFiles] = useState(false);
  // 模型目录：优先后端动态目录（各网关插件注册表），拉取失败回退硬编码
  const [chatModels, setChatModels] = useState<ModelInfo[]>(CHAT_MODEL_REGISTRY);
  const [modelsResolved, setModelsResolved] = useState(false);
  const [selectedModel, setSelectedModel] = useState(() => {
    // 保留持久化原值（可能是动态目录独有、初始 registry 里没有的模型），
    // 等动态目录加载后再校正；加载失败由下方 clamp effect 兜底
    const storedModel = getStoredSelectedModel();
    return storedModel || defaultModelOptionValue(CHAT_MODEL_REGISTRY);
  });
  const [reasoningEffort, setReasoningEffort] = useState<ReasoningEffort>('medium');
  const [thinkingVisible, setThinkingVisible] = useState(() => readLocalStorageValue(THINKING_VISIBLE_STORAGE_KEY) !== '0');
  const [userInfo, setUserInfo] = useState<UserInfo | null>(null);
  const [error, setError] = useState('');
  const [retryRequest, setRetryRequest] = useState<RetryRequest | null>(null);
  // 发送闸门：从 submitUserMessage 开始到本轮流式收尾期间同步置真。
  // 用它挡掉「落库→起流」异步空档里 hasRecoverableUserMessage 的误闪。
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [interactionNotice, setInteractionNotice] = useState('');
  const [previewImage, setPreviewImage] = useState<ImagePreviewState | null>(null);
  const [sidebarOpen, setSidebarOpen] = useState(true);
  const [isMobile, setIsMobile] = useState(() => (
    typeof window !== 'undefined' ? window.innerWidth <= MOBILE_BREAKPOINT : false
  ));

  const abortRef = useRef<AbortController | null>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  // 由 ChatRuntimeProvider 挂载后填充；未挂载时为 null（相关操作静默跳过）
  const composerApiRef = useRef<ComposerTextApi | null>(null);
  const activeIdRef = useRef<number | null>(null);
  const skipNextMessagesLoadRef = useRef<number | null>(null);
  const pendingRefocusRef = useRef(false);
  const blobUrlRegistryRef = useRef<BlobUrlRegistry>(new Map());

  const setMessages = useCallback<typeof setMessagesRaw>((arg) => {
    const transform = (items: Message[]) =>
      items.map(msg => {
        if (!msg.content || !msg.content.includes('data:image/')) return msg;
        return { ...msg, content: replaceBase64WithBlobUrls(msg.content, blobUrlRegistryRef.current) };
      });
    if (typeof arg === 'function') {
      setMessagesRaw(prev => transform(arg(prev)));
    } else {
      setMessagesRaw(transform(arg));
    }
  }, []);

  const selectedModelInfo = useMemo(
    () => chatModels.find(item => modelOptionValue(item) === selectedModel),
    [chatModels, selectedModel],
  );
  const selectedModelID = selectedModelInfo?.id || '';
  const selectedPlatform = selectedModelInfo?.platform || '';
  const selectedModelSupportsReasoning = supportsReasoning(selectedModelInfo);

  const modelOptions = useMemo(() => {
    return chatModels
      .map(model => ({
        value: modelOptionValue(model),
        label: `${model.name || model.id} · ${model.platform}`,
      }));
  }, [chatModels]);
  const modelChoices = useMemo(
    () => chatModels.map(model => ({ value: modelOptionValue(model), model })),
    [chatModels],
  );

  // 拉取动态模型目录；当前选中模型不在新目录里时回退默认
  useEffect(() => {
    api.listChatModels().then(result => {
      const items: ModelInfo[] = (result.models || []).map(item => ({
        id: item.id,
        name: item.name || item.id,
        platform: item.platform,
        input_price: 0,
        output_price: 0,
        context_window: item.context_window,
        max_output_tokens: item.max_output_tokens,
        capabilities: item.capabilities || [],
      }));
      // 后端按用户的分组资格裁过平台（企业成员白名单）；旧后端不返回该字段，视为全部有资格
      const eligiblePlatforms = Array.isArray(result.eligible_platforms) ? new Set(result.eligible_platforms) : null;
      const platformEligible = (platform: string) => !eligiblePlatforms || eligiblePlatforms.has(platform);
      if (!items.length && !eligiblePlatforms) return;
      // 某平台在动态目录里缺失（如该网关插件短暂不可用）时，用硬编码兜底补齐该平台，
      // 避免一次上游抖动就让整类模型（如全部 Claude）从下拉里消失；
      // 但被权限裁掉的平台不能补回，否则成员又会选到路由不到的模型
      const dynamicPlatforms = new Set(items.map(model => model.platform));
      const merged = [...items];
      for (const fallback of CHAT_MODEL_REGISTRY) {
        if (!dynamicPlatforms.has(fallback.platform) && platformEligible(fallback.platform)) merged.push(fallback);
      }
      setChatModels(merged);
      // 恢复用户持久化的选择（可能是动态目录独有、初始 registry 里没有的模型）
      const stored = getStoredSelectedModel();
      setSelectedModel(current => {
        if (stored && merged.some(model => modelOptionValue(model) === stored)) return stored;
        if (merged.some(model => modelOptionValue(model) === current)) return current;
        return defaultModelOptionValue(merged);
      });
    }).catch(() => {}).finally(() => setModelsResolved(true));
  }, []);

  // 兜底：目录确定后（成功或失败），selectedModel 若仍不在目录里则回退默认，
  // 避免 selectedModelInfo 为空导致发送按钮永久禁用。门控在 resolved 之后，
  // 防止加载完成前误清用户偏好。
  useEffect(() => {
    if (!modelsResolved) return;
    if (selectedModel && !chatModels.some(model => modelOptionValue(model) === selectedModel)) {
      const fallback = defaultModelOptionValue(chatModels);
      if (fallback) setSelectedModel(fallback);
    }
  }, [modelsResolved, chatModels, selectedModel]);

  const sidebarConversations = useMemo(
    () => conversations.filter(item => item.id !== DRAFT_CONVERSATION_ID),
    [conversations],
  );
  const activeConversation = useMemo(
    () => conversations.find(item => item.id === activeId),
    [activeId, conversations],
  );
  const isActiveConversationStreaming = isStreaming && streamConversationId === activeId;
  // 跨会话恢复：重开/刷新后末位仍是用户提问(助手没答上来)时,给一个安静的一键重试。
  // 严格门控——发送中(isSubmitting/isStreaming)不显以免误闪;实时失败时由错误条接管
  // (!error),二者互斥,任意时刻最多一条。
  const hasRecoverableUserMessage = isTailRecoverable({
    activeId,
    isStreaming,
    isSubmitting,
    hasError: Boolean(error),
    lastRole: messages[messages.length - 1]?.role,
  });
  const lastMessage = messages[messages.length - 1];
  const hasOutputLimitReached = isTailOutputLimited({
    activeId,
    isStreaming,
    isSubmitting,
    hasError: Boolean(error),
    lastRole: lastMessage?.role,
    finishReason: lastMessage?.finish_reason,
  });
  const canSubmit = Boolean(
    selectedPlatform &&
    selectedModelID &&
    !isStreaming &&
    !isProcessingAttachments,
  );

  const resolveGroupID = useCallback(() => activeConversation?.group_id || 0, [activeConversation?.group_id]);

  useEffect(() => {
    api.listConversations().then(nextConversations => {
      setConversations(nextConversations);
      setConversationsLoaded(true);
      const storedActiveId = getStoredActiveConversationId();
      if (storedActiveId && nextConversations.some(item => item.id === storedActiveId)) {
        setActiveId(storedActiveId);
      } else {
        writeLocalStorageValue(ACTIVE_CONVERSATION_STORAGE_KEY, null);
      }
    }).catch(() => setConversationsLoaded(true));

    api.getUserInfo().then(setUserInfo).catch(() => {});
  }, []);

  useEffect(() => {
    activeIdRef.current = activeId;
    if (!conversationsLoaded) return;
    if (activeId && activeId !== DRAFT_CONVERSATION_ID) {
      writeLocalStorageValue(ACTIVE_CONVERSATION_STORAGE_KEY, String(activeId));
    } else {
      writeLocalStorageValue(ACTIVE_CONVERSATION_STORAGE_KEY, null);
    }
  }, [activeId, conversationsLoaded]);

  useEffect(() => {
    // 只在选中模型确实存在于目录时写入；不在目录时（多为动态目录尚未加载）
    // 保留而非清除，否则会丢掉持久化的动态独有模型偏好
    if (selectedModel && chatModels.some(item => modelOptionValue(item) === selectedModel)) {
      writeLocalStorageValue(SELECTED_MODEL_STORAGE_KEY, selectedModel);
    }
  }, [chatModels, selectedModel]);

  useEffect(() => {
    writeLocalStorageValue(THINKING_VISIBLE_STORAGE_KEY, thinkingVisible ? null : '0');
  }, [thinkingVisible]);

  useEffect(() => {
    if (!activeId || activeId === DRAFT_CONVERSATION_ID) {
      setMessages([]);
      return;
    }
    if (skipNextMessagesLoadRef.current === activeId) {
      skipNextMessagesLoadRef.current = null;
      return;
    }
    api.listMessages(activeId).then(setMessages).catch(err => {
      setError(err instanceof Error ? err.message : 'Failed to load messages');
    });
  }, [activeId, setMessages]);

  useEffect(() => {
    const registry = blobUrlRegistryRef.current;
    return () => revokeBlobRegistry(registry);
  }, []);

  // 滚动跟随/跳底已迁移到 assistant-ui 的 ThreadPrimitive.Viewport（autoScroll）
  // 与 ThreadPrimitive.ScrollToBottom，此处不再持有滚动状态。

  useEffect(() => {
    if (!isStreaming && pendingRefocusRef.current) {
      pendingRefocusRef.current = false;
      requestAnimationFrame(() => inputRef.current?.focus());
    }
  }, [isStreaming]);

  useEffect(() => {
    if (typeof window === 'undefined') return;
    const mediaQuery = window.matchMedia(`(max-width: ${MOBILE_BREAKPOINT}px)`);
    const updateViewport = (event?: MediaQueryListEvent) => {
      setIsMobile(event ? event.matches : mediaQuery.matches);
    };
    updateViewport();
    if (mediaQuery.addEventListener) {
      mediaQuery.addEventListener('change', updateViewport);
      return () => mediaQuery.removeEventListener('change', updateViewport);
    }
    mediaQuery.addListener(updateViewport);
    return () => mediaQuery.removeListener(updateViewport);
  }, []);

  useEffect(() => {
    setSidebarOpen(!isMobile);
  }, [isMobile]);

  useEffect(() => {
    if (!interactionNotice) return;
    const timer = window.setTimeout(() => setInteractionNotice(''), 1400);
    return () => window.clearTimeout(timer);
  }, [interactionNotice]);

  const createConversation = useCallback(() => {
    const now = new Date().toISOString();
    const draft: Conversation = {
      id: DRAFT_CONVERSATION_ID,
      user_id: userInfo?.id || 0,
      title: '',
      group_id: resolveGroupID(),
      platform: selectedPlatform,
      model: selectedModelID,
      created_at: now,
      updated_at: now,
    };
    setConversations(prev => [draft, ...prev.filter(item => item.id !== DRAFT_CONVERSATION_ID)]);
    setActiveId(DRAFT_CONVERSATION_ID);
    setMessages([]);
    setPendingImages([]);
    setPendingFiles([]);
    setError('');
    setRetryRequest(null);
    if (isMobile) setSidebarOpen(false);
    requestAnimationFrame(() => inputRef.current?.focus());
  }, [isMobile, resolveGroupID, selectedModelID, selectedPlatform, setMessages, userInfo?.id]);

  const openConversation = useCallback((id: number) => {
    setActiveId(id);
    setPendingImages([]);
    setPendingFiles([]);
    setError('');
    setRetryRequest(null);
    if (isMobile) setSidebarOpen(false);
  }, [isMobile]);

  const deleteConversation = useCallback(async (id: number) => {
    const confirmed = await window.airgate?.confirm?.(t('playground.delete_conversation_confirm'), {
      title: t('playground.delete_conversation'),
      danger: true,
    });
    if (!confirmed) return;

    if (id === DRAFT_CONVERSATION_ID) {
      setConversations(prev => prev.filter(item => item.id !== id));
    } else {
      await api.deleteConversation(id);
      setConversations(prev => prev.filter(item => item.id !== id));
    }
    if (activeIdRef.current === id) {
      setActiveId(null);
      setMessages([]);
    }
  }, [setMessages, t]);

  const finishStreaming = useCallback(() => {
    setIsStreaming(false);
    setStreamParts([]);
    setStreamConversationId(null);
    abortRef.current = null;
  }, []);

  const streamAssistantResponse = useCallback(async ({
    conversationID,
    requestMessages,
    model,
    groupID,
    platform,
    supportsReasoning: requestSupportsReasoning,
    reasoningEffort: requestReasoningEffort,
    titleContent,
  }: StreamAssistantOptions) => {
    const nextRetryRequest: RetryRequest = {
      conversationID,
      requestMessages: requestMessages.map(msg => ({ ...msg })),
      model,
      groupID,
      platform,
      supportsReasoning: requestSupportsReasoning,
      reasoningEffort: requestReasoningEffort,
    };

    setError('');
    setRetryRequest(null);
    setIsStreaming(true);
    setStreamConversationId(conversationID);
    setStreamParts([]);

    const abort = new AbortController();
    abortRef.current = abort;

    let accumulated = '';
    let accumulatedReasoning = '';
    // 工具循环产物的持久化累加器（onDone 时随消息落库，供历史重建工具卡）。
    let finalStreamParts: readonly StreamPart[] = [];
    const baseRequest = {
      model,
      messages: requestMessages.map(msg => ({
        role: msg.role,
        content: toChatMessageContent(msg.role, replaceBlobUrlsWithBase64(msg.content, blobUrlRegistryRef.current)),
      })),
      stream: true as const,
      ...(requestSupportsReasoning ? { reasoning_effort: requestReasoningEffort ?? reasoningEffort } : {}),
      // 工具产物归属（文档生成落资产用）；草稿会话已换轨为真实 id。
      ...(conversationID > 0 ? { conversation_id: conversationID } : {}),
    };

    const fail = (message: string) => {
      if (activeIdRef.current === conversationID) {
        setError(message);
        setRetryRequest(nextRetryRequest);
      }
      finishStreaming();
    };

    try {
      await chatCompletionsStream(
        platform,
        baseRequest,
        {
          onData: (text) => {
            const chunk = replaceBase64WithBlobUrls(text, blobUrlRegistryRef.current);
            accumulated += chunk;
            setStreamParts(prev => appendStreamPart(prev, 'text', chunk));
          },
          onReasoning: (text) => {
            accumulatedReasoning += text;
            setStreamParts(prev => appendStreamPart(prev, 'reasoning', text));
          },
          onToolEvent: (event, _iteration, call) => {
            const status: ToolCallStatus | undefined = event === 'tool_call_finished'
              ? (call.status === 'error' ? 'error' : 'complete')
              : 'running';
            setStreamParts(prev => {
              const next = upsertToolPart(prev, {
                id: call.id,
                name: call.name,
                status,
                args: call.arguments,
                result: call.result,
                error: call.error,
              });
              finalStreamParts = next;
              return next;
            });
          },
          onDone: async (usage) => {
            // 工具循环可能出现「只出文档卡片、正文极简」的情况，正文空但有工具
            // 产物时不判失败。
            const toolCalls: PersistedToolCall[] = persistedToolCallsFromStream(finalStreamParts);
            if (!accumulated && toolCalls.length === 0) {
              fail(t('playground.no_response'));
              return;
            }
            const persisted = await api.persistMessage({
              conversation_id: conversationID,
              role: 'assistant',
              content: replaceBlobUrlsWithBase64(accumulated, blobUrlRegistryRef.current),
              reasoning: accumulatedReasoning,
              platform,
              model: usage.model || model,
              group_id: groupID,
              input_tokens: usage.input_tokens,
              output_tokens: usage.output_tokens,
              cost: usage.cost,
              render_fee: usage.render_fee,
              finish_reason: usage.finish_reason,
              ...(toolCalls.length > 0 ? { tool_calls: toolCalls } : {}),
            });
            if (activeIdRef.current === conversationID) {
              setMessages(prev => [...prev, { ...persisted, content: accumulated, tool_calls: toolCalls.length > 0 ? toolCalls : undefined }]);
            }
            if (titleContent) {
              setConversations(prev => prev.map(item =>
                item.id === conversationID && !item.title
                  ? { ...item, title: titleFromMessageContent(titleContent), updated_at: new Date().toISOString() }
                  : item,
              ));
            }
            api.getUserInfo().then(setUserInfo).catch(() => {});
            setRetryRequest(null);
            finishStreaming();
          },
          onError: fail,
        },
        abort.signal,
      );
    } catch (err) {
      if (abort.signal.aborted) return;
      fail(err instanceof Error ? err.message : 'stream failed');
    }
  }, [finishStreaming, reasoningEffort, setMessages, t]);

  // 发送用户消息。text 由调用方入参化：
  // - 文本路径：assistant-ui composer.send() → adapter.onNew 取 text parts 后调用
  //   （此时 composer 已同步清空草稿，等价旧 setInput('') 时序）；
  // - 纯附件路径：Composer 发送按钮直接以 text='' 调用。
  // 失败且用户消息未持久化时，草稿文本经 composerApiRef 回填（等价旧 setInput(draftInput)）。
  const submitUserMessage = useCallback(async (text: string) => {
    const restoreComposerText = () => {
      if (text) composerApiRef.current?.setText(text);
    };
    if (!canSubmit || !activeId) {
      // composer 已先清空草稿，守卫拦下时须还原，避免文本凭空丢失
      restoreComposerText();
      return;
    }
    if (!text.trim() && pendingImages.length === 0 && pendingFiles.length === 0) return;
    pendingRefocusRef.current = true;
    const draftPendingImages = pendingImages;
    const draftPendingFiles = pendingFiles;
    const previousMessages = messages;
    const content = messageContentWithAttachments(text, draftPendingImages, draftPendingFiles);
    const groupID = resolveGroupID();
    let conversationID = activeId;
    let conversationCreated = false;
    let userMessagePersisted = false;
    const localUserMessage: Message = {
      id: Date.now(),
      conversation_id: activeId,
      role: 'user',
      content,
      reasoning_effort: selectedModelSupportsReasoning ? reasoningEffort : undefined,
      platform: selectedPlatform,
      model: selectedModelID,
      group_id: groupID,
      input_tokens: 0,
      output_tokens: 0,
      cost: 0,
	  render_fee: 0,
      created_at: new Date().toISOString(),
    };
    const requestMessages = [...messages, localUserMessage];

    setPendingImages([]);
    setPendingFiles([]);
    if (fileInputRef.current) fileInputRef.current.value = '';
    setError('');
    setRetryRequest(null);
    setMessages(requestMessages);

    try {
      // 同步开闸：与上面的 setMessages 同一批 render 生效,恢复条在整个发送期间不显;
      // 放在 try 内保证任何失败路径都会经 finally 关闸,不会把闸门永久卡死。
      setIsSubmitting(true);
      if (conversationID === DRAFT_CONVERSATION_ID) {
        const conv = await api.createConversation({
          title: '',
          group_id: groupID,
          platform: selectedPlatform,
          model: selectedModelID,
        });
        conversationCreated = true;
        conversationID = conv.id;
        if (activeIdRef.current === DRAFT_CONVERSATION_ID) {
          skipNextMessagesLoadRef.current = conv.id;
          setActiveId(conv.id);
          setMessages(prev => prev.map(msg => ({ ...msg, conversation_id: conv.id })));
        }
        setConversations(prev => [conv, ...prev.filter(item => item.id !== DRAFT_CONVERSATION_ID)]);
      }

      setPendingImages([]);
      setPendingFiles([]);
      if (fileInputRef.current) fileInputRef.current.value = '';

      await api.persistMessage({
        conversation_id: conversationID,
        role: 'user',
        content,
        reasoning_effort: selectedModelSupportsReasoning ? reasoningEffort : undefined,
        platform: selectedPlatform,
        model: selectedModelID,
        group_id: groupID,
      });
      userMessagePersisted = true;

      await streamAssistantResponse({
        conversationID,
        requestMessages: requestMessages.map(msg => ({ ...msg, conversation_id: conversationID })),
        model: selectedModelID,
        groupID,
        platform: selectedPlatform,
        supportsReasoning: selectedModelSupportsReasoning,
        reasoningEffort,
        titleContent: content,
      });
    } catch (err) {
      if (!userMessagePersisted) {
        restoreComposerText();
        setPendingImages(draftPendingImages);
        setPendingFiles(draftPendingFiles);
        if (fileInputRef.current) fileInputRef.current.value = '';
        setMessages(previousMessages);
        if (conversationCreated && activeIdRef.current === conversationID) {
          setConversations(prev => prev.map(item =>
            item.id === conversationID ? { ...item, updated_at: new Date().toISOString() } : item,
          ));
        }
      }
      if (activeIdRef.current === conversationID || activeIdRef.current === DRAFT_CONVERSATION_ID) {
        setError(err instanceof Error ? err.message : 'stream failed');
      }
      finishStreaming();
    } finally {
      // 关闸：成功→末位已是 assistant;失败→error 已置,两种情况恢复条都不显。
      setIsSubmitting(false);
    }
  }, [
    activeId,
    canSubmit,
    finishStreaming,
    messages,
    pendingImages,
    pendingFiles,
    reasoningEffort,
    resolveGroupID,
    selectedModelID,
    selectedModelSupportsReasoning,
    selectedPlatform,
    setMessages,
    streamAssistantResponse,
  ]);

  const stopStreaming = useCallback(() => {
    abortRef.current?.abort();
    finishStreaming();
  }, [finishStreaming]);

  const continueLastResponse = useCallback(() => {
    if (!hasOutputLimitReached || isStreaming) return;
    void submitUserMessage(t('playground.continue_generation_prompt'));
  }, [hasOutputLimitReached, isStreaming, submitUserMessage, t]);

  const regenerateLastResponse = useCallback(() => {
    if (!retryRequest || isStreaming) return;
    void streamAssistantResponse(retryRequest);
  }, [isStreaming, retryRequest, streamAssistantResponse]);

  // 跨会话恢复用：retryRequest 是会话内状态,重开/刷新后已丢,故从持久化的 messages
  // 重建请求(取到最后一条 user 为止),不依赖 retryRequest。
  const regenerateUnfinishedResponse = useCallback(() => {
    if (!activeId || isStreaming) return;
    const lastUserIndex = [...messages].map(msg => msg.role).lastIndexOf('user');
    if (lastUserIndex < 0) return;
    const requestMessages = messages.slice(0, lastUserIndex + 1);
    const lastUser = requestMessages[requestMessages.length - 1];
    const model = lastUser.model || selectedModelID;
    const platform = lastUser.platform || selectedPlatform;
    if (!model || !platform) {
      setError(t('playground.select_model_first'));
      return;
    }
    void streamAssistantResponse({
      conversationID: activeId,
      requestMessages,
      model,
      groupID: lastUser.group_id || resolveGroupID(),
      platform,
      supportsReasoning: selectedModelSupportsReasoning,
      reasoningEffort,
    });
  }, [
    activeId,
    isStreaming,
    messages,
    reasoningEffort,
    resolveGroupID,
    selectedModelID,
    selectedModelSupportsReasoning,
    selectedPlatform,
    streamAssistantResponse,
    t,
  ]);

  // 统一附件入口：图片/PDF/Excel/邮件/网页/文本都经 processAttachments 处理。
  // 处理期间（大 PDF 秒级）用 ref 同步闸门挡住并发批次——两个重叠批次会拿到
  // 同一份过期额度快照，叠加后超过单条消息限额。
  const processingAttachmentsRef = useRef(false);
  const addAttachments = useCallback(async (files: File[]) => {
    if (!files.length) return;
    if (processingAttachmentsRef.current) {
      setError(t('playground.attachment.busy'));
      return;
    }
    processingAttachmentsRef.current = true;
    setIsProcessingAttachments(true);
    setError('');
    try {
      const result = await processAttachments(files, {
        imageCount: pendingImages.filter(item => item.mediaKind !== 'video').length,
        videoCount: pendingImages.filter(item => item.mediaKind === 'video').length,
        attachmentCount: pendingImages.length + pendingFiles.length,
        totalRawBytes: pendingImages.reduce((sum, item) => sum + (item.originalBytes || 0), 0)
          + pendingFiles.reduce((sum, item) => sum + item.size, 0),
        extractedChars: pendingFiles.reduce((sum, item) => sum + item.content.length, 0),
        imageBinaryBytes: pendingImages.reduce((sum, item) => sum + (item.finalBytes || 0), 0),
      });

      if (result.images.length) {
        setPendingImages(prev => [...prev, ...result.images.map(image => ({
          id: image.id,
          name: image.name,
          url: image.url,
          originalBytes: image.originalBytes,
          finalBytes: image.finalBytes,
          compressed: image.compressed,
          mediaKind: image.mediaKind,
          warningText: image.warnings.map(issue => formatAttachmentIssue(t, issue)).join('；') || undefined,
        }))]);
      }
      if (result.files.length) {
        setPendingFiles(prev => [...prev, ...result.files.map(file => ({
          id: file.id,
          name: file.name,
          content: file.content,
          size: file.size,
          type: file.type,
          truncated: file.truncated,
          warningText: file.warnings.map(issue => formatAttachmentIssue(t, issue)).join('；') || undefined,
        }))]);
      }
      // 只有真的加上了附件才清掉重试请求，避免一次失败的添加毁掉重试按钮
      if (result.images.length || result.files.length) {
        setRetryRequest(null);
      }
      if (result.errors.length) {
        setError(formatAttachmentErrors(t, result.errors));
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to read file');
    } finally {
      processingAttachmentsRef.current = false;
      setIsProcessingAttachments(false);
    }
  }, [pendingFiles, pendingImages, t]);

  const handleAttachmentChange = useCallback(async (event: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files || []);
    event.target.value = '';
    await addAttachments(files);
  }, [addAttachments]);

  const handlePaste = useCallback((event: React.ClipboardEvent<HTMLTextAreaElement>) => {
    const files = Array.from(event.clipboardData.files || []);
    if (files.length) {
      event.preventDefault();
      void addAttachments(files);
      return;
    }

    // 图文消息复制回填：我们的复制会写 text/html（图片内联 data URL），
    // 粘回输入框时把图片还原成附件、文字进输入框。
    const html = event.clipboardData.getData('text/html');
    if (!html || !html.includes('data:image/')) return;
    const doc = new DOMParser().parseFromString(html, 'text/html');
    const imageFiles: File[] = [];
    doc.querySelectorAll('img').forEach((img, index) => {
      const src = img.getAttribute('src') || '';
      if (!src.startsWith('data:image/')) return;
      const blob = dataUrlToBlob(src);
      if (!blob) return;
      const ext = (blob.type.split('/')[1] || 'png').replace(/[^a-z0-9]/gi, '') || 'png';
      imageFiles.push(new File([blob], `pasted-image-${index + 1}.${ext}`, { type: blob.type }));
    });
    if (!imageFiles.length) return;

    event.preventDefault();
    const text = event.clipboardData.getData('text/plain').replace(/\[Image\]/g, '').trim();
    if (text) {
      const current = composerApiRef.current?.getText() ?? '';
      composerApiRef.current?.setText(current ? `${current}\n${text}` : text);
    }
    void addAttachments(imageFiles);
  }, [addAttachments]);

  // 拖拽上传：window 级监听（dragenter 计数防子元素抖动），仅在有活跃会话时接收
  const addAttachmentsRef = useRef(addAttachments);
  useEffect(() => {
    addAttachmentsRef.current = addAttachments;
  }, [addAttachments]);

  useEffect(() => {
    if (typeof window === 'undefined') return;
    let depth = 0;
    const hasFiles = (event: DragEvent) => Boolean(
      event.dataTransfer && Array.from(event.dataTransfer.types).includes('Files'),
    );
    const reset = () => { depth = 0; setIsDraggingFiles(false); };
    const onDragEnter = (event: DragEvent) => {
      if (!hasFiles(event)) return;
      depth += 1;
      if (activeIdRef.current) setIsDraggingFiles(true);
    };
    const onDragOver = (event: DragEvent) => {
      // 无条件拦截浏览器默认：否则拖入的文件会导致整个 SPA 导航到该文件、丢失所有状态
      if (hasFiles(event)) event.preventDefault();
    };
    const onDragLeave = (event: DragEvent) => {
      if (!hasFiles(event)) return;
      depth = Math.max(0, depth - 1);
      if (depth === 0) setIsDraggingFiles(false);
    };
    const onDrop = (event: DragEvent) => {
      if (hasFiles(event)) event.preventDefault();
      reset();
      if (!hasFiles(event) || !activeIdRef.current) return;
      const files = Array.from(event.dataTransfer?.files || []);
      if (files.length) void addAttachmentsRef.current(files);
    };
    // 拖拽被放弃（拖出窗口松手、Esc、切窗）时没有 drop/dragend，靠 blur 兜底清遮罩
    window.addEventListener('dragenter', onDragEnter);
    window.addEventListener('dragover', onDragOver);
    window.addEventListener('dragleave', onDragLeave);
    window.addEventListener('drop', onDrop);
    window.addEventListener('dragend', reset);
    window.addEventListener('blur', reset);
    return () => {
      window.removeEventListener('dragenter', onDragEnter);
      window.removeEventListener('dragover', onDragOver);
      window.removeEventListener('dragleave', onDragLeave);
      window.removeEventListener('drop', onDrop);
      window.removeEventListener('dragend', reset);
      window.removeEventListener('blur', reset);
    };
  }, []);

  const removePendingImage = useCallback((id: string) => {
    setPendingImages(prev => prev.filter(item => item.id !== id));
  }, []);

  const removePendingFile = useCallback((id: string) => {
    setPendingFiles(prev => prev.filter(item => item.id !== id));
  }, []);

  const triggerImagePicker = useCallback(() => {
    fileInputRef.current?.click();
  }, []);

  const handleMessageCopy = useCallback((content: string) => {
    void copyMessageContent(content)
      .then(() => setInteractionNotice(t('playground.copied')))
      .catch(() => setInteractionNotice(t('playground.copy_failed')));
  }, [t]);

  const showImagePreview = useCallback((images: Array<{ url: string; alt: string }>, index: number) => {
    if (!images.length) return;
    const safeIndex = Math.max(0, Math.min(index, images.length - 1));
    setPreviewImage({ images, index: safeIndex });
  }, []);

  const showNextPreviewImage = useCallback((direction: number) => {
    setPreviewImage(current => {
      if (!current || current.images.length <= 1) return current;
      const next = (current.index + direction + current.images.length) % current.images.length;
      return { ...current, index: next };
    });
  }, []);

  const renderNativeSelect = useCallback(({
    id,
    value,
    options,
    onChange,
    ariaLabel,
    style,
  }: {
    id: string;
    value: string;
    options: SelectOption[];
    onChange: (value: string) => void;
    ariaLabel: string;
    style?: CSSProperties;
  }) => (
    <select
      id={id}
      className="pg-composer-select"
      value={value}
      onChange={event => onChange(event.target.value)}
      aria-label={ariaLabel}
      style={{
        ...styles.selectTrigger,
        minWidth: id === 'model' ? 188 : 112,
        maxWidth: id === 'model' ? 280 : 132,
        flexShrink: id === 'model' ? 1 : 0,
        ...style,
      }}
    >
      {options.map(option => (
        <option key={option.value} value={option.value}>{option.label}</option>
      ))}
    </select>
  ), []);

  const interactiveMessageOptions = useMemo<MessageContentOptions>(() => ({
    imagePreviewTitle: t('playground.preview_image'),
    generatedImageAlt: t('playground.generated_image'),
    isMobile,
  }), [isMobile, t]);

  const value: PlaygroundContextValue = {
    t: t,
    conversations,
    sidebarConversations,
    activeId,
    messages,
    isStreaming,
    streamParts,
    streamConversationId,
    isActiveConversationStreaming,
    hasRecoverableUserMessage,
    hasOutputLimitReached,
    selectedModel,
    setSelectedModel,
    selectedModelInfo,
    selectedModelID,
    selectedPlatform,
    selectedModelSupportsReasoning,
    modelOptions,
    modelChoices,
    isDraggingFiles,
    pendingImages,
    pendingFiles,
    isProcessingAttachments,
    canSubmit,
    error,
    retryRequest,
    interactionNotice,
    previewImage,
    setPreviewImage,
    userInfo,
    reasoningEffort,
    setReasoningEffort,
    thinkingVisible,
    setThinkingVisible,
    isMobile,
    sidebarOpen,
    setSidebarOpen,
    inputRef,
    fileInputRef,
    composerApiRef,
    createConversation,
    openConversation,
    deleteConversation,
    submitUserMessage,
    stopStreaming,
    regenerateLastResponse,
    regenerateUnfinishedResponse,
    continueLastResponse,
    handleMessageCopy,
    showImagePreview,
    showNextPreviewImage,
    removePendingImage,
    removePendingFile,
    triggerImagePicker,
    handleAttachmentChange,
    handlePaste,
    renderNativeSelect,
    interactiveMessageOptions,
  };

  return <PlaygroundContext.Provider value={value}>{children}</PlaygroundContext.Provider>;
}
