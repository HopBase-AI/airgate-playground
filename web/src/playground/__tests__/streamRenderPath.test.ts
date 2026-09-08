// 流式渲染路径的行为回归：context 拆分、合帧提交、停止后不回写。
//
// 这三项都是「用户能感觉到、但类型系统看不见」的性质，所以用真实 Provider +
// 受控 SSE 流来验证，而不是只测 reducer：
//   1. 每个 token 只应重渲染流式消费者，不应把整棵 usePlayground 消费者树
//      （侧边栏、Composer、通知条）一起带着抖；
//   2. 合帧不能丢字、不能乱序——最终内容必须与逐片提交完全一致；
//   3. 点「停止」之后仍在途的增量不得把已清空的流式区重新点亮。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, createElement } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import {
  PlaygroundProvider,
  usePlayground,
  useStreamParts,
  type PlaygroundContextValue,
} from '../PlaygroundContext';
import { streamPartsText, type StreamPart } from '../aui/streamState';

if (!i18n.isInitialized) {
  void i18n.use(initReactI18next).init({
    resources: { zh: { translation: {} } },
    lng: 'zh',
    interpolation: { escapeValue: false },
  });
}

// ── 受控 SSE 流 ───────────────────────────────────────────────────────────────

interface StreamController {
  push: (payload: unknown) => Promise<void>;
  close: () => Promise<void>;
  /** 上游已开始推送（fetch 已被调用）时兑现 */
  started: Promise<void>;
}

function sseChunk(payload: unknown): Uint8Array {
  return new TextEncoder().encode(`data: ${JSON.stringify(payload)}\n\n`);
}

function textDelta(text: string) {
  return { object: 'chat.completion.chunk', choices: [{ index: 0, delta: { content: text } }] };
}

function reasoningDelta(text: string) {
  return { object: 'chat.completion.chunk', choices: [{ index: 0, delta: { reasoning_content: text } }] };
}

function toolEvent(event: string, call: Record<string, unknown>) {
  return { object: 'airgate.tool_event', event, iteration: 1, call };
}

function makeStreamController(): { controller: StreamController; body: ReadableStream<Uint8Array> } {
  let enqueue!: (c: Uint8Array) => void;
  let finish!: () => void;
  let markStarted!: () => void;
  const started = new Promise<void>(resolve => { markStarted = resolve; });
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      enqueue = (chunk) => c.enqueue(chunk);
      finish = () => c.close();
      markStarted();
    },
  });
  const controller: StreamController = {
    started,
    push: async (payload) => {
      await act(async () => {
        enqueue(sseChunk(payload));
        // 让 reader 的 await read() 微任务落地
        await new Promise(resolve => setTimeout(resolve, 0));
      });
    },
    close: async () => {
      await act(async () => {
        finish();
        await new Promise(resolve => setTimeout(resolve, 0));
      });
    },
  };
  return { controller, body };
}

// ── fetch mock ────────────────────────────────────────────────────────────────

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

const CONVERSATION = {
  id: 42,
  user_id: 1,
  title: '',
  group_id: 0,
  platform: 'openai',
  model: 'gpt-5.5',
  created_at: '2026-09-08T00:00:00Z',
  updated_at: '2026-09-08T00:00:00Z',
};

function installFetchMock(streamBody: ReadableStream<Uint8Array>) {
  vi.stubGlobal('fetch', vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : input.toString();
    const method = (init?.method || 'GET').toUpperCase();
    if (url.endsWith('/chat/completions')) {
      return new Response(streamBody, {
        status: 200,
        headers: { 'Content-Type': 'text/event-stream' },
      });
    }
    if (url.endsWith('/user/info')) {
      return jsonResponse({ user_id: 1, username: 't', email: 't@e.com', role: 'user', balance: 10 });
    }
    if (url.endsWith('/models')) {
      return jsonResponse({
        models: [{
          id: 'gpt-5.5', name: 'GPT-5.5', platform: 'openai',
          context_window: 200000, max_output_tokens: 32768, capabilities: ['chat'],
        }],
      });
    }
    if (url.endsWith('/conversations')) {
      return method === 'POST' ? jsonResponse(CONVERSATION, 201) : jsonResponse([]);
    }
    if (url.endsWith('/messages')) {
      return jsonResponse({
        id: 7, conversation_id: CONVERSATION.id, role: 'assistant', content: '',
        render_fee: 0, created_at: '2026-09-08T00:00:01Z',
      }, 201);
    }
    return jsonResponse([]);
  }));
}

// ── 探针 ──────────────────────────────────────────────────────────────────────

interface Probes {
  /** usePlayground 消费者的渲染次数（代表侧边栏/Composer/通知条这类非流式 UI） */
  mainRenders: number;
  /** useStreamParts 消费者的渲染次数（代表 ChatRuntimeProvider） */
  streamRenders: number;
  /** 流式消费者每次拿到的 parts 快照 */
  streamSnapshots: StreamPart[][];
  api: PlaygroundContextValue | null;
}

function buildProbes(probes: Probes) {
  function MainConsumer() {
    const ctx = usePlayground();
    probes.mainRenders += 1;
    probes.api = ctx;
    return null;
  }
  function StreamConsumer() {
    const parts = useStreamParts();
    probes.streamRenders += 1;
    probes.streamSnapshots.push(parts.map(part => ({ ...part })));
    return null;
  }
  // 探针自身不持状态，避免成为重渲染来源
  function Tree() {
    return createElement(
      PlaygroundProvider,
      null,
      createElement(MainConsumer),
      createElement(StreamConsumer),
    );
  }
  return Tree;
}

// ── harness ───────────────────────────────────────────────────────────────────

describe('流式渲染路径', () => {
  let container: HTMLDivElement;
  let root: Root;
  let probes: Probes;

  beforeEach(() => {
    (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    window.localStorage.clear();
    container = document.createElement('div');
    document.body.appendChild(container);
    probes = { mainRenders: 0, streamRenders: 0, streamSnapshots: [], api: null };
  });

  afterEach(async () => {
    await act(async () => root?.unmount());
    container.remove();
    vi.unstubAllGlobals();
  });

  async function mountAndSend(controller: StreamController): Promise<void> {
    const Tree = buildProbes(probes);
    await act(async () => {
      root = createRoot(container);
      root.render(createElement(Tree));
    });
    // 等 /models、/user/info、/conversations 落地
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)); });

    expect(probes.api, 'Provider 未就绪').toBeTruthy();
    await act(async () => { probes.api!.createConversation(); });
    await act(async () => {
      void probes.api!.submitUserMessage('你好');
      await new Promise(resolve => setTimeout(resolve, 0));
    });
    await controller.started;
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)); });
  }

  function latestSnapshot(): StreamPart[] {
    return probes.streamSnapshots[probes.streamSnapshots.length - 1] ?? [];
  }

  it('合帧提交不丢字、不乱序：最终文本等于所有分片顺序拼接', async () => {
    const { controller, body } = makeStreamController();
    installFetchMock(body);
    await mountAndSend(controller);

    const chunks = ['你', '好', '，', '这是', '一段', '较长的', '流式', '回复', '。'];
    for (const chunk of chunks) {
      await controller.push(textDelta(chunk));
    }
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 32)); });

    expect(streamPartsText(latestSnapshot(), 'text')).toBe(chunks.join(''));
  });

  it('思考与正文交错时保持到达顺序（不会被合帧打乱或合并）', async () => {
    const { controller, body } = makeStreamController();
    installFetchMock(body);
    await mountAndSend(controller);

    await controller.push(reasoningDelta('先想一下'));
    await controller.push(reasoningDelta('再想一下'));
    await controller.push(textDelta('结论是'));
    await controller.push(reasoningDelta('补充思考'));
    await controller.push(textDelta('最终答案'));
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 32)); });

    const parts = latestSnapshot();
    expect(parts.map(p => p.kind)).toEqual(['reasoning', 'text', 'reasoning', 'text']);
    expect(streamPartsText(parts, 'reasoning')).toBe('先想一下再想一下补充思考');
    expect(streamPartsText(parts, 'text')).toBe('结论是最终答案');
  });

  it('工具事件按 id 原地更新，位置不随 running→complete 变化', async () => {
    const { controller, body } = makeStreamController();
    installFetchMock(body);
    await mountAndSend(controller);

    await controller.push(textDelta('开始查。'));
    await controller.push(toolEvent('tool_call_started', { id: 'c1', name: 'web_search', arguments: { q: 'x' } }));
    await controller.push(textDelta('查完了。'));
    await controller.push(toolEvent('tool_call_finished', {
      id: 'c1', name: 'web_search', status: 'ok', result: { sources: [{ url: 'https://e.com' }] },
    }));
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 32)); });

    const parts = latestSnapshot();
    expect(parts.map(p => p.kind)).toEqual(['text', 'tool', 'text']);
    const tool = parts[1] as Extract<StreamPart, { kind: 'tool' }>;
    expect(tool.id).toBe('c1');
    expect(tool.status).toBe('complete');
    expect(tool.result).toEqual({ sources: [{ url: 'https://e.com' }] });
  });

  it('每个 token 不再重渲染 usePlayground 消费者（侧边栏/Composer 不跟着抖）', async () => {
    const { controller, body } = makeStreamController();
    installFetchMock(body);
    await mountAndSend(controller);

    const mainBefore = probes.mainRenders;
    const streamBefore = probes.streamRenders;

    const chunkCount = 40;
    for (let i = 0; i < chunkCount; i += 1) {
      await controller.push(textDelta(`t${i}`));
    }
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 32)); });

    const mainDelta = probes.mainRenders - mainBefore;
    const streamDelta = probes.streamRenders - streamBefore;

    // 流式消费者必须收到更新（否则内容不会动）
    expect(streamDelta).toBeGreaterThan(0);
    // 非流式消费者的重渲染次数必须与 token 数解耦。
    // 拆分前每个 token 必然触发一次；这里给足余量，只要求远低于 token 数。
    // 实测：拆分前 mainDelta === chunkCount（一 token 一次）；拆分后为 0。
    // 留 2 次余量容纳流式期间的正常状态变化，仍足以挡住「按 token 重渲染」的回归。
    expect(mainDelta).toBeLessThanOrEqual(2);
    expect(streamPartsText(latestSnapshot(), 'text')).toBe(
      Array.from({ length: chunkCount }, (_, i) => `t${i}`).join(''),
    );
  });

  it('点停止后：流式区清空，且在途分片不会把它重新点亮', async () => {
    const { controller, body } = makeStreamController();
    installFetchMock(body);
    await mountAndSend(controller);

    await controller.push(textDelta('已经输出的内容'));
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 32)); });
    expect(streamPartsText(latestSnapshot(), 'text')).toBe('已经输出的内容');

    await act(async () => { probes.api!.stopStreaming(); });
    expect(latestSnapshot()).toEqual([]);

    // 停止后上游仍推来的分片必须被丢弃
    await controller.push(textDelta('停止后仍在途的残帧'));
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 32)); });

    expect(latestSnapshot()).toEqual([]);
    expect(probes.api!.isStreaming).toBe(false);
  });

  it('停止后再发一条：流式区从空开始，不残留上一轮内容', async () => {
    const first = makeStreamController();
    installFetchMock(first.body);
    await mountAndSend(first.controller);

    await first.controller.push(textDelta('第一轮内容'));
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 32)); });
    await act(async () => { probes.api!.stopStreaming(); });

    const second = makeStreamController();
    installFetchMock(second.body);
    await act(async () => {
      void probes.api!.submitUserMessage('再问一次');
      await new Promise(resolve => setTimeout(resolve, 0));
    });
    await second.controller.started;
    await second.controller.push(textDelta('第二轮'));
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 32)); });

    expect(streamPartsText(latestSnapshot(), 'text')).toBe('第二轮');
  });
});
