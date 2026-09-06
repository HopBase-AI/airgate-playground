import type { CSSProperties } from 'react';
import { cssVar } from '@doudou-start/airgate-theme';

const PLAYGROUND_COMPOSER_TEXTAREA_HEIGHT = 112;

export const keyframes = `
@keyframes pg-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.3; }
}
@keyframes pg-fadein {
  from { opacity: 0; transform: translateY(6px); }
  to { opacity: 1; transform: translateY(0); }
}
@keyframes pg-spin {
  to { transform: rotate(360deg); }
}

/* ── Quiet Modern aesthetic ──
   单一字体（系统 Chinese ladder + 现代 sans for Latin），多权重撑层级，
   完全跟随 SDK 主题色系。没有任何复古/编辑级装饰。 */
[data-pg-aesthetic] {
  font-feature-settings: 'cv11' on, 'ss01' on;
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
  text-rendering: optimizeLegibility;
}

/* 删除按钮：hover 才显出，颜色保持中性，hover 时才染 danger */
.pg-conv-delete {
  opacity: 0;
  color: var(--ag-text-tertiary, #9ca3af);
  transition: opacity 120ms ease, background 120ms ease, color 120ms ease;
}
.pg-conv-item:hover .pg-conv-delete,
.pg-conv-item:focus-within .pg-conv-delete {
  opacity: 1;
}
.pg-conv-delete:hover {
  background: var(--ag-danger-subtle, rgba(239, 68, 68, 0.12));
  color: var(--ag-danger, #ef4444);
}
.pg-conv-delete:focus-visible {
  opacity: 1;
  outline: 2px solid var(--ag-border-focus, #3b82f6);
  outline-offset: 1px;
}

.pg-conv-item {
  position: relative;
}
.pg-conv-item:hover,
.pg-sidebar-action:hover {
  background: var(--ag-bg-hover, rgba(148, 163, 184, 0.12)) !important;
  color: var(--ag-text, #111827) !important;
}
.pg-conv-item.is-active {
  background: var(--ag-bg-hover, rgba(148, 163, 184, 0.12)) !important;
}
.pg-sidebar-action:focus-visible {
  outline: 2px solid var(--ag-border-focus, #3b82f6);
  outline-offset: 2px;
}
.pg-sidebar-collapse-button svg {
  stroke-width: 2.5;
}
.pg-sidebar-collapse-button:hover {
  background: var(--ag-bg-hover, rgba(148, 163, 184, 0.12)) !important;
  color: var(--ag-text, #111827) !important;
}
.pg-input-wrapper:focus-within {
  border-color: color-mix(in oklab, var(--ag-primary, #111827) 35%, transparent) !important;
  box-shadow:
    0 8px 40px rgba(0, 0, 0, 0.18),
    0 2px 12px rgba(0, 0, 0, 0.08),
    0 0 0 1px color-mix(in oklab, var(--ag-primary, #111827) 12%, transparent) !important;
}
.pg-input-wrapper textarea::placeholder {
  color: var(--ag-field-placeholder, #9ca3af);
  opacity: 1;
}
.pg-composer-select {
  color-scheme: light;
}
[data-theme="dark"] .pg-composer-select,
.dark .pg-composer-select {
  color-scheme: dark;
}
.pg-composer-select:hover {
  border-color: var(--ag-border, rgba(148, 163, 184, 0.26)) !important;
  color: var(--ag-text, #111827) !important;
}
.pg-composer-select:focus {
  outline: none;
}
.pg-composer-select:focus-visible {
  border-color: color-mix(in oklab, var(--ag-primary, #111827) 38%, transparent) !important;
  box-shadow: inset 0 0 0 1px color-mix(in oklab, var(--ag-primary, #111827) 22%, transparent);
}
.pg-composer-select option {
  background: var(--ag-bg-surface, #ffffff);
  color: var(--ag-text, #111827);
}

/* 消息尾部悬浮复制按钮：hover/键盘聚焦才显出（移动端由 pg-msg-copy-visible 常显）。
   原实现用 hoveredCopyTarget state 控制，迁 assistant-ui 后改为纯 CSS。 */
.pg-msg-copy {
  opacity: 0;
  pointer-events: none;
}
.pg-copy-zone:hover .pg-msg-copy,
.pg-copy-zone:focus-within .pg-msg-copy,
.pg-msg-copy-visible {
  opacity: 1;
  pointer-events: auto;
}

/* 模型选择器:触发器沿用 .pg-composer-select 的外观;弹层选项 hover / 当前项浅底 */
.pg-model-trigger { padding-right: 8px !important; }
.pg-model-item:hover { background: var(--ag-bg-hover, rgba(148, 163, 184, 0.12)) !important; }
.pg-model-item[data-active="true"] { background: var(--ag-bg-hover, rgba(148, 163, 184, 0.12)) !important; }
.pg-model-item:focus-visible { outline: 2px solid var(--ag-border-focus, #3b82f6); outline-offset: -2px; }
.pg-model-search::placeholder { color: var(--ag-text-tertiary, #9ca3af); }

/* 跳到底部胶囊：贴底时 ThreadPrimitive.ScrollToBottom 自动 disabled，借此隐藏。
   胶囊与外层都带内联 display(inline-flex / flex),这里必须 !important 才压得过内联样式,
   否则贴底了胶囊仍然显示。 */
.pg-jump-bottom[disabled] {
  display: none !important;
}
.pg-jump-bottom-wrap:has(> button[disabled]) {
  display: none !important;
}
.pg-jump-bottom:hover {
  background: var(--ag-bg-hover, rgba(148, 163, 184, 0.12)) !important;
  border-color: var(--ag-border, rgba(148, 163, 184, 0.26)) !important;
}
.pg-jump-bottom:focus-visible {
  outline: 2px solid var(--ag-border-focus, #3b82f6);
  outline-offset: 1px;
}

/* 失败卡片的「重新生成」：ghost 按钮，hover 才轻抬（与主题单色体系一致） */
.pg-error-retry:hover {
  background: var(--ag-bg-active, rgba(148, 163, 184, 0.16)) !important;
  border-color: var(--ag-border, rgba(148, 163, 184, 0.26)) !important;
  color: var(--ag-text, #111827) !important;
}
.pg-error-retry:focus-visible {
  outline: 2px solid var(--ag-border-focus, #3b82f6);
  outline-offset: 1px;
}

/* 输入区动作按钮：附件/思考=ghost，发送=primary，停止=danger。
   inline style 只画静态态，hover/active 的抬压反馈交给这里，补足「质感」。 */
.pg-ghost-btn:hover:not(:disabled) {
  background: var(--ag-bg-active, rgba(148, 163, 184, 0.16)) !important;
  border-color: var(--ag-border, rgba(148, 163, 184, 0.26)) !important;
  color: var(--ag-text, #111827) !important;
}
/* 思考开关处于「激活」态时 hover 保留主色调，别被上面的中性 hover 抹平
   （同 specificity 靠源码顺序在后取胜） */
.pg-ghost-btn.is-active:hover {
  background: color-mix(in oklab, var(--ag-primary, #111827) 16%, transparent) !important;
  border-color: color-mix(in oklab, var(--ag-primary, #111827) 45%, transparent) !important;
  color: var(--ag-primary, #111827) !important;
}
.pg-ghost-btn:disabled {
  opacity: 0.45;
  cursor: not-allowed;
}
.pg-send-btn:hover:not(:disabled) {
  background: var(--ag-primary-hover, var(--ag-primary, #111827)) !important;
}
.pg-send-btn:active:not(:disabled) {
  transform: translateY(0.5px);
}
.pg-stop-btn:hover {
  background: color-mix(in oklab, var(--ag-danger, #ef4444) 22%, transparent) !important;
}
/* 消息尾部复制按钮：hover 才染出边框与文字，点前有明确可点反馈 */
.pg-copy-btn:hover {
  background: var(--ag-bg-hover, rgba(148, 163, 184, 0.12)) !important;
  border-color: var(--ag-border, rgba(148, 163, 184, 0.26)) !important;
  color: var(--ag-text, #111827) !important;
}
.pg-copy-btn:focus-visible {
  outline: 2px solid var(--ag-border-focus, #3b82f6);
  outline-offset: 1px;
}
.pg-ghost-btn:focus-visible,
.pg-send-btn:focus-visible,
.pg-stop-btn:focus-visible {
  outline: 2px solid var(--ag-border-focus, #3b82f6);
  outline-offset: 1px;
}
`;

export const styles: Record<string, CSSProperties> = {
  layout: {
    display: 'flex',
    height: '100%',
    minHeight: 0,
    minWidth: 0,
    position: 'relative',
    isolation: 'isolate',
    background: cssVar('bgDeep'),
    fontFamily: cssVar('fontSans'),
    color: cssVar('text'),
    overflow: 'hidden',
  },
  // ── Sidebar ──
  sidebar: {
    width: 284,
    minWidth: 284,
    display: 'flex',
    flexDirection: 'column',
    minHeight: 0,
    background: cssVar('bgDeep'),
    borderRight: `1px solid ${cssVar('borderSubtle')}`,
    position: 'relative',
    zIndex: 3,
    fontSynthesis: 'none',
    textRendering: 'geometricPrecision',
  },
  sidebarRail: {
    width: 48,
    minWidth: 48,
    display: 'flex',
    justifyContent: 'center',
    alignItems: 'flex-start',
    paddingTop: 4,
    background: cssVar('bgDeep'),
    borderRight: `1px solid ${cssVar('borderSubtle')}`,
    flexShrink: 0,
    zIndex: 3,
  },
  sidebarRailMobile: {
    position: 'absolute',
    top: 0,
    left: 0,
    background: 'transparent',
    borderRight: 'none',
    zIndex: 4,
  },
  sidebarBackdrop: {
    position: 'absolute',
    inset: 0,
    background: 'rgba(6, 10, 18, 0.64)',
    zIndex: 25,
  },
  sidebarMobile: {
    position: 'absolute',
    top: 0,
    bottom: 0,
    left: 0,
    width: 'min(84vw, 320px)',
    minWidth: 'min(84vw, 320px)',
    boxShadow: '0 18px 48px rgba(0, 0, 0, 0.32)',
    zIndex: 30,
  },
  sidebarHeader: {
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'stretch',
    gap: 2,
    padding: '8px 12px 10px',
  },
  sidebarTopbar: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: 8,
    minWidth: 0,
    height: 40,
  },
  newBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'flex-start',
    flex: 1,
    width: 'auto',
    minWidth: 0,
    minHeight: 40,
    gap: 12,
    padding: '0 10px',
    border: 'none',
    borderRadius: 8,
    background: 'transparent',
    color: cssVar('text'),
    cursor: 'pointer',
    transition: cssVar('transition'),
    flexShrink: 0,
    fontFamily: cssVar('fontSans'),
    fontSize: 15,
    fontWeight: 400,
    lineHeight: '20px',
    letterSpacing: 0,
  },
  newBtnIcon: {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 20,
    height: 20,
    flexShrink: 0,
  },
  convList: {
    flex: 1,
    overflowY: 'auto',
    padding: '0 12px 12px',
  },
  convItem: {
    display: 'flex',
    alignItems: 'center',
    gap: 12,
    minHeight: 36,
    padding: '7px 10px',
    borderRadius: 6,
    cursor: 'pointer',
    transition: cssVar('transition'),
    marginBottom: 4,
    color: cssVar('text'),
  },
  convItemActive: {
    background: cssVar('bgHover'),
    fontWeight: 600,
  },
  // 当前会话右侧的 6px 强调色圆点(HopBase 为橙)
  convDot: {
    width: 6,
    height: 6,
    flexShrink: 0,
    borderRadius: 999,
    background: 'var(--ag-accent, var(--ag-primary))',
  },
  convIcon: {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 20,
    height: 20,
    flexShrink: 0,
    transition: cssVar('transition'),
  },
  convTitle: {
    flex: 1,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
    fontSize: 15,
    lineHeight: '20px',
    letterSpacing: 0,
  },
  deleteBtn: {
    width: 22,
    height: 22,
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    background: 'transparent',
    border: 'none',
    borderRadius: 6,
    cursor: 'pointer',
    padding: 0,
    lineHeight: 1,
    flexShrink: 0,
    marginTop: 0,
  },
  emptyConvList: {
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
    gap: 8,
    padding: '32px 16px',
    color: cssVar('textTertiary'),
    fontSize: 12,
  },
  balanceBar: {
    display: 'flex',
    justifyContent: 'space-between',
    alignItems: 'baseline',
    gap: 12,
    margin: '8px 12px 12px',
    padding: '12px 10px 0',
    borderTop: `1px solid ${cssVar('borderSubtle')}`,
  },
  balanceLabel: {
    fontSize: 12,
    fontWeight: 500,
    color: cssVar('textTertiary'),
    letterSpacing: '0.02em',
  },
  balanceValue: {
    fontSize: 12,
    fontWeight: 500,
    color: cssVar('text'),
    fontVariantNumeric: 'tabular-nums',
  },

  // ── Main ──
  main: {
    position: 'relative' as const,
    flex: 1,
    display: 'flex',
    flexDirection: 'column',
    minHeight: 0,
    minWidth: 0,
    overflow: 'hidden',
  },
  toggleBtn: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 40,
    minWidth: 40,
    height: 40,
    border: 'none',
    borderRadius: cssVar('radiusSm'),
    background: 'transparent',
    color: cssVar('textSecondary'),
    cursor: 'pointer',
    transition: cssVar('transition'),
    flexShrink: 0,
  },
  // 模型和 reasoning 选择器嵌在输入卡片内，保持透明底，避免控件层级过重。
  selectors: {
    display: 'flex',
    alignItems: 'center',
    gap: 6,
    flexWrap: 'nowrap',
    minWidth: 0,
    flex: 1,
    overflow: 'hidden',
  },
  selectorsMobile: {
    width: '100%',
    gap: 4,
    rowGap: 2,
    flexWrap: 'wrap',
  },
  selectTrigger: {
    display: 'block',
    width: 'auto',
    maxWidth: '100%',
    height: 28,
    padding: '0 8px',
    border: `1px solid ${cssVar('borderSubtle')}`,
    borderRadius: 7,
    backgroundColor: cssVar('bgHover'),
    backgroundClip: 'padding-box',
    color: cssVar('textSecondary'),
    fontFamily: 'inherit',
    fontSize: 12,
    fontWeight: 600,
    lineHeight: 'normal',
    outline: 'none',
    appearance: 'auto',
    WebkitAppearance: 'menulist',
    cursor: 'pointer',
    transition: cssVar('transition'),
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
    boxSizing: 'border-box',
  },
  // ── Messages ──
  // ThreadPrimitive.Root 外壳：透明布局容器，让 Viewport 撑满主列
  threadRoot: {
    display: 'flex',
    flexDirection: 'column',
    flex: 1,
    minHeight: 0,
    minWidth: 0,
  },
  messagesArea: {
    flex: 1,
    minHeight: 0,
    overflowY: 'auto',
    display: 'flex',
    flexDirection: 'column',
    position: 'relative',
    paddingTop: 0,
  },
  // 跳到底部胶囊（贴底时经 .pg-jump-bottom[disabled] CSS 隐藏）
  jumpToBottomWrap: {
    position: 'sticky',
    bottom: 10,
    display: 'flex',
    justifyContent: 'center',
    pointerEvents: 'none',
    zIndex: 5,
  },
  jumpToBottomBtn: {
    pointerEvents: 'auto',
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    padding: '6px 14px',
    fontSize: 12,
    fontWeight: 500,
    borderRadius: 999,
    border: `1px solid ${cssVar('borderSubtle')}`,
    background: cssVar('bgElevated'),
    color: cssVar('textSecondary'),
    cursor: 'pointer',
    boxShadow: '0 6px 20px rgba(0, 0, 0, 0.14)',
    transition: cssVar('transition'),
  },

  // ── Empty state ──
  // 居中、克制、靠层级与留白说话。一个标题、一个描述、一个 primary CTA。
  emptyState: {
    display: 'flex',
    flexDirection: 'column',
    alignItems: 'center',
    justifyContent: 'center',
    flex: 1,
    gap: 14,
    padding: '40px 32px',
    maxWidth: 480,
    margin: '0 auto',
    width: '100%',
    textAlign: 'center',
    animation: 'pg-fadein 0.4s ease-out',
  },
  emptyStateMobile: {
    padding: '32px 24px',
    gap: 12,
  },
  emptyTitle: {
    fontSize: 32,
    fontWeight: 500,
    color: cssVar('text'),
    lineHeight: 1.18,
    letterSpacing: '-0.018em',
    margin: 0,
  },
  emptyDesc: {
    fontSize: 14,
    color: cssVar('textTertiary'),
    lineHeight: 1.55,
    margin: 0,
  },
  emptyBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    padding: '10px 20px',
    border: 'none',
    borderRadius: 999,
    background: cssVar('primary'),
    color: cssVar('textInverse'),
    fontSize: 13,
    fontWeight: 500,
    cursor: 'pointer',
    transition: cssVar('transition'),
    marginTop: 12,
  },

  // ── Message row ──
  // ChatGPT 风格：用户消息右对齐圆角气泡（bgSurface），助手消息左对齐无气泡纯
  // 排版。两侧都不显示 avatar 和"你/助手"role label。借助 padding 拉空气，
  // 取消行间分割线。整列居中、限定 768px 宽，营造窄列阅读体验。
  messageRow: {
    display: 'flex',
    width: '100%',
    maxWidth: 768,
    margin: '0 auto',
    padding: '14px 24px',
  },
  messageRowMobile: {
    padding: '10px 14px',
  },
  messageRowUser: {
    justifyContent: 'flex-end',
  },
  messageRowAssistant: {
    justifyContent: 'flex-start',
  },
  userBubble: {
    maxWidth: '78%',
    minWidth: 0,
    padding: '11px 16px',
    borderRadius: 18,
    background: cssVar('bgSurface'),
    border: `1px solid ${cssVar('borderSubtle')}`,
  },
  userBubbleMobile: {
    maxWidth: '82%',
    padding: '10px 13px',
    borderRadius: 16,
  },
  assistantBlock: {
    maxWidth: '100%',
    width: '100%',
    minWidth: 0,
  },
  messageCopyBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 24,
    height: 24,
    border: `1px solid ${cssVar('borderSubtle')}`,
    borderRadius: '999px',
    background: 'transparent',
    color: cssVar('textTertiary'),
    cursor: 'pointer',
    transition: cssVar('transition'),
  },
  // 悬浮复制按钮的布局壳（显隐由 .pg-msg-copy CSS 控制，见 keyframes 注入串）
  messageCopyInline: {
    display: 'inline-flex',
    verticalAlign: 'text-bottom',
    marginLeft: 6,
    transform: 'translateY(1px)',
    transition: cssVar('transition'),
  },
  messageCopyAfterTextBtn: {
    width: 22,
    height: 22,
  },
  messageContent: {
    fontSize: 14,
    lineHeight: 1.72,
    wordBreak: 'break-word',
    color: cssVar('text'),
  },
  markdownParagraph: {
    margin: '0 0 11px',
  },
  markdownH1: {
    margin: '4px 0 14px',
    fontSize: 22,
    fontWeight: 600,
    lineHeight: 1.25,
    letterSpacing: '-0.015em',
    color: cssVar('text'),
  },
  markdownH2: {
    margin: '18px 0 10px',
    fontSize: 18,
    fontWeight: 600,
    lineHeight: 1.3,
    letterSpacing: '-0.01em',
    color: cssVar('text'),
  },
  markdownH3: {
    margin: '16px 0 8px',
    fontSize: 15,
    fontWeight: 600,
    lineHeight: 1.35,
    color: cssVar('text'),
  },
  markdownH4: {
    margin: '14px 0 8px',
    fontSize: 14,
    fontWeight: 600,
    lineHeight: 1.4,
    color: cssVar('text'),
  },
  markdownList: {
    margin: '0 0 12px',
    paddingLeft: 20,
    color: cssVar('text'),
  },
  markdownNestedList: {
    margin: '4px 0 2px',
    paddingLeft: 18,
    color: cssVar('text'),
  },
  markdownListItem: {
    margin: '4px 0',
  },
  markdownTaskItem: {
    listStyle: 'none',
  },
  markdownTaskBox: {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 15,
    height: 15,
    marginLeft: -19,
    marginRight: 6,
    borderRadius: 4,
    border: `1.5px solid ${cssVar('border')}`,
    fontSize: 10,
    lineHeight: 1,
    verticalAlign: 'text-top',
    color: 'transparent',
    userSelect: 'none',
  },
  markdownTaskBoxChecked: {
    background: cssVar('primary'),
    borderColor: cssVar('primary'),
    color: cssVar('textInverse'),
  },
  markdownBlockquote: {
    margin: '0 0 12px',
    padding: '9px 13px',
    borderLeft: `3px solid ${cssVar('primary')}`,
    borderRadius: '0 10px 10px 0',
    background: cssVar('primarySubtle'),
    color: cssVar('textSecondary'),
  },
  // 代码块外壳：边框/圆角/背景由外壳承载，头部放语言标签与复制按钮
  markdownCodeShell: {
    margin: '4px 0 14px',
    borderRadius: cssVar('radiusSm'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    background: cssVar('bgDeep'),
    overflow: 'hidden',
  },
  markdownCodeHeader: {
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: 8,
    padding: '4px 8px 4px 14px',
    borderBottom: `1px solid ${cssVar('borderSubtle')}`,
    background: cssVar('bgHover'),
  },
  markdownCodeLang: {
    fontSize: 11,
    fontFamily: cssVar('fontMono'),
    color: cssVar('textSecondary'),
    letterSpacing: '0.03em',
    userSelect: 'none',
  },
  markdownCodeCopyBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 4,
    padding: '3px 8px',
    fontSize: 11,
    lineHeight: 1,
    borderRadius: 6,
    border: '1px solid transparent',
    background: 'transparent',
    color: cssVar('textSecondary'),
    cursor: 'pointer',
    transition: cssVar('transition'),
  },
  markdownCodeCopyBtnDone: {
    color: cssVar('primary'),
  },
  markdownCodeBlock: {
    margin: 0,
    padding: '13px 15px',
    color: cssVar('text'),
    fontFamily: cssVar('fontMono'),
    fontSize: 12.5,
    lineHeight: 1.72,
    overflowX: 'auto',
    whiteSpace: 'pre',
  },
  markdownInlineCode: {
    padding: '1px 5px 2px',
    borderRadius: 6,
    background: cssVar('bgSurface'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    color: cssVar('primary'),
    fontFamily: cssVar('fontMono'),
    fontSize: '0.9em',
  },
  markdownInlineMath: {
    display: 'inline-block',
    maxWidth: '100%',
    overflowX: 'auto',
    overflowY: 'hidden',
    verticalAlign: '-0.18em',
  },
  markdownBlockMath: {
    margin: '4px 0 14px',
    padding: '12px 14px',
    borderRadius: cssVar('radiusSm'),
    background: cssVar('bgSurface'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    color: cssVar('text'),
    overflowX: 'auto',
    overflowY: 'hidden',
  },
  markdownLink: {
    color: cssVar('primary'),
    textDecoration: 'underline',
    textDecorationColor: cssVar('primary'),
    textUnderlineOffset: 3,
  },
  markdownStrike: {
    opacity: 0.6,
  },
  markdownDivider: {
    height: 1,
    border: 0,
    background: cssVar('border'),
    margin: '16px 0',
  },
  // GFM 表格：容器兜横向滚动，宽表不撑破气泡
  markdownTableWrap: {
    margin: '4px 0 14px',
    overflowX: 'auto',
    border: `1px solid ${cssVar('borderSubtle')}`,
    borderRadius: cssVar('radiusSm'),
  },
  markdownTable: {
    width: '100%',
    borderCollapse: 'collapse',
    fontSize: 13.5,
    lineHeight: 1.6,
  },
  markdownTableHeadCell: {
    padding: '8px 12px',
    textAlign: 'left',
    fontWeight: 600,
    whiteSpace: 'nowrap',
    color: cssVar('text'),
    background: cssVar('bgHover'),
    borderBottom: `1px solid ${cssVar('border')}`,
  },
  markdownTableCell: {
    padding: '7px 12px',
    verticalAlign: 'top',
    color: cssVar('text'),
    borderBottom: `1px solid ${cssVar('borderSubtle')}`,
  },
  markdownTableCellLastRow: {
    borderBottom: 0,
  },
  markdownTableRowAlt: {
    background: 'rgba(128, 128, 128, 0.045)',
  },
  reasoningBox: {
    marginBottom: 10,
    padding: '10px 12px',
    borderRadius: cssVar('radiusSm'),
    background: cssVar('bgSurface'),
    border: `1px solid ${cssVar('borderSubtle')}`,
  },
  reasoningSummary: {
    cursor: 'pointer',
    display: 'flex',
    alignItems: 'center',
    gap: 8,
    fontSize: 12,
    fontWeight: 600,
    color: cssVar('textSecondary'),
    userSelect: 'none',
  },
  reasoningContent: {
    marginTop: 8,
    fontSize: 13,
    lineHeight: 1.6,
    wordBreak: 'break-word',
    color: cssVar('textSecondary'),
  },
  imageGroup: {
    display: 'flex',
    flexWrap: 'wrap',
    alignItems: 'flex-start',
    gap: 12,
    maxWidth: '100%',
    margin: '10px 0 6px',
  },
  imageGroupMobile: {
    gap: 8,
    marginTop: 8,
  },
  generatedImageFrame: {
    position: 'relative',
    display: 'inline-flex',
    flexDirection: 'column',
    alignItems: 'flex-start',
    gap: 8,
    flex: '1 1 180px',
    maxWidth: 'min(100%, 320px)',
    minWidth: 0,
  },
  generatedImageFrameMobile: {
    flex: '1 1 140px',
    maxWidth: 'min(100%, 240px)',
  },
  generatedImagePreviewBtn: {
    display: 'block',
    width: '100%',
    padding: 0,
    border: 'none',
    background: 'transparent',
    cursor: 'zoom-in',
    textAlign: 'left',
    font: 'inherit',
  },
  generatedImageDimensions: {
    display: 'block',
    marginTop: 4,
    fontSize: 11,
    fontFamily: 'ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
    color: cssVar('textTertiary'),
    textAlign: 'center' as const,
  },
  generatedImage: {
    display: 'block',
    maxHeight: 420,
    width: '100%',
    height: 'auto',
    borderRadius: cssVar('radiusMd'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    objectFit: 'contain',
  },
  imagePreviewOverlay: {
    position: 'absolute',
    inset: 0,
    zIndex: 100,
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'center',
    padding: 24,
    background: 'rgba(4, 7, 13, 0.78)',
    backdropFilter: 'blur(10px)',
  },
  imagePreviewModal: {
    position: 'relative',
    display: 'flex',
    maxWidth: 'min(94vw, 1120px)',
    maxHeight: '90vh',
    width: 'fit-content',
    borderRadius: cssVar('radiusLg'),
    border: `1px solid ${cssVar('border')}`,
    background: cssVar('bgDeep'),
    boxShadow: '0 28px 90px rgba(0, 0, 0, 0.45)',
    overflow: 'hidden',
  },
  imagePreviewNavBtn: {
    position: 'absolute',
    top: '50%',
    transform: 'translateY(-50%)',
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 42,
    height: 42,
    border: `1px solid ${cssVar('glassBorder')}`,
    borderRadius: '999px',
    background: cssVar('glass'),
    color: cssVar('text'),
    fontSize: 34,
    lineHeight: 1,
    cursor: 'pointer',
    backdropFilter: 'blur(10px)',
    boxShadow: cssVar('shadowMd'),
  },
  imagePreviewCounter: {
    position: 'absolute',
    left: '50%',
    bottom: 12,
    transform: 'translateX(-50%)',
    padding: '5px 10px',
    borderRadius: '999px',
    border: `1px solid ${cssVar('glassBorder')}`,
    background: cssVar('glass'),
    color: cssVar('textSecondary'),
    fontSize: 12,
    backdropFilter: 'blur(10px)',
    boxShadow: cssVar('shadowMd'),
  },
  imagePreviewCloseBtn: {
    position: 'absolute',
    top: 10,
    right: 10,
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 32,
    height: 32,
    border: `1px solid ${cssVar('glassBorder')}`,
    borderRadius: '999px',
    background: cssVar('glass'),
    color: cssVar('text'),
    fontSize: 22,
    lineHeight: 1,
    cursor: 'pointer',
    backdropFilter: 'blur(10px)',
    boxShadow: cssVar('shadowMd'),
  },
  imagePreviewLarge: {
    display: 'block',
    maxWidth: 'min(94vw, 1120px)',
    maxHeight: '90vh',
    width: 'auto',
    height: 'auto',
    objectFit: 'contain',
    background: cssVar('bgDeep'),
  },
  interactionNotice: {
    position: 'sticky',
    bottom: 12,
    alignSelf: 'center',
    zIndex: 4,
    padding: '7px 12px',
    borderRadius: '999px',
    background: cssVar('bgElevated'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    color: cssVar('textSecondary'),
    fontSize: 12,
    boxShadow: cssVar('shadowMd'),
  },
  messageMeta: {
    display: 'flex',
    alignItems: 'center',
	flexWrap: 'wrap',
    gap: 6,
    marginTop: 8,
    fontSize: 11,
    color: cssVar('textTertiary'),
  },
  metaBadge: {
    display: 'inline-flex',
    padding: '2px 8px',
    borderRadius: '999px',
    background: cssVar('bgSurface'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    fontSize: 11,
    fontFamily: cssVar('fontMono'),
    color: cssVar('textSecondary'),
  },
  streamingDot: {
    width: 6,
    height: 6,
    borderRadius: '50%',
    background: cssVar('primary'),
    animation: 'pg-pulse 1.2s ease-in-out infinite',
  },
  thinkingDots: {
    animation: 'pg-pulse 1.5s ease-in-out infinite',
  },

  // ── Error ──
  // 失败提示走「安静卡片」：中性抬升面 + 细边 + 柔和阴影,红只作为图标 chip 的信号色,
  // 不再整条染红。圆角 16(与气泡18/输入卡20 同族,宽卡略收),入场用 pg-fadein 轻推。
  errorBar: {
    display: 'flex',
    alignItems: 'center',
    gap: 10,
    // 与消息栏同列宽（messageRow maxWidth 768 - 两侧 24px 内边距），居中不横贯全屏
    width: 'calc(100% - 48px)',
    maxWidth: 720,
    boxSizing: 'border-box',
    margin: '10px auto',
    padding: '10px 12px',
    borderRadius: 16,
    background: cssVar('bgElevated'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    boxShadow: cssVar('shadowMd'),
    color: cssVar('textSecondary'),
    fontSize: 13,
    lineHeight: 1.5,
    animation: 'pg-fadein 0.24s ease-out',
  },
  errorBarMobile: {
    width: 'calc(100% - 20px)',
    margin: '8px auto',
  },
  errorIcon: {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 26,
    height: 26,
    flexShrink: 0,
    borderRadius: '999px',
    background: `color-mix(in oklab, ${cssVar('danger')} 12%, transparent)`,
    color: cssVar('danger'),
  },
  // 恢复条(跨会话未完成)不是错误,图标 chip 走中性色,与红色错误 chip 区分开
  recoveryIcon: {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 26,
    height: 26,
    flexShrink: 0,
    borderRadius: '999px',
    background: cssVar('bgHover'),
    color: cssVar('textSecondary'),
  },
  errorMessage: {
    flex: 1,
    minWidth: 0,
    color: cssVar('textSecondary'),
  },
  errorRetryBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    flexShrink: 0,
    height: 28,
    padding: '0 12px',
    borderRadius: '999px',
    border: `1px solid ${cssVar('borderSubtle')}`,
    background: cssVar('bgHover'),
    color: cssVar('textSecondary'),
    fontSize: 12,
    fontWeight: 600,
    cursor: 'pointer',
    transition: cssVar('transition'),
    fontFamily: cssVar('fontSans'),
  },

  // ── Input ──
  inputArea: {
    padding: '12px 28px 20px',
    background: 'transparent',
    flexShrink: 0,
  },
  inputAreaMobile: {
    padding: '8px 10px 10px',
  },
  inputWrapper: {
    display: 'flex',
    flexDirection: 'column',
    gap: 0,
    border: `1px solid ${cssVar('glassBorder')}`,
    borderRadius: 20,
    background: cssVar('bgElevated'),
    paddingTop: 6,
    paddingRight: 6,
    paddingBottom: 10,
    paddingLeft: 6,
    boxShadow: '0 8px 32px rgba(0, 0, 0, 0.18), 0 2px 12px rgba(0, 0, 0, 0.08)',
    transition: 'box-shadow 0.2s ease, border-color 0.2s ease',
    width: '100%',
    maxWidth: 768,
    margin: '0 auto',
  },
  inputWrapperMobile: {
    borderRadius: 20,
    paddingTop: 6,
    paddingRight: 6,
    paddingBottom: 8,
    paddingLeft: 6,
  },
  inputWrapperStreaming: {
    borderColor: cssVar('borderSubtle'),
  },
  imagePreviewList: {
    display: 'flex',
    flexWrap: 'wrap',
    gap: 8,
    padding: '6px 12px 2px',
  },
  imagePreviewItem: {
    position: 'relative',
    width: 76,
    height: 76,
    padding: 0,
    borderRadius: cssVar('radiusSm'),
    overflow: 'hidden',
    border: `1px solid ${cssVar('borderSubtle')}`,
    background: cssVar('bgHover'),
  },
  imagePreview: {
    width: '100%',
    height: '100%',
    objectFit: 'cover',
    display: 'block',
  },
  filePreviewItem: {
    position: 'relative',
    display: 'flex',
    alignItems: 'center',
    gap: 8,
    width: 180,
    maxWidth: '100%',
    height: 42,
    padding: '0 30px 0 10px',
    borderRadius: cssVar('radiusSm'),
    border: `1px solid ${cssVar('borderSubtle')}`,
    background: cssVar('bgHover'),
    color: cssVar('textSecondary'),
    boxSizing: 'border-box',
  },
  // 移动端占满整行，文件名显示更完整（避免固定 180 截断长文件名）
  filePreviewItemMobile: {
    width: '100%',
  },
  filePreviewIcon: {
    display: 'inline-flex',
    alignItems: 'center',
    justifyContent: 'center',
    width: 22,
    height: 22,
    flexShrink: 0,
    color: cssVar('textTertiary'),
  },
  filePreviewName: {
    minWidth: 0,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
    fontSize: 12,
    fontWeight: 600,
  },
  removeImageBtn: {
    position: 'absolute',
    top: 4,
    right: 4,
    width: 20,
    height: 20,
    border: `1px solid ${cssVar('glassBorder')}`,
    borderRadius: 999,
    background: cssVar('glass'),
    color: cssVar('text'),
    cursor: 'pointer',
    lineHeight: '20px',
    padding: 0,
    fontSize: 16,
  },
  textarea: {
    width: '100%',
    height: PLAYGROUND_COMPOSER_TEXTAREA_HEIGHT,
    minHeight: PLAYGROUND_COMPOSER_TEXTAREA_HEIGHT,
    maxHeight: PLAYGROUND_COMPOSER_TEXTAREA_HEIGHT,
    padding: '8px 14px',
    border: 'none',
    background: 'transparent',
    color: cssVar('text'),
    fontSize: 14,
    fontFamily: 'inherit',
    resize: 'none',
    outline: 'none',
    lineHeight: 1.6,
    overflowY: 'auto',
    boxSizing: 'border-box',
  },
  inputActions: {
    display: 'flex',
    justifyContent: 'space-between',
    alignItems: 'center',
    gap: 8,
    padding: '2px 8px 0',
  },
  inputActionsMobile: {
    alignItems: 'stretch',
    flexDirection: 'column',
    gap: 7,
  },
  inputButtonGroup: {
    display: 'flex',
    alignItems: 'center',
    gap: 8,
  },
  inputButtonGroupMobile: {
    width: '100%',
    minWidth: 0,
    justifyContent: 'space-between',
    gap: 6,
  },
  fileInput: {
    display: 'none',
  },
  attachBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    height: 28,
    padding: '0 10px',
    border: `1px solid ${cssVar('borderSubtle')}`,
    borderRadius: 7,
    background: cssVar('bgHover'),
    color: cssVar('textSecondary'),
    fontSize: 11,
    fontWeight: 600,
    lineHeight: '26px',
    whiteSpace: 'nowrap',
    boxSizing: 'border-box',
    cursor: 'pointer',
    transition: cssVar('transition'),
  },
  thinkingToggleBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    height: 28,
    padding: '0 10px',
    border: `1px solid ${cssVar('borderSubtle')}`,
    borderRadius: 7,
    background: cssVar('bgHover'),
    color: cssVar('textSecondary'),
    fontSize: 11,
    fontWeight: 600,
    lineHeight: '26px',
    whiteSpace: 'nowrap',
    boxSizing: 'border-box',
    cursor: 'pointer',
    transition: cssVar('transition'),
  },
  thinkingToggleBtnActive: {
    background: cssVar('primarySubtle'),
    borderColor: `color-mix(in oklab, ${cssVar('primary')} 38%, transparent)`,
    color: cssVar('primary'),
  },
  sendBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    height: 28,
    padding: '0 12px',
    border: `1px solid color-mix(in oklab, ${cssVar('primary')} 45%, transparent)`,
    borderRadius: 7,
    background: cssVar('primary'),
    color: cssVar('textInverse'),
    fontSize: 12,
    fontWeight: 600,
    lineHeight: '26px',
    whiteSpace: 'nowrap',
    boxSizing: 'border-box',
    cursor: 'pointer',
    transition: cssVar('transition'),
  },
  stopBtn: {
    display: 'inline-flex',
    alignItems: 'center',
    gap: 6,
    height: 28,
    padding: '0 12px',
    border: `1px solid color-mix(in oklab, ${cssVar('danger')} 35%, transparent)`,
    borderRadius: 7,
    background: cssVar('dangerSubtle'),
    color: cssVar('danger'),
    fontSize: 12,
    fontWeight: 600,
    lineHeight: '26px',
    whiteSpace: 'nowrap',
    boxSizing: 'border-box',
    cursor: 'pointer',
    transition: cssVar('transition'),
  },
  actionBtnMobile: {
    flex: '0 1 auto',
    minWidth: 44,
    minHeight: 36,
    justifyContent: 'center',
    padding: '8px 11px',
  },
};
