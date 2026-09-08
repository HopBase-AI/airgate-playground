// 流式块内元素的身份稳定性回归。
//
// 背景：MarkdownEnv 的三个钩子曾用模块级自增计数器当 React key，每次渲染 key 都变。
// 已完成的块被 MemoBlock 按 content 挡住看不出问题，但「正在生长的最后一块」每个
// token 都会重渲染，于是块内的代码块 / 图片 / 公式每个 token 都被卸载重挂载——
// 表现为代码块闪烁、图片重解码、KaTeX 反复重排、块内已选中的文字被清空。
//
// 这里用 DOM 节点身份（同一个对象引用）作为「没有重挂载」的判据：只要 key 不稳定，
// React 就会销毁旧节点新建一个，identity 断裂即失败。
import { beforeAll, describe, expect, it } from 'vitest';
import { act, createElement, type ReactNode } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import i18n from 'i18next';
import { initReactI18next } from 'react-i18next';
import { renderMessageContent } from '../MessageRendering';

beforeAll(async () => {
  (globalThis as unknown as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  if (!i18n.isInitialized) {
    void i18n.use(initReactI18next).init({
      resources: { zh: { translation: {} } },
      lng: 'zh',
      interpolation: { escapeValue: false },
    });
  }
  // 公式走 React.lazy：先把模块真正装载进注册表，让 lazy 的 import() 只剩一个微任务，
  // 配合下面的 settle() 把 Suspense fallback→真身的切换排干净。
  await import('../../MathRenderer');
});

// settle 反复排空微任务与定时器，直到异步装载引起的节点替换全部结束。
async function settle(rounds = 5): Promise<void> {
  for (let i = 0; i < rounds; i += 1) {
    await act(async () => {
      await Promise.resolve();
      await new Promise(resolve => setTimeout(resolve, 0));
    });
  }
}

interface Harness {
  container: HTMLDivElement;
  render: (content: string) => Promise<void>;
  cleanup: () => Promise<void>;
}

async function mount(): Promise<Harness> {
  const container = document.createElement('div');
  document.body.appendChild(container);
  let root: Root;
  await act(async () => {
    root = createRoot(container);
  });
  return {
    container,
    render: async (content: string) => {
      await act(async () => {
        root.render(createElement('div', null, renderMessageContent(content) as ReactNode));
      });
    },
    cleanup: async () => {
      await act(async () => root.unmount());
      container.remove();
    },
  };
}

type Picker = string | ((root: Element) => Element | null);

function pickNode(root: Element, picker: Picker): Element | null {
  return typeof picker === 'string' ? root.querySelector(picker) : picker(root);
}

// 逐步喂入内容，返回每一步命中的节点；用于比对身份是否始终不变。
//
// 先用首帧内容渲染并把微任务/定时器排空再开始比对：公式走 React.lazy + Suspense，
// 懒加载模块 resolve 时 fallback→真身的节点替换是 React 的正常行为，与本用例
// 要验证的「内容增长时不重挂载」无关。不预热的话，用例结果会随模块缓存冷热
// （单跑 vs 全量跑）漂移。
async function nodesAcrossSteps(steps: string[], picker: Picker): Promise<Element[]> {
  const h = await mount();
  const seen: Element[] = [];
  try {
    await h.render(steps[0]);
    await settle();

    for (const step of steps) {
      await h.render(step);
      const node = pickNode(h.container, picker);
      expect(node, `渲染「${step.slice(0, 24)}…」后未命中目标节点`).toBeTruthy();
      seen.push(node!);
    }
  } finally {
    await h.cleanup();
  }
  return seen;
}

// 行内公式在测试环境下 katex 脚本不可达，MathRenderer 稳定降级为 <span>{tex}</span>。
function pickInlineMath(root: Element): Element | null {
  return Array.from(root.querySelectorAll('span'))
    .find(span => span.textContent === 'a^2+b^2=c^2') ?? null;
}

function allSameNode(nodes: Element[]): boolean {
  return nodes.every(node => node === nodes[0]);
}

describe('流式生长块内的元素不被重挂载', () => {
  it('未闭合代码块：<pre> 在逐段追加中保持同一节点', async () => {
    const nodes = await nodesAcrossSteps([
      '说明：\n\n```js\nconst a = 1;',
      '说明：\n\n```js\nconst a = 1;\nconst b = 2;',
      '说明：\n\n```js\nconst a = 1;\nconst b = 2;\nconst c = 3;',
    ], 'pre');
    expect(allSameNode(nodes)).toBe(true);
  });

  it('闭合后继续追加正文：代码块 <pre> 仍是同一节点', async () => {
    const nodes = await nodesAcrossSteps([
      '```js\nconst a = 1;\n```\n\n结论：',
      '```js\nconst a = 1;\n```\n\n结论：这段代码',
      '```js\nconst a = 1;\n```\n\n结论：这段代码没有副作用。',
    ], 'pre');
    expect(allSameNode(nodes)).toBe(true);
  });

  it('生长段落里的图片：<img> 保持同一节点（不会重新解码闪烁）', async () => {
    const nodes = await nodesAcrossSteps([
      '![示意](https://example.com/a.png) 这张图',
      '![示意](https://example.com/a.png) 这张图说明了',
      '![示意](https://example.com/a.png) 这张图说明了整体结构。',
    ], 'img');
    expect(allSameNode(nodes)).toBe(true);
  });

  it('生长段落里的行内公式：容器保持同一节点（不会反复重排）', async () => {
    const nodes = await nodesAcrossSteps([
      '其中 $a^2+b^2=c^2$ 成立',
      '其中 $a^2+b^2=c^2$ 成立，因此',
      '其中 $a^2+b^2=c^2$ 成立，因此可以推出结论。',
    ], pickInlineMath);
    expect(allSameNode(nodes)).toBe(true);
  });

  it('表格逐行到达：<table> 保持同一节点', async () => {
    const nodes = await nodesAcrossSteps([
      '| 模型 | 价格 |\n| --- | --- |\n| a | 1 |',
      '| 模型 | 价格 |\n| --- | --- |\n| a | 1 |\n| b | 2 |',
      '| 模型 | 价格 |\n| --- | --- |\n| a | 1 |\n| b | 2 |\n| c | 3 |',
    ], 'table');
    expect(allSameNode(nodes)).toBe(true);
  });

  it('代码块内容随流式增长，文本仍逐步补齐（身份稳定不等于内容冻结）', async () => {
    const h = await mount();
    try {
      await h.render('```js\nconst a = 1;');
      const first = h.container.querySelector('pre');
      expect(first!.textContent).toContain('const a = 1;');

      await h.render('```js\nconst a = 1;\nconst b = 2;');
      const second = h.container.querySelector('pre');
      expect(second).toBe(first);
      expect(second!.textContent).toContain('const b = 2;');
    } finally {
      await h.cleanup();
    }
  });
});
