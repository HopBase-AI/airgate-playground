// 流式输出的 parts 累积器：替代原先 streamContent/streamReasoning 两根独立字符串。
// 按到达顺序维护有序 part 列表；连续同类文本增量合并，交错时开新 part；
// 工具调用为结构化 part（started 追加，finished 按 id 原地更新）。

export type StreamPartKind = 'reasoning' | 'text';

export type StreamTextPart = { kind: StreamPartKind; text: string };

// 工具调用 part：状态机 running → complete/error；result 为后端 tool_call_finished
// 下发的载荷（web_search→{sources}，文件生成工具→{file}）。
export type ToolCallStatus = 'running' | 'complete' | 'error';
export type StreamToolPart = {
  kind: 'tool';
  id: string;
  name: string;
  status: ToolCallStatus;
  args?: unknown;
  result?: Record<string, unknown>;
  error?: string;
};

export type StreamPart = StreamTextPart | StreamToolPart;

export function isToolPart(part: StreamPart): part is StreamToolPart {
  return part.kind === 'tool';
}

// 纯函数 reducer：不可变更新（React state 直接可用）。空增量原样返回旧数组，
// 保持引用不变以跳过无意义的重渲染。
export function appendStreamPart(
  parts: readonly StreamPart[],
  kind: StreamPartKind,
  text: string,
): readonly StreamPart[] {
  if (!text) return parts;
  const last = parts[parts.length - 1];
  if (last && last.kind === kind) {
    return [...parts.slice(0, -1), { kind, text: (last).text + text }];
  }
  return [...parts, { kind, text }];
}

// upsertToolPart：tool_call_started 追加 running part；tool_call_finished 按 id
// 原地更新状态/结果（保持位置，text/tool 交错顺序不乱）。
export function upsertToolPart(
  parts: readonly StreamPart[],
  call: { id: string; name: string; status?: ToolCallStatus; args?: unknown; result?: Record<string, unknown>; error?: string },
): readonly StreamPart[] {
  const idx = parts.findIndex(part => isToolPart(part) && part.id === call.id);
  if (idx === -1) {
    return [...parts, {
      kind: 'tool',
      id: call.id,
      name: call.name,
      status: call.status ?? 'running',
      args: call.args,
      result: call.result,
      error: call.error,
    }];
  }
  const existing = parts[idx] as StreamToolPart;
  const merged: StreamToolPart = {
    ...existing,
    status: call.status ?? existing.status,
    args: call.args ?? existing.args,
    result: call.result ?? existing.result,
    error: call.error ?? existing.error,
  };
  return [...parts.slice(0, idx), merged, ...parts.slice(idx + 1)];
}

export function streamPartsText(parts: readonly StreamPart[], kind: StreamPartKind): string {
  return parts
    .filter((part): part is StreamTextPart => part.kind === kind)
    .map(part => part.text)
    .join('');
}
