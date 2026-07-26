// assistant-ui 外部存储 runtime 装配层：我们的 Message[] state 仍是唯一真源，
// runtime 只负责把它投影成 thread/composer 原语可消费的形态。
// API 契约、持久化时序全部留在 PlaygroundContext，本层不做任何业务逻辑。
import { useEffect, useMemo, type ReactNode } from 'react';
import {
  AssistantRuntimeProvider,
  useExternalStoreRuntime,
  type AppendMessage,
} from '@assistant-ui/react';
import { usePlayground } from '../PlaygroundContext';
import { convertAuiMessage, type AuiSourceMessage } from './convert';

export function ChatRuntimeProvider({ children }: { children: ReactNode }) {
  const {
    messages,
    isActiveConversationStreaming,
    streamParts,
    canSubmit,
    submitUserMessage,
    stopStreaming,
    composerApiRef,
  } = usePlayground();

  // 活跃会话消息 ⊕ 流式临时消息（仅当流式发生在当前会话时追加）。
  // 流式中末位始终是 assistant 消息，避免 runtime 注入自己的 optimistic 占位。
  const auiMessages = useMemo<readonly AuiSourceMessage[]>(() => (
    isActiveConversationStreaming
      ? [...messages, { auiStreamingDraft: true as const, parts: streamParts }]
      : messages
  ), [messages, isActiveConversationStreaming, streamParts]);

  const runtime = useExternalStoreRuntime<AuiSourceMessage>({
    messages: auiMessages,
    convertMessage: convertAuiMessage,
    isRunning: isActiveConversationStreaming,
    // 平台/模型未就绪、他会话流式中、附件处理中 ⇒ composer.send() 不可用
    // （输入框仍可打字，等价旧发送按钮 disabled 语义；同时防止 send 清空草稿后
    // submitUserMessage 守卫拦截造成的文本丢失）。
    isSendDisabled: !canSubmit,
    onNew: async (message: AppendMessage) => {
      const text = message.content
        .filter((part): part is { type: 'text'; text: string } => part.type === 'text')
        .map(part => part.text)
        .join('\n');
      await submitUserMessage(text);
    },
    onCancel: () => {
      stopStreaming();
      return Promise.resolve();
    },
  });

  // 把 composer 草稿文本的读写口交给 PlaygroundContext（粘贴回填/失败恢复用）
  useEffect(() => {
    composerApiRef.current = {
      getText: () => runtime.thread.composer.getState().text,
      setText: (text: string) => runtime.thread.composer.setText(text),
    };
    return () => {
      composerApiRef.current = null;
    };
  }, [runtime, composerApiRef]);

  return <AssistantRuntimeProvider runtime={runtime}>{children}</AssistantRuntimeProvider>;
}
