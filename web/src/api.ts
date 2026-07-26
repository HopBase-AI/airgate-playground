const BASE = '/api/v1/ext-user/airgate-playground';

function getStoredToken() {
  if (typeof window === 'undefined') return '';
  try {
    return window.localStorage.getItem('token') || '';
  } catch {
    return '';
  }
}

function clearStoredTokenAndRedirect() {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.removeItem('token');
  } catch {
    // Storage can be unavailable in private mode or locked-down browsers.
  }
  window.location.href = '/login';
}

function authHeaders(): Record<string, string> {
  const headers: Record<string, string> = {};
  const token = getStoredToken();
  if (token) headers['Authorization'] = `Bearer ${token}`;
  return headers;
}

async function request<T>(method: string, path: string, body?: unknown, base = BASE): Promise<T> {
  const headers: Record<string, string> = { ...authHeaders() };
  if (body !== undefined) headers['Content-Type'] = 'application/json';

  const resp = await fetch(base + path, {
    method,
    headers,
    body: body ? JSON.stringify(body) : undefined,
  });

  if (!resp.ok) {
    const text = await resp.text();
    let msg = `HTTP ${resp.status}`;
    try {
      const j = JSON.parse(text);
      msg = j.error || j.message || msg;
    } catch { /* ignore */ }
    if (resp.status === 401) {
      clearStoredTokenAndRedirect();
    }
    throw new Error(msg);
  }

  const text = await resp.text();
  return text ? JSON.parse(text) as T : null as unknown as T;
}

// ── Types ──

export type ReasoningEffort = 'minimal' | 'low' | 'medium' | 'high' | 'xhigh';

export interface Conversation {
  id: number;
  user_id: number;
  title: string;
  group_id: number;
  platform: string;
  model: string;
  created_at: string;
  updated_at: string;
}

// 持久化的工具调用记录（历史会话重建工具卡片用）。
export interface PersistedToolCall {
  id: string;
  name: string;
  status: 'complete' | 'error';
  args?: unknown;
  result?: Record<string, unknown>;
  error?: string;
}

export interface Message {
  id: number;
  conversation_id: number;
  role: string;
  content: string;
  reasoning?: string;
  reasoning_effort?: ReasoningEffort;
  platform: string;
  model: string;
  group_id: number;
  input_tokens: number;
  output_tokens: number;
  cost: number;
  render_fee: number;
  finish_reason?: string;
  tool_calls?: PersistedToolCall[];
  created_at: string;
}

export interface UserInfo {
  id: number;
  username: string;
  email: string;
  role: string;
  balance: number;
  status: string;
  api_key_id?: number;
  api_key_name?: string;
  api_key_platform?: string;
}

export interface PersistedMessageRequest {
  conversation_id: number;
  role: string;
  content: string;
  reasoning?: string;
  reasoning_effort?: ReasoningEffort;
  platform?: string;
  model?: string;
  group_id?: number;
  input_tokens?: number;
  output_tokens?: number;
  cost?: number;
  render_fee?: number;
  finish_reason?: string;
  tool_calls?: PersistedToolCall[];
}

export type ChatMessageContent = string | Array<
  | { type: 'text'; text: string }
  | { type: 'image_url'; image_url: { url: string } }
>;

// 工具循环 SSE 扩展事件（object=airgate.tool_event）。started 带完整
// arguments，finished 带 status/result（web_search→sources，generate_document→file）。
export interface ToolEventCall {
  id: string;
  name: string;
  arguments?: unknown;
  status?: 'ok' | 'error';
  duration_ms?: number;
  result?: Record<string, unknown>;
  error?: string;
}

export interface ChatCompletionCallbacks {
  onData: (text: string) => void;
  onReasoning: (text: string) => void;
  onToolEvent?: (event: 'tool_call_started' | 'tool_call_finished', iteration: number, call: ToolEventCall) => void;
  onDone: (result: { input_tokens: number; output_tokens: number; model: string; cost: number; model_cost: number; render_fee: number; finish_reason: string; stop_reason: string }) => void | Promise<void>;
  onError: (err: string) => void;
}

// ── API ──

export interface ChatModelItem {
  id: string;
  name: string;
  platform: string;
  context_window: number;
  max_output_tokens: number;
  capabilities: string[] | null;
}

export const api = {
  listConversations: () => request<Conversation[]>('GET', '/conversations'),

  // 动态模型目录：来自各网关插件注册表（claude＝Max 号池网关声明的模型）
  listChatModels: () => request<{ models: ChatModelItem[] }>('GET', '/models'),

  createConversation: (data: { title?: string; group_id?: number; platform?: string; model?: string }) =>
    request<Conversation>('POST', '/conversations', data),

  deleteConversation: (id: number) =>
    request<{ status: string }>('DELETE', `/conversations/${id}`),

  listMessages: (convId: number) => request<Message[]>('GET', `/messages/${convId}`),

  persistMessage: (data: PersistedMessageRequest) => request<Message>('POST', '/messages', data),

  exportMessage: (messageID: number, format: 'pdf' | 'docx' | 'pptx' | 'xlsx') =>
    request<{ file: { name: string; content_type: string; size: number; src: string }; model_tokens: number; render_fee: number }>('POST', '/exports', {
      message_id: messageID,
      format,
    }),

  getUserInfo: () => request<UserInfo>('GET', '/user/info'),
};

// SSE chunk 的最小类型契约:字段全可选,消费侧显式兜底。
// JSON.parse 出来的 any 一律先收进这个类型再用,别让 any 直接流进回调。
interface StreamChunk {
  error?: string | { message?: string };
  object?: string;
  event?: 'tool_call_started' | 'tool_call_finished';
  iteration?: number;
  call?: ToolEventCall;
  choices?: Array<{ delta?: { content?: string; reasoning_content?: string }; finish_reason?: string }>;
  airgate?: { stop_reason?: string };
  usage?: unknown;
  model?: string;
}

export async function chatCompletionsStream(
  platform: string,
  body: {
    model: string;
    messages: Array<{ role: string; content: ChatMessageContent }>;
    stream: true;
    reasoning_effort?: ReasoningEffort;
    stream_options?: { include_usage?: boolean };
    // 工具循环的产物归属（文档生成落资产需要）；后端转发前剥除，不上行。
    conversation_id?: number;
  },
  callbacks: ChatCompletionCallbacks,
  signal?: AbortSignal,
): Promise<void> {
  const requestBody = {
    ...body,
    stream_options: {
      include_usage: true,
      ...body.stream_options,
    },
  };

  const resp = await fetch(`${BASE}/chat/completions`, {
    method: 'POST',
    headers: {
      ...authHeaders(),
      'Content-Type': 'application/json',
      'Accept': 'text/event-stream',
      'X-Airgate-Platform': platform,
    },
    body: JSON.stringify(requestBody),
    signal,
  });

  if (!resp.ok || !resp.body) {
    const text = await resp.text();
    let msg = `HTTP ${resp.status}`;
    try {
      const parsed = JSON.parse(text);
      msg = parsed.error?.message || parsed.error || parsed.message || msg;
    } catch { /* ignore */ }
    if (resp.status === 401) {
      clearStoredTokenAndRedirect();
    }
    callbacks.onError(msg);
    return;
  }

  const reader = resp.body.getReader();
  const decoder = new TextDecoder();
  let buffer = '';
  let usage = { input_tokens: 0, output_tokens: 0, model: body.model, cost: 0, model_cost: 0, render_fee: 0 };
  let finishReason = '';
  let stopReason = '';

  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true });

      const parts = buffer.split('\n');
      buffer = parts.pop() || '';

      for (const line of parts) {
        const trimmed = line.trim();
        if (!trimmed.startsWith('data: ')) continue;
        const payload = trimmed.slice(6);
        if (payload === '[DONE]') {
          continue;
        }
        try {
          const parsed = JSON.parse(payload) as StreamChunk;
          if (parsed.error) {
            const errText = typeof parsed.error === 'string' ? parsed.error : parsed.error.message;
            callbacks.onError(errText || 'stream failed');
            return;
          }
          // 工具循环事件：与 OpenAI chunk 用 object 字段区分，参数不做流式增量。
          if (parsed.object === 'airgate.tool_event' && parsed.event) {
            callbacks.onToolEvent?.(parsed.event, parsed.iteration ?? 0, parsed.call ?? { id: '', name: '' });
            continue;
          }
          const choiceDelta = parsed.choices?.[0]?.delta;
          const nextFinishReason = parsed.choices?.[0]?.finish_reason;
          if (typeof nextFinishReason === 'string' && nextFinishReason) finishReason = nextFinishReason;
          const nextStopReason = parsed.airgate?.stop_reason;
          if (typeof nextStopReason === 'string' && nextStopReason) stopReason = nextStopReason;
          const reasoningDelta = choiceDelta?.reasoning_content;
          if (reasoningDelta) callbacks.onReasoning(reasoningDelta);
          const delta = choiceDelta?.content;
          if (delta) callbacks.onData(delta);
          if (parsed.usage) usage = normalizeStreamUsage(parsed.usage, parsed.model || usage.model);
        } catch {
          // non-JSON SSE line, skip
        }
      }
    }
    await callbacks.onDone({ ...usage, finish_reason: finishReason, stop_reason: stopReason });
  } catch (err) {
    if (signal?.aborted) return;
    callbacks.onError(err instanceof Error ? err.message : 'stream failed');
  }
}

function normalizeStreamUsage(raw: any, fallbackModel: string) {
  // 先把 any 数组收进最小类型再遍历,避免 any 直接流进算术
  const metrics: Array<{ key?: string; value?: unknown }> = Array.isArray(raw?.metrics) ? raw.metrics : [];
  const metricValue = (key: string): number => {
    const metric = metrics.find((item) => item?.key === key);
    const value = Number(metric?.value);
    return Number.isFinite(value) ? value : 0;
  };

  const promptTokens = Number(raw?.prompt_tokens ?? raw?.input_tokens);
  const completionTokens = Number(raw?.completion_tokens ?? raw?.output_tokens);
  const directCost = Number(raw?.cost ?? raw?.user_cost ?? raw?.account_cost);
	const totalCost = Number.isFinite(directCost) ? Math.max(0, directCost) : 0;
	const costDetails: Array<{ key?: string; user_cost?: unknown }> = Array.isArray(raw?.cost_details)
		? raw.cost_details
		: [];
	const renderFeeRaw = costDetails
		.filter((item) => item?.key === 'document_render')
		.reduce((sum: number, item) => {
			const value = Number(item?.user_cost);
			return sum + (Number.isFinite(value) ? Math.max(0, value) : 0);
		}, 0);
	const renderFee = Math.min(totalCost, renderFeeRaw);

  return {
    input_tokens: Number.isFinite(promptTokens) ? promptTokens : metricValue('input_tokens'),
    output_tokens: Number.isFinite(completionTokens) ? completionTokens : metricValue('output_tokens'),
    model: String(raw?.model || fallbackModel || ''),
		cost: totalCost,
		model_cost: Math.max(0, totalCost - renderFee),
		render_fee: renderFee,
  };
}
