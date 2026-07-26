import type { ChatMessageContent, ModelInfo } from './types';
import type { BlobUrlRegistry, PendingFile, PendingImage, PreviewImage } from './types';
import {
  ACTIVE_CONVERSATION_STORAGE_KEY,
  BASE64_DATA_URL_RE,
  DEFAULT_MODEL_ID,
  FILE_BLOCK_RE,
  IMAGE_MARKDOWN_ITEM_RE,
  IMAGE_MARKDOWN_RE,
  SELECTED_MODEL_STORAGE_KEY,
} from './constants';

export function dataUrlToBlob(dataUrl: string): Blob | null {
  const commaIdx = dataUrl.indexOf(',');
  if (commaIdx < 0) return null;
  const meta = dataUrl.slice(5, commaIdx);
  const mimeMatch = /^([^;]+)/.exec(meta);
  const mime = mimeMatch?.[1] || 'application/octet-stream';
  const b64 = dataUrl.slice(commaIdx + 1);
  try {
    const binary = atob(b64);
    const bytes = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) bytes[i] = binary.charCodeAt(i);
    return new Blob([bytes], { type: mime });
  } catch {
    return null;
  }
}

export function replaceBase64WithBlobUrls(content: string, registry: BlobUrlRegistry): string {
  if (!content || !content.includes('data:image/')) return content;
  return content.replace(BASE64_DATA_URL_RE, (dataUrl) => {
    const blob = dataUrlToBlob(dataUrl);
    if (!blob) return dataUrl;
    const url = URL.createObjectURL(blob);
    registry.set(url, dataUrl);
    return url;
  });
}

export function replaceBlobUrlsWithBase64(content: string, registry: BlobUrlRegistry): string {
  if (!content || !content.includes('blob:')) return content;
  return content.replace(/blob:[^\s)"']+/g, (url) => registry.get(url) || url);
}

export function revokeBlobRegistry(registry: BlobUrlRegistry) {
  registry.forEach((_, url) => URL.revokeObjectURL(url));
  registry.clear();
}

export function escapeMarkdownAlt(text: string) {
  return text.replace(/[\]\\]/g, '');
}

export function messageContentWithAttachments(text: string, images: PendingImage[], files: PendingFile[]) {
  const body = text.trim();
  const imageMarkdown = images.map(image => `![${escapeMarkdownAlt(image.name)}](${image.url})`).join('\n');
  const fileText = files.map(file => {
    const truncatedAttr = file.truncated ? ' truncated="true"' : '';
    // 内容里的字面 </file> 会提前终止 FILE_BLOCK_RE 的非贪婪匹配，转义防串块
    const safeContent = file.content.replace(/<\/file>/gi, '<\\/file>');
    return `<file name="${escapeFileAttribute(file.name)}" type="${escapeFileAttribute(file.type || 'text/plain')}" size="${file.size}"${truncatedAttr}>\n${safeContent}\n</file>`;
  }).join('\n\n');
  return [body, fileText, imageMarkdown].filter(Boolean).join('\n\n');
}

export interface ParsedFileBlock {
  name: string;
  type: string;
  size: number;
  truncated: boolean;
  content: string;
}

// 把消息里的 <file> 块拆出来（渲染层折叠成 chip 用）。
export function splitFileBlocks(content: string): Array<{ kind: 'text'; text: string } | { kind: 'file'; block: ParsedFileBlock }> {
  const segments: Array<{ kind: 'text'; text: string } | { kind: 'file'; block: ParsedFileBlock }> = [];
  let lastIndex = 0;
  let match: RegExpExecArray | null;
  FILE_BLOCK_RE.lastIndex = 0;

  while ((match = FILE_BLOCK_RE.exec(content)) !== null) {
    const before = content.slice(lastIndex, match.index).trim();
    if (before) segments.push({ kind: 'text', text: before });
    segments.push({
      kind: 'file',
      block: {
        name: unescapeFileAttribute(match[1]),
        type: unescapeFileAttribute(match[2]),
        size: Number(match[3]) || 0,
        truncated: Boolean(match[4]),
        content: match[5],
      },
    });
    lastIndex = match.index + match[0].length;
  }

  const tail = content.slice(lastIndex).trim();
  if (tail) segments.push({ kind: 'text', text: tail });
  return segments;
}

export function stripFileBlocks(content: string) {
  FILE_BLOCK_RE.lastIndex = 0;
  return content.replace(FILE_BLOCK_RE, (_m, name: string) => `[文件: ${unescapeFileAttribute(name)}]`);
}

function escapeFileAttribute(text: string) {
  return text.replace(/[&"]/g, (ch) => (ch === '&' ? '&amp;' : '&quot;'));
}

function unescapeFileAttribute(text: string) {
  return text.replace(/&quot;/g, '"').replace(/&amp;/g, '&');
}

export function formatByteSize(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return '';
  if (bytes < 1024) return `${bytes}B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(bytes < 100 * 1024 ? 1 : 0)}KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)}MB`;
}

// 附件视频与图片共用 markdown 图片语法；渲染端按 URL 区分 <video> 与 <img>。
export function isVideoAttachmentUrl(url: string): boolean {
  if (url.startsWith('data:video/')) return true;
  const clean = url.split(/[?#]/)[0].toLowerCase();
  return /\.(mp4|mov|webm|m4v)$/.test(clean);
}

export function generatedImages(content: string): PreviewImage[] {
  const images: PreviewImage[] = [];
  let match: RegExpExecArray | null;
  IMAGE_MARKDOWN_ITEM_RE.lastIndex = 0;

  while ((match = IMAGE_MARKDOWN_ITEM_RE.exec(content)) !== null) {
    // 视频不进图片灯箱（索引须与 MarkdownImage 的 takeImageIndex 计数保持一致）
    if (isVideoAttachmentUrl(match[2])) continue;
    images.push({ alt: match[1], url: match[2] });
  }

  IMAGE_MARKDOWN_ITEM_RE.lastIndex = 0;
  return images;
}

export function stripImageMarkdown(content: string) {
  return content.replace(IMAGE_MARKDOWN_RE, '[Image]').trim() || '[Image]';
}

export function copyableMessageText(content: string) {
  return content.replace(IMAGE_MARKDOWN_RE, '[Image]').trim() || '[Image]';
}

export function hasCopyableMessageText(content: string) {
  return content.replace(IMAGE_MARKDOWN_RE, '').trim().length > 0;
}

export function titleFromMessageContent(content: string) {
  const title = stripFileBlocks(content).replace(IMAGE_MARKDOWN_RE, '[Image]').trim() || '[Image]';
  return title.slice(0, 30) + (title.length > 30 ? '...' : '');
}

export function toChatMessageContent(role: string, content: string): ChatMessageContent {
  if (role !== 'user') return stripImageMarkdown(content);

  const parts: Exclude<ChatMessageContent, string> = [];
  let lastIndex = 0;
  let match: RegExpExecArray | null;
  IMAGE_MARKDOWN_RE.lastIndex = 0;

  while ((match = IMAGE_MARKDOWN_RE.exec(content)) !== null) {
    const text = content.slice(lastIndex, match.index).trim();
    if (text) parts.push({ type: 'text', text });
    parts.push({ type: 'image_url', image_url: { url: match[1] } });
    lastIndex = match.index + match[0].length;
  }

  const tail = content.slice(lastIndex).trim();
  if (tail) parts.push({ type: 'text', text: tail });

  return parts.length ? parts : content;
}

export function supportsReasoning(model?: ModelInfo) {
  if (!model) return false;
  if (model.capabilities?.includes('reasoning') || model.capabilities?.includes('thinking')) return true;
  if (!model.capabilities?.length) return true;
  return false;
}

export function modelOptionValue(model: ModelInfo) {
  return `${encodeURIComponent(model.platform || '')}:${encodeURIComponent(model.id)}`;
}

export function normalizeModelName(value?: string) {
  return (value || '').toLowerCase().replace(/[-_\s]/g, '');
}

export function defaultModelOptionValue(models: ModelInfo[]) {
  const target = normalizeModelName(DEFAULT_MODEL_ID);
  const preferred = models.find(model => normalizeModelName(model.id) === target || normalizeModelName(model.name) === target);
  return preferred ? modelOptionValue(preferred) : (models[0] ? modelOptionValue(models[0]) : '');
}

export function readLocalStorageValue(key: string) {
  if (typeof window === 'undefined') return '';
  try {
    return window.localStorage.getItem(key) || '';
  } catch {
    return '';
  }
}

export function writeLocalStorageValue(key: string, value: string | null) {
  if (typeof window === 'undefined') return;
  try {
    if (value == null) window.localStorage.removeItem(key);
    else window.localStorage.setItem(key, value);
  } catch {
    // Storage can be unavailable in private mode or locked-down browsers.
  }
}

export function getStoredActiveConversationId() {
  const raw = readLocalStorageValue(ACTIVE_CONVERSATION_STORAGE_KEY);
  if (!raw) return null;
  const value = Number(raw);
  return Number.isInteger(value) && value > 0 ? value : null;
}

export function getStoredSelectedModel() {
  return readLocalStorageValue(SELECTED_MODEL_STORAGE_KEY);
}

export function isSafeLinkUrl(url: string) {
  return /^(https?:|mailto:|#)/i.test(url);
}

export function isSafeImageUrl(url: string) {
  return /^(data:(?:image\/(?:png|jpeg|jpg|webp|gif)|video\/(?:mp4|quicktime|webm|x-m4v));base64,|https?:|blob:|\/assets-runtime\/|\/api\/v1\/ext-user\/airgate-playground\/assets\/)/i.test(url);
}

function escapeHtml(text: string) {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

async function urlToDataURL(url: string): Promise<string | null> {
  try {
    const resp = await fetch(url, { credentials: 'include' });
    if (!resp.ok) return null;
    const blob = await resp.blob();
    return await new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result || ''));
      reader.onerror = () => reject(reader.error || new Error('read failed'));
      reader.readAsDataURL(blob);
    });
  } catch {
    return null;
  }
}

// 消息 → text/html 剪贴板片段：文本转义、图片内联为 data URL <img>，
// 粘贴到微信/Word/邮件等富文本编辑器时文字和图片一起带过去。
export async function messageContentToClipboardHtml(content: string): Promise<string> {
  // 用局部正则，避免与共享的全局 IMAGE_MARKDOWN_RE 争用 lastIndex（await 期间会被打断）
  const re = new RegExp(IMAGE_MARKDOWN_RE.source, 'g');
  type Seg = { kind: 'text'; text: string } | { kind: 'img'; url: string };
  const segs: Seg[] = [];
  let lastIndex = 0;
  let match: RegExpExecArray | null;
  while ((match = re.exec(content)) !== null) {
    segs.push({ kind: 'text', text: content.slice(lastIndex, match.index) });
    segs.push({ kind: 'img', url: match[1] });
    lastIndex = match.index + match[0].length;
  }
  segs.push({ kind: 'text', text: content.slice(lastIndex) });

  // 图片 URL 并行转 data URL（顺序 fetch 会让多图复制卡数秒）
  const dataUrls = await Promise.all(
    segs.map(seg => (seg.kind === 'img' && !seg.url.startsWith('data:') ? urlToDataURL(seg.url) : Promise.resolve(null))),
  );

  const parts: string[] = [];
  segs.forEach((seg, i) => {
    if (seg.kind === 'text') {
      const trimmed = seg.text.trim();
      if (trimmed) parts.push(`<div>${escapeHtml(trimmed).replace(/\n/g, '<br>')}</div>`);
    } else {
      const src = seg.url.startsWith('data:') ? seg.url : (dataUrls[i] || seg.url);
      parts.push(`<img src="${escapeHtml(src)}" alt="">`);
    }
  });
  return parts.join('');
}

// 复制消息：含图片时写 text/plain + text/html 双格式，纯文本场景/失败回退 copyText。
export async function copyMessageContent(content: string) {
  const plain = copyableMessageText(content);
  IMAGE_MARKDOWN_RE.lastIndex = 0;
  const hasImages = IMAGE_MARKDOWN_RE.test(content);
  IMAGE_MARKDOWN_RE.lastIndex = 0;
  if (
    !hasImages ||
    typeof ClipboardItem === 'undefined' ||
    !navigator.clipboard?.write ||
    !window.isSecureContext
  ) {
    await copyText(plain);
    return;
  }
  try {
    // text/html 用 Promise 形式传入：Safari 要求剪贴板写入保持在用户手势内
    const htmlBlob = messageContentToClipboardHtml(content)
      .then(html => new Blob([html], { type: 'text/html' }));
    await navigator.clipboard.write([
      new ClipboardItem({
        'text/plain': new Blob([plain], { type: 'text/plain' }),
        'text/html': htmlBlob,
      }),
    ]);
  } catch {
    await copyText(plain);
  }
}

// 跨会话恢复条的显隐判定（纯函数，便于单测）：重开/刷新后末位仍是用户提问、且当前
// 空闲(非流式、非发送中、无错误)时才显示。发送中由 isSubmitting 挡掉「落库→起流」空档
// 的误闪；实时失败由 hasError 让位给错误条——二者互斥，任意时刻最多一条。
export function isTailRecoverable(params: {
  activeId: number | null;
  isStreaming: boolean;
  isSubmitting: boolean;
  hasError: boolean;
  lastRole: string | undefined;
}): boolean {
  return Boolean(
    params.activeId &&
    !params.isStreaming &&
    !params.isSubmitting &&
    !params.hasError &&
    params.lastRole === 'user',
  );
}

// 回答达到输出上限时保留已生成内容，并提供显式续写入口。结束原因随消息持久化，
// 因此刷新会话后提示仍然成立；一旦用户继续对话，末位不再是该助手消息，提示消失。
export function isTailOutputLimited(params: {
  activeId: number | null;
  isStreaming: boolean;
  isSubmitting: boolean;
  hasError: boolean;
  lastRole: string | undefined;
  finishReason: string | undefined;
}): boolean {
  return Boolean(
    params.activeId &&
    !params.isStreaming &&
    !params.isSubmitting &&
    !params.hasError &&
    params.lastRole === 'assistant' &&
    params.finishReason === 'length',
  );
}

export async function copyText(text: string) {
  if (navigator.clipboard?.writeText && window.isSecureContext) {
    await navigator.clipboard.writeText(text);
    return;
  }

  const textarea = document.createElement('textarea');
  textarea.value = text;
  textarea.style.position = 'fixed';
  textarea.style.opacity = '0';
  textarea.style.pointerEvents = 'none';
  document.body.appendChild(textarea);
  textarea.select();
  const copied = document.execCommand('copy');
  textarea.remove();
  if (!copied) throw new Error('copy failed');
}
