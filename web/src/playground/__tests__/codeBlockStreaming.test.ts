// 流式代码块必须始终显示当前内容。
//
// 高亮是异步的（懒加载 hljs + 150ms 防抖）。此前每次渲染 key 都在变，组件被重挂载，
// highlighted 状态跟着清空，所以未落地时总是回落到「当前」纯文本。移除 key 后组件
// 不再重挂载，若不做处理，highlighted 会保留上一版 HTML —— 代码增长时块里显示的
// 仍是更短的旧内容，观感是代码块卡住甚至回退，正好与「流式更丝滑」的目标相反。
//
// 这里给 window.hljs 装一个同步替身让高亮真的发生，再检查内容增长后的显示。
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, createElement, type ReactNode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import { renderMessageContent } from '../MessageRendering';

// 复用 highlight.ts 已声明的 window.hljs 类型，避免重复声明冲突
type HljsRuntime = NonNullable<Window['hljs']>;

describe('流式代码块内容跟随', () => {
  let container: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    if (!i18n.isInitialized) {
      void i18n.use(initReactI18next).init({
        resources: { zh: { translation: {} } },
        lng: 'zh',
        interpolation: { escapeValue: false },
      });
    }
    vi.useFakeTimers();
    // 同步替身：真实 hljs 走网络懒加载，测试里永远到不了，highlighted 恒为空，
    // 那样这条用例就测不到东西了。
    const fake: HljsRuntime = {
      getLanguage: () => true,
      highlight: (code: string) => ({ value: `<span class="hl">${code}</span>` }),
    };
    window.hljs = fake;
    container = document.createElement('div');
    document.body.appendChild(container);
  });

  afterEach(async () => {
    await act(async () => root?.unmount());
    container.remove();
    delete window.hljs;
    vi.useRealTimers();
  });

  async function render(content: string): Promise<void> {
    await act(async () => {
      root.render(createElement('div', null, renderMessageContent(content) as ReactNode));
    });
  }

  // 让防抖定时器与随后的 promise 链全部落地
  async function settleHighlight(): Promise<void> {
    await act(async () => { await vi.advanceTimersByTimeAsync(200); });
  }

  it('高亮落地后再追加内容：块内显示的必须是最新代码，而不是上一版', async () => {
    await act(async () => { root = createRoot(container); });

    await render('```js\nconst a = 1;\n');
    await settleHighlight();
    // 自检：高亮确实生效了，否则本用例测不到 stale 问题
    expect(container.querySelector('.hl'), '高亮替身未生效，探针无效').toBeTruthy();
    expect(container.querySelector('pre')!.textContent).toContain('const a = 1;');

    // 追加一行后立刻检查（防抖未到期）：此刻绝不能还显示旧的短内容
    await render('```js\nconst a = 1;\nconst b = 2;\n');
    const text = container.querySelector('pre')!.textContent ?? '';
    expect(text, '代码块停留在上一版内容（高亮状态未随 code 失效）').toContain('const b = 2;');
  });

  it('防抖落地后重新高亮到最新内容', async () => {
    await act(async () => { root = createRoot(container); });

    await render('```js\nconst a = 1;\n');
    await settleHighlight();
    await render('```js\nconst a = 1;\nconst b = 2;\n');
    await settleHighlight();

    const pre = container.querySelector('pre')!;
    expect(pre.querySelector('.hl'), '重新高亮未落地').toBeTruthy();
    expect(pre.textContent).toContain('const b = 2;');
  });

  it('连续多次追加，每一步显示的都是当步内容', async () => {
    await act(async () => { root = createRoot(container); });
    const steps = [
      '```js\nconst a = 1;\n',
      '```js\nconst a = 1;\nconst b = 2;\n',
      '```js\nconst a = 1;\nconst b = 2;\nconst c = 3;\n',
      '```js\nconst a = 1;\nconst b = 2;\nconst c = 3;\nconst d = 4;\n',
    ];
    const expected = ['const a = 1;', 'const b = 2;', 'const c = 3;', 'const d = 4;'];

    for (let i = 0; i < steps.length; i += 1) {
      await render(steps[i]);
      // 交替：偶数步让高亮落地，奇数步立刻检查（模拟 token 间隔长短不一）
      if (i % 2 === 0) await settleHighlight();
      const text = container.querySelector('pre')!.textContent ?? '';
      expect(text, `第 ${i + 1} 步显示的不是当步内容`).toContain(expected[i]);
    }
  });
});
