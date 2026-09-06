import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from 'react';
import { createPortal } from 'react-dom';
import { useTranslation } from 'react-i18next';
import { cssVar } from '@doudou-start/airgate-theme';
import type { ModelInfo } from '../types';

export interface ModelChoice {
  value: string;
  model: ModelInfo;
}

interface ModelPickerProps {
  choices: ModelChoice[];
  value: string;
  onChange: (value: string) => void;
  ariaLabel: string;
  triggerStyle?: CSSProperties;
}

const PLATFORM_LABEL: Record<string, string> = {
  claude: 'Claude',
  openai: 'OpenAI',
  gemini: 'Gemini',
  kiro: 'Kiro',
};

/** 模型名首段作为归组键(Claude / GPT / Gemini / DeepSeek / GLM …),没有名字时退回平台 */
function familyOf(model: ModelInfo): string {
  const name = (model.name || model.id).trim();
  const head = name.split(/[\s\-_/·]+/)[0];
  return head || PLATFORM_LABEL[model.platform] || model.platform;
}

function contextLabel(model: ModelInfo): string {
  const n = model.context_window;
  if (!n) return '';
  if (n >= 1_000_000) return `${Math.round(n / 100_000) / 10}M`;
  if (n >= 1_000) return `${Math.round(n / 1_000)}K`;
  return String(n);
}

/**
 * 模型选择器:原生 <select> 在几十个模型时只能给一条长清单;这里改成搜索 + 按系列归组的弹层,
 * 触发器只显示模型名与平台。纯展示层,选中值仍是 modelOptionValue,与原来一致。
 */
export function ModelPicker({ choices, value, onChange, ariaLabel, triggerStyle }: ModelPickerProps) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const rootRef = useRef<HTMLDivElement>(null);
  const popoverRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  // 弹层 portal 到 body,按触发器位置向上展开:输入区容器带 overflow hidden,放在里面会被裁掉
  const [anchor, setAnchor] = useState<{ left: number; bottom: number; maxHeight: number } | null>(null);

  const selected = choices.find((choice) => choice.value === value)?.model;

  const groups = useMemo(() => {
    const q = query.trim().toLowerCase();
    const map = new Map<string, ModelChoice[]>();
    for (const choice of choices) {
      const m = choice.model;
      if (q && !`${m.name} ${m.id} ${m.platform}`.toLowerCase().includes(q)) continue;
      const key = familyOf(m);
      if (!map.has(key)) map.set(key, []);
      map.get(key)!.push(choice);
    }
    return [...map.entries()]
      .sort((a, b) => a[0].localeCompare(b[0], undefined, { sensitivity: 'base' }))
      .map(([family, items]) => ({
        family,
        items: [...items].sort((a, b) => (a.model.name || a.model.id).localeCompare(b.model.name || b.model.id, undefined, { numeric: true })),
      }));
  }, [choices, query]);

  useLayoutEffect(() => {
    if (!open) { setAnchor(null); return; }
    const place = () => {
      const rect = rootRef.current?.getBoundingClientRect();
      if (!rect) return;
      const gap = 6;
      setAnchor({
        left: Math.max(8, Math.min(rect.left, window.innerWidth - 348)),
        bottom: Math.max(8, window.innerHeight - rect.top + gap),
        maxHeight: Math.min(400, rect.top - gap - 12),
      });
    };
    place();
    window.addEventListener('resize', place);
    window.addEventListener('scroll', place, true);
    return () => {
      window.removeEventListener('resize', place);
      window.removeEventListener('scroll', place, true);
    };
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const onDown = (event: MouseEvent) => {
      const target = event.target as Node;
      if (rootRef.current?.contains(target) || popoverRef.current?.contains(target)) return;
      setOpen(false);
    };
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false);
    };
    document.addEventListener('mousedown', onDown);
    document.addEventListener('keydown', onKey);
    const timer = window.setTimeout(() => searchRef.current?.focus(), 0);
    return () => {
      document.removeEventListener('mousedown', onDown);
      document.removeEventListener('keydown', onKey);
      window.clearTimeout(timer);
    };
  }, [open]);

  const pick = (next: string) => {
    onChange(next);
    setOpen(false);
    setQuery('');
  };

  return (
    <div ref={rootRef} style={rootStyle}>
      <button
        type="button"
        className="pg-composer-select pg-model-trigger"
        aria-label={ariaLabel}
        aria-haspopup="listbox"
        aria-expanded={open}
        style={{ ...triggerStyle, ...triggerBaseStyle }}
        onClick={() => setOpen((v) => !v)}
        title={selected ? `${selected.name || selected.id} · ${selected.platform}` : ariaLabel}
      >
        <span style={triggerNameStyle}>{selected ? (selected.name || selected.id) : ariaLabel}</span>
        {selected ? <span style={triggerMetaStyle}>{PLATFORM_LABEL[selected.platform] || selected.platform}</span> : null}
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" style={{ flexShrink: 0, opacity: 0.6 }}>
          <path d="m6 9 6 6 6-6" />
        </svg>
      </button>

      {open && anchor ? createPortal(
        <div ref={popoverRef} className="pg-model-popover" role="listbox" aria-label={ariaLabel} style={{ ...popoverStyle, left: anchor.left, bottom: anchor.bottom, maxHeight: anchor.maxHeight }}>
          <div style={searchWrapStyle}>
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" style={{ flexShrink: 0, color: cssVar('textTertiary') }}>
              <circle cx="11" cy="11" r="8" /><path d="m21 21-4.3-4.3" />
            </svg>
            <input
              ref={searchRef}
              className="pg-model-search"
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder={t('playground.model_search', { defaultValue: '搜索模型' })}
              aria-label={t('playground.model_search', { defaultValue: '搜索模型' })}
              style={searchInputStyle}
            />
          </div>
          <div style={listStyle}>
            {groups.length === 0 ? (
              <div style={emptyStyle}>{t('playground.model_no_match', { defaultValue: '没有匹配的模型' })}</div>
            ) : groups.map((group) => (
              <div key={group.family}>
                <div style={groupLabelStyle}>{group.family}<span style={groupCountStyle}>{group.items.length}</span></div>
                {group.items.map(({ value: itemValue, model }) => {
                  const active = itemValue === value;
                  const meta = [contextLabel(model), PLATFORM_LABEL[model.platform] || model.platform].filter(Boolean).join(' · ');
                  return (
                    <button
                      key={itemValue}
                      type="button"
                      role="option"
                      aria-selected={active}
                      className="pg-model-item"
                      data-active={active ? 'true' : undefined}
                      style={itemStyle}
                      onClick={() => pick(itemValue)}
                    >
                      <span style={{ minWidth: 0, flex: 1 }}>
                        <span style={{ ...itemNameStyle, fontWeight: active ? 600 : 500 }}>{model.name || model.id}</span>
                        <span style={itemMetaStyle}>{meta}</span>
                      </span>
                      {active ? (
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" style={{ flexShrink: 0, color: 'var(--ag-accent, var(--ag-primary))' }}>
                          <path d="M20 6 9 17l-5-5" />
                        </svg>
                      ) : null}
                    </button>
                  );
                })}
              </div>
            ))}
          </div>
        </div>,
        document.body,
      ) : null}
    </div>
  );
}

const rootStyle: CSSProperties = { position: 'relative', minWidth: 0, display: 'inline-flex' };

const triggerBaseStyle: CSSProperties = {
  display: 'inline-flex',
  alignItems: 'center',
  gap: 6,
  minWidth: 0,
  appearance: 'none',
  WebkitAppearance: 'none',
  textAlign: 'left',
};

const triggerNameStyle: CSSProperties = {
  minWidth: 0,
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
  color: cssVar('text'),
};

const triggerMetaStyle: CSSProperties = {
  flexShrink: 0,
  fontSize: 11,
  fontWeight: 500,
  color: cssVar('textTertiary'),
};

const popoverStyle: CSSProperties = {
  position: 'fixed',
  zIndex: 1200,
  width: 340,
  maxWidth: 'calc(100vw - 32px)',
  display: 'flex',
  flexDirection: 'column',
  maxHeight: 400,
  padding: 6,
  border: `1px solid ${cssVar('borderSubtle')}`,
  borderRadius: 6,
  background: cssVar('bgSurface'),
  boxShadow: '0 12px 28px rgba(11, 13, 12, 0.10)',
};

const searchWrapStyle: CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 8,
  height: 32,
  padding: '0 8px',
  marginBottom: 4,
  border: `1px solid ${cssVar('borderSubtle')}`,
  borderRadius: 4,
  background: cssVar('bgElevated'),
};

const searchInputStyle: CSSProperties = {
  flex: 1,
  minWidth: 0,
  border: 0,
  outline: 'none',
  background: 'transparent',
  color: cssVar('text'),
  fontSize: 12.5,
  fontFamily: 'inherit',
};

const listStyle: CSSProperties = { overflowY: 'auto', minHeight: 0 };

const groupLabelStyle: CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 6,
  padding: '8px 8px 4px',
  fontFamily: cssVar('fontMono'),
  fontSize: 10.5,
  letterSpacing: '0.1em',
  textTransform: 'uppercase',
  color: cssVar('textTertiary'),
};

const groupCountStyle: CSSProperties = { fontWeight: 400, letterSpacing: 0, opacity: 0.8 };

const itemStyle: CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 8,
  width: '100%',
  padding: '6px 8px',
  border: 0,
  borderRadius: 4,
  background: 'transparent',
  color: cssVar('text'),
  font: 'inherit',
  textAlign: 'left',
  cursor: 'pointer',
};

const itemNameStyle: CSSProperties = {
  display: 'block',
  fontSize: 12.5,
  lineHeight: 1.3,
  overflow: 'hidden',
  textOverflow: 'ellipsis',
  whiteSpace: 'nowrap',
};

const itemMetaStyle: CSSProperties = {
  display: 'block',
  marginTop: 1,
  fontSize: 11,
  color: cssVar('textTertiary'),
  whiteSpace: 'nowrap',
};

const emptyStyle: CSSProperties = {
  padding: '18px 8px',
  textAlign: 'center',
  fontSize: 12,
  color: cssVar('textTertiary'),
};
