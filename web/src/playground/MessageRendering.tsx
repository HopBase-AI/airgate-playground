import { Suspense, lazy, useEffect, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import type { MessageContentOptions } from './types';
import { styles } from './styles';
import { copyText, formatByteSize, splitFileBlocks, type ParsedFileBlock } from './utils';
import { loadHighlightRuntime, normalizeHighlightLanguage } from './highlight';
import { MarkdownMessage, type MarkdownEnv } from './MarkdownMessage';

const MathRenderer = lazy(() => import('../MathRenderer'));

export function GeneratedImageFrame({ url, alt, options, imageIndex }: {
  url: string; alt: string; options: MessageContentOptions; imageIndex: number;
}) {
  const [dimensions, setDimensions] = useState('');
  const imageNode = (
    <img
      src={url}
      alt={alt}
      style={styles.generatedImage}
      loading="lazy"
      onLoad={e => {
        const img = e.currentTarget;
        if (img.naturalWidth && img.naturalHeight) {
          setDimensions(`${img.naturalWidth}×${img.naturalHeight}`);
        }
      }}
    />
  );
  const previewTitle = options.imagePreviewTitle || 'Preview image';
  const previewableImage = options.onImagePreview ? (
    <button
      type="button"
      style={styles.generatedImagePreviewBtn}
      title={previewTitle}
      aria-label={previewTitle}
      onClick={() => options.onImagePreview?.(url, alt, imageIndex)}
    >
      {imageNode}
    </button>
  ) : imageNode;

  return (
    <span style={{ ...styles.generatedImageFrame, ...(options.isMobile ? styles.generatedImageFrameMobile : null) }}>
      {previewableImage}
      {dimensions && <span style={styles.generatedImageDimensions}>{dimensions}</span>}
    </span>
  );
}

function renderGeneratedImage(key: string, url: string, alt: string, options: MessageContentOptions) {
  const imageIndex = options.takeImageIndex?.() ?? -1;
  return <GeneratedImageFrame key={key} url={url} alt={alt} options={options} imageIndex={imageIndex} />;
}

function renderMath(tex: string, key: string, displayMode: boolean) {
  const Tag = displayMode ? 'div' : 'span';
  const style = displayMode ? styles.markdownBlockMath : styles.markdownInlineMath;
  const fallback = <Tag style={style}>{tex}</Tag>;
  return (
    <Suspense key={key} fallback={fallback}>
      <MathRenderer displayMode={displayMode} style={style} tex={tex} />
    </Suspense>
  );
}

// 代码块：语言标签 + 一键复制 + 异步语法高亮。样式由 shell 承载（markdownCodeBlock 只管 pre 本体）。
// 高亮是渐进增强：hljs 未加载/语言未知/加载失败时保持纯文本。流式输出时内容逐 chunk 变化，
// 防抖 150ms 避免每个 token 都跑一遍 tokenizer。
function CodeBlock({ language, code }: { language: string; code: string }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  const [highlighted, setHighlighted] = useState('');

  useEffect(() => {
    const lang = normalizeHighlightLanguage(language);
    if (!lang || !code) {
      setHighlighted('');
      return;
    }
    let cancelled = false;
    const timer = window.setTimeout(() => {
      loadHighlightRuntime()
        .then((hljs) => {
          if (cancelled || !hljs.getLanguage(lang)) return;
          // hljs.highlight 会转义非 token 文本，输出可安全注入
          const { value } = hljs.highlight(code, { language: lang, ignoreIllegals: true });
          if (!cancelled) setHighlighted(value);
        })
        .catch(() => {});
    }, 150);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [language, code]);

  return (
    <div style={styles.markdownCodeShell} className="pg-md-code">
      <div style={styles.markdownCodeHeader}>
        <span style={styles.markdownCodeLang}>{language || 'code'}</span>
        <button
          type="button"
          style={{ ...styles.markdownCodeCopyBtn, ...(copied ? styles.markdownCodeCopyBtnDone : null) }}
          aria-label={t('playground.copy_code')}
          onClick={() => {
            void copyText(code)
              .then(() => {
                setCopied(true);
                window.setTimeout(() => setCopied(false), 1400);
              })
              .catch(() => {});
          }}
        >
          {copied ? (
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <path d="M20 6 9 17l-5-5" />
            </svg>
          ) : (
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
              <rect x="9" y="9" width="13" height="13" rx="2" ry="2" />
              <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" />
            </svg>
          )}
          {copied ? t('playground.copied') : t('common.copy')}
        </button>
      </div>
      {highlighted ? (
        // hljs.highlight 会转义非 token 文本，输出可安全注入
        <pre style={styles.markdownCodeBlock}><code dangerouslySetInnerHTML={{ __html: highlighted }} /></pre>
      ) : (
        <pre style={styles.markdownCodeBlock}><code>{code}</code></pre>
      )}
    </div>
  );
}

const fileBlockChipStyles = {
  details: {
    margin: '4px 0',
    border: '1px solid rgba(128, 128, 128, 0.28)',
    borderRadius: 8,
    padding: '4px 8px',
    background: 'rgba(128, 128, 128, 0.08)',
    fontSize: 13,
  },
  summary: {
    cursor: 'pointer',
    display: 'flex',
    alignItems: 'center',
    gap: 6,
    listStyle: 'none' as const,
    userSelect: 'none' as const,
  },
  name: {
    fontWeight: 600,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap' as const,
    maxWidth: 260,
  },
  meta: { opacity: 0.65, fontSize: 12, flexShrink: 0 },
  badge: {
    fontSize: 11,
    padding: '0 6px',
    borderRadius: 6,
    background: 'rgba(217, 119, 6, 0.16)',
    color: '#b45309',
    flexShrink: 0,
  },
  content: {
    marginTop: 6,
    maxHeight: 320,
    overflow: 'auto' as const,
    whiteSpace: 'pre-wrap' as const,
    wordBreak: 'break-word' as const,
    fontSize: 12,
    lineHeight: 1.5,
    opacity: 0.9,
  },
};

// 消息里的 <file> 块折叠为 chip，展开时才渲染内容（200k 字符的抽取文本不能直出气泡）。
function FileBlockChip({ block }: { block: ParsedFileBlock }) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const sizeLabel = formatByteSize(block.size);
  return (
    <details
      style={fileBlockChipStyles.details}
      onToggle={event => setOpen((event.currentTarget).open)}
    >
      <summary style={fileBlockChipStyles.summary}>
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" style={{ flexShrink: 0 }}>
          <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
          <path d="M14 2v6h6" />
        </svg>
        <span style={fileBlockChipStyles.name} title={block.name}>{block.name}</span>
        {sizeLabel && <span style={fileBlockChipStyles.meta}>{sizeLabel}</span>}
        {block.truncated && (
          <span style={fileBlockChipStyles.badge} title={t('playground.attachment.truncated')}>
            {t('playground.attachment_truncated_badge')}
          </span>
        )}
      </summary>
      {open && <div style={fileBlockChipStyles.content}>{block.content}</div>}
    </details>
  );
}

export function renderMessageContent(content: string, options: MessageContentOptions = {}) {
  if (options.parseFileBlocks && content.includes('<file name="')) {
    const segments = splitFileBlocks(content);
    if (segments.some(segment => segment.kind === 'file')) {
      // 图片预览索引须跨文本段连续（ChatView 用整条消息的 generatedImages 列表）
      let imageIndex = -1;
      const sharedOptions: MessageContentOptions = {
        ...options,
        takeImageIndex: () => {
          imageIndex += 1;
          return imageIndex;
        },
      };
      // trailingInlineAction（悬浮复制按钮）只挂在最后一个文本段，避免逐段重复。
      const lastTextIndex = segments.reduce((last, segment, index) => (segment.kind === 'text' ? index : last), -1);
      const nodes = segments.map((segment, index) => (
        segment.kind === 'file'
          ? <FileBlockChip key={`fileblock-${index}`} block={segment.block} />
          : (
            <div key={`filetext-${index}`}>
              {renderMarkdownContent(
                segment.text,
                index === lastTextIndex ? sharedOptions : { ...sharedOptions, trailingInlineAction: undefined },
              )}
            </div>
          )
      ));
      // 纯文件消息没有文本段可挂复制按钮，单独补一行
      if (lastTextIndex === -1 && options.trailingInlineAction) {
        nodes.push(<div key="fileblock-action">{options.trailingInlineAction}</div>);
      }
      return nodes;
    }
  }
  return renderMarkdownContent(content, options);
}

function renderMarkdownContent(content: string, options: MessageContentOptions = {}) {
  // 分段渲染（file 块拆分）时由上层传入共享计数器，保证图片索引跨段连续
  let localImageIndex = -1;
  const takeImageIndex = options.takeImageIndex ?? (() => {
    localImageIndex += 1;
    return localImageIndex;
  });
  const renderOptions: MessageContentOptions = { ...options, takeImageIndex };

  const env: MarkdownEnv = {
    renderImage: (key, url, alt) =>
      renderGeneratedImage(key, url, alt || options.generatedImageAlt || 'Generated image', renderOptions),
    renderCodeBlock: (key, language, code) => <CodeBlock key={key} language={language} code={code} />,
    renderMath: (key, tex, displayMode) => renderMath(tex, key, displayMode),
  };

  const nodes: ReactNode[] = [<MarkdownMessage key="md" content={content} env={env} />];
  if (options.trailingInlineAction) {
    nodes.push(
      <div key="md-action" style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 2 }}>
        {options.trailingInlineAction}
      </div>,
    );
  }
  return nodes;
}
