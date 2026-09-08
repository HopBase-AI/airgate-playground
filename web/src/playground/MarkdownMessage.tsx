// 标准 markdown 渲染引擎：react-markdown（remark AST 管线）+ remark-gfm/remark-math，
// 替代手写正则解析器。样式经 components 覆盖映射到自有组件（皮肤与解析解耦）。
// 流式性能用 AI SDK 同款分块 memoization：marked.lexer 负责块级切分（每次都切全文，
// 见下方 lexBlocks 的说明），react-markdown 的 AST 解析与渲染则被 MemoBlock 按块挡住，
// 每个 token 到达时只有最后一个未完成块会重新解析渲染。
import { createContext, memo, useContext, useMemo, type ReactNode } from 'react';
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import remarkMath from 'remark-math';
import { marked } from 'marked';
import { styles } from './styles';
import { isSafeImageUrl, isSafeLinkUrl, isVideoAttachmentUrl } from './utils';

// 宿主环境注入：图片/代码块/数学公式的实际渲染由 MessageRendering 提供
// （它们依赖预览索引、hljs、KaTeX 等宿主能力），避免模块循环依赖。
// 这三个钩子都只返回单个元素，由 React 按位置维持身份，不需要（也不能）带 key：
// 曾用全局自增计数器当 key，导致每次渲染 key 都变，流式回复里的代码块/图片/公式
// 每个 token 都被卸载重挂载（代码块闪烁、图片重解码、KaTeX 反复重排、选中被清空）。
export interface MarkdownEnv {
  renderImage: (url: string, alt: string) => ReactNode;
  renderCodeBlock: (language: string, code: string) => ReactNode;
  renderMath: (tex: string, displayMode: boolean) => ReactNode;
}

const MarkdownEnvContext = createContext<MarkdownEnv | null>(null);

const REMARK_PLUGINS = [remarkGfm, remarkMath];

// data:image / blob: / 站内资产等自有协议不能被默认 urlTransform 剥掉
function urlTransform(url: string): string {
  return isSafeImageUrl(url) || isSafeLinkUrl(url) ? url : '';
}

// GFM 自动链接会把结尾的 CJK 标点吞进 URL，这里剥出来留在正文
const TRAILING_CJK_PUNCT_RE = /[。，；：、！？…）】」』]+$/;

function nodeText(children: ReactNode): string {
  if (typeof children === 'string') return children;
  if (Array.isArray(children)) return children.map(nodeText).join('');
  if (children && typeof children === 'object' && 'props' in children) {
    return nodeText((children as { props: { children?: ReactNode } }).props.children);
  }
  return '';
}

function useEnv(): MarkdownEnv {
  const env = useContext(MarkdownEnvContext);
  if (!env) throw new Error('MarkdownMessage 需要 MarkdownEnv');
  return env;
}

function CodeOrMath({ className, children }: { className?: string; children?: ReactNode }) {
  const env = useEnv();
  if (className?.includes('math-inline')) {
    return env.renderMath(nodeText(children), false);
  }
  return <code style={styles.markdownInlineCode}>{children}</code>;
}

function PreBlock({ children }: { children?: ReactNode }) {
  const env = useEnv();
  const codeEl = (Array.isArray(children) ? children[0] : children) as
    | { props?: { className?: string; children?: ReactNode } }
    | undefined;
  const className = codeEl?.props?.className || '';
  const raw = nodeText(codeEl?.props?.children ?? '').replace(/\n$/, '');
  if (className.includes('math-display') || /language-math\b/.test(className)) {
    return env.renderMath(raw, true);
  }
  const language = /language-([\w+#-]+)/.exec(className)?.[1] || '';
  return env.renderCodeBlock(language, raw);
}

function MarkdownImage({ src, alt }: { src?: string | Blob; alt?: string }) {
  const env = useEnv();
  const url = typeof src === 'string' ? src : '';
  if (!url) return null;
  // 附件视频与图片共用 markdown 图片语法，按 URL 分流到 <video>（不走图片灯箱）
  if (isVideoAttachmentUrl(url)) {
    return (
      <video
        src={url}
        controls
        preload="metadata"
        style={{ maxWidth: '100%', maxHeight: 360, borderRadius: 10, display: 'block' }}
      />
    );
  }
  return env.renderImage(url, alt || '');
}

function MarkdownLink({ href, children }: { href?: string; children?: ReactNode }) {
  if (!href) return <>{children}</>;
  const text = nodeText(children);
  // 自动链接（链接文本 == URL）时剥掉被 GFM 吞进 URL 的结尾 CJK 标点。
  // href 里这些标点已被百分号编码，须先解码再比对。
  let decoded = href;
  try {
    decoded = decodeURI(href);
  } catch {
    // 保留原样
  }
  if (text === decoded) {
    const trailing = TRAILING_CJK_PUNCT_RE.exec(decoded)?.[0];
    if (trailing) {
      const clean = decoded.slice(0, -trailing.length);
      return (
        <>
          <a href={clean} style={styles.markdownLink} target="_blank" rel="noreferrer">{clean}</a>
          {trailing}
        </>
      );
    }
  }
  return <a href={href} style={styles.markdownLink} target="_blank" rel="noreferrer">{children}</a>;
}

// GFM 任务列表的 checkbox → 与旧版一致的勾选框样式
function TaskCheckbox({ checked }: { checked?: boolean }) {
  return (
    <span aria-hidden="true" style={{ ...styles.markdownTaskBox, ...(checked ? styles.markdownTaskBoxChecked : null) }}>
      {checked ? '✓' : ''}
    </span>
  );
}

const COMPONENTS: Components = {
  p: ({ children }) => <p style={styles.markdownParagraph}>{children}</p>,
  h1: ({ children }) => <h1 style={styles.markdownH1}>{children}</h1>,
  h2: ({ children }) => <h2 style={styles.markdownH2}>{children}</h2>,
  h3: ({ children }) => <h3 style={styles.markdownH3}>{children}</h3>,
  h4: ({ children }) => <h4 style={styles.markdownH4}>{children}</h4>,
  h5: ({ children }) => <h4 style={styles.markdownH4}>{children}</h4>,
  h6: ({ children }) => <h4 style={styles.markdownH4}>{children}</h4>,
  ul: ({ children }) => <ul style={styles.markdownList}>{children}</ul>,
  ol: ({ children }) => <ol style={styles.markdownList}>{children}</ol>,
  li: ({ children, className }) => (
    <li style={{ ...styles.markdownListItem, ...(className?.includes('task-list-item') ? styles.markdownTaskItem : null) }}>
      {children}
    </li>
  ),
  input: ({ checked, type }) => (type === 'checkbox' ? <TaskCheckbox checked={Boolean(checked)} /> : null),
  blockquote: ({ children }) => <blockquote style={styles.markdownBlockquote}>{children}</blockquote>,
  hr: () => <hr style={styles.markdownDivider} />,
  del: ({ children }) => <del style={styles.markdownStrike}>{children}</del>,
  table: ({ children }) => (
    <div className="pg-md-table" style={styles.markdownTableWrap}>
      <table style={styles.markdownTable}>{children}</table>
    </div>
  ),
  th: ({ children, style }) => <th style={{ ...styles.markdownTableHeadCell, ...style }}>{children}</th>,
  td: ({ children, style }) => <td style={{ ...styles.markdownTableCell, ...style }}>{children}</td>,
  a: MarkdownLink,
  img: MarkdownImage,
  pre: PreBlock,
  code: CodeOrMath,
};

// 已完成的块内容不变则整块跳过重渲染（React.memo 按 content 比较）
const MemoBlock = memo(
  function MarkdownBlock({ content }: { content: string }) {
    return (
      <ReactMarkdown remarkPlugins={REMARK_PLUGINS} components={COMPONENTS} urlTransform={urlTransform}>
        {content}
      </ReactMarkdown>
    );
  },
  (prev, next) => prev.content === next.content,
);

// 表格斑马纹等无法用内联样式表达的规则，注入一次全局样式
const TABLE_STYLE_ID = 'pg-md-global-style';
function injectGlobalMarkdownStyle() {
  if (typeof document === 'undefined' || document.getElementById(TABLE_STYLE_ID)) return;
  const style = document.createElement('style');
  style.id = TABLE_STYLE_ID;
  style.textContent = `
.pg-md-table tbody tr:nth-child(even){background:rgba(128,128,128,0.045)}
.pg-md-table tbody tr:last-child td{border-bottom:0}
`;
  document.head.appendChild(style);
}

// 切分保持全量 lex：曾尝试「只重切最后一块」的增量优化，但被测试证伪——
// marked 的列表 token 会跨多个块回并（`- 父` + 空行 + `  - 子` 重新合成一个 list），
// 松散列表可以任意长，不存在安全的固定重切窗口；且 token.raw 对残缺输入会被
// 规范化（`'- '` → `'-\n'`），块长度并不等于原文跨度。
// 流式下的重复切分成本已由 PlaygroundContext 的合帧提交封顶（每帧至多一次，
// 而非每 token 一次），不需要在这里冒正确性的险。
function lexBlocks(content: string): string[] {
  try {
    return marked.lexer(content).map((token) => token.raw);
  } catch {
    return [content];
  }
}

export function MarkdownMessage({ content, env }: { content: string; env: MarkdownEnv }) {
  injectGlobalMarkdownStyle();
  // marked.lexer 只做块级切分（raw 原文透传给 react-markdown），不参与渲染
  const blocks = useMemo(() => lexBlocks(content), [content]);

  return (
    <MarkdownEnvContext.Provider value={env}>
      {blocks.map((block, index) => (
        <MemoBlock key={`block-${index}`} content={block} />
      ))}
    </MarkdownEnvContext.Provider>
  );
}
