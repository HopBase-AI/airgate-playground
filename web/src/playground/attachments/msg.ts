import { htmlToText } from './htmlToText';
import { isCfb, readCfb } from './cfb';
import { decodeCharsetBytes } from './mime';
import { truncateExtractedText } from './textExtract';
import type { AttachmentIssue, ExtractedText } from './types';

// .msg（Outlook）＝ CFB 容器 + MAPI 属性流。属性流命名 __substg1.0_<TAG><TYPE>：
// TAG 4 位十六进制属性号，TYPE 4 位十六进制类型（001F=UTF16LE, 001E=ASCII/codepage,
// 0102=binary）。这里按 TAG 取值，按 TYPE 解码。

const SUBSTG_RE = /^__substg1\.0_([0-9a-fA-F]{4})([0-9a-fA-F]{4})$/;

// 关注的 MAPI 属性号
const TAG_SUBJECT = 0x0037;
const TAG_SENDER_NAME = 0x0c1a;
const TAG_SENDER_EMAIL = 0x0c1f;
const TAG_DISPLAY_TO = 0x0e04;
const TAG_DISPLAY_CC = 0x0e03;
const TAG_BODY = 0x1000;
const TAG_BODY_HTML = 0x1013;
const TAG_CONVERSATION_TOPIC = 0x0070;

export interface MsgStream {
  tag: number;
  type: number;
  data: Uint8Array;
}

function decodeMapiString(stream: MsgStream): string {
  if (stream.type === 0x001f) {
    // UTF-16LE，遇 NUL 终止（MAPI 字符串以 NUL 结尾）
    let out = '';
    for (let i = 0; i + 1 < stream.data.length; i += 2) {
      const code = stream.data[i] | (stream.data[i + 1] << 8);
      if (code === 0) break;
      out += String.fromCharCode(code);
    }
    return out.trimEnd();
  }
  // 001E (ASCII/codepage) 或 binary 当文本：走 charset 回退（含 GBK 启发）
  const decoded = decodeCharsetBytes(stream.data, undefined);
  const nulIdx = decoded.indexOf(String.fromCharCode(0));
  return (nulIdx >= 0 ? decoded.slice(0, nulIdx) : decoded).trimEnd();
}

// 从 __properties 流里找 delivery time（0E06）或 client submit time（0039），PT_SYSTIME=0x0040。
// 结构：32 字节头 + 若干 16 字节定长属性记录（type 2 + id 2 + flags 4 + value 8）。
export function parseMsgDate(props: Uint8Array | undefined): string {
  if (!props || props.length < 48) return '';
  const dv = new DataView(props.buffer, props.byteOffset, props.byteLength);
  for (let off = 32; off + 16 <= props.length; off += 16) {
    const type = dv.getUint16(off, true);
    const id = dv.getUint16(off + 2, true);
    if (type === 0x0040 && (id === 0x0e06 || id === 0x0039)) {
      const ft = dv.getBigUint64(off + 8, true);
      // FILETIME(100ns since 1601) → unix ms
      const ms = Number(ft / 10000n) - 11644473600000;
      if (ms > 0 && ms < 4102444800000) {
        return new Date(ms).toISOString();
      }
    }
  }
  return '';
}

// 纯提取逻辑（不含 CFB 容器），便于单测：喂 name→bytes。
export function extractMsgFields(streams: Map<string, Uint8Array>, maxChars: number): ExtractedText {
  const warnings: AttachmentIssue[] = [];
  const byTag = new Map<number, MsgStream>();
  let props: Uint8Array | undefined;

  for (const [name, data] of streams) {
    if (name === '__properties_version1.0') {
      props = data;
      continue;
    }
    const m = SUBSTG_RE.exec(name);
    if (!m) continue;
    const tag = parseInt(m[1], 16);
    const type = parseInt(m[2], 16);
    // 同一 tag 优先 unicode(001F) 版本
    const existing = byTag.get(tag);
    if (!existing || (type === 0x001f && existing.type !== 0x001f)) {
      byTag.set(tag, { tag, type, data });
    }
  }

  const str = (tag: number): string => {
    const s = byTag.get(tag);
    return s ? decodeMapiString(s).trim() : '';
  };

  const subject = str(TAG_SUBJECT) || str(TAG_CONVERSATION_TOPIC);
  const senderName = str(TAG_SENDER_NAME);
  const senderEmail = str(TAG_SENDER_EMAIL);
  const from = [senderName, senderEmail && `<${senderEmail}>`].filter(Boolean).join(' ');
  const to = str(TAG_DISPLAY_TO);
  const cc = str(TAG_DISPLAY_CC);
  const date = parseMsgDate(props);

  let body = str(TAG_BODY);
  if (!body) {
    const htmlStream = byTag.get(TAG_BODY_HTML);
    if (htmlStream) body = htmlToText(decodeCharsetBytes(htmlStream.data, undefined)).text;
  }

  const headerLines = [
    from && `From: ${from}`,
    to && `To: ${to}`,
    cc && `Cc: ${cc}`,
    subject && `Subject: ${subject}`,
    date && `Date: ${date}`,
  ].filter(Boolean);

  if (!body) warnings.push({ code: 'attachment.email_no_body' });

  const combined = [headerLines.join('\n'), body.replace(/\r\n?/g, '\n').trim()]
    .filter(Boolean)
    .join('\n\n');
  const { content, truncated } = truncateExtractedText(combined, maxChars);
  if (truncated) warnings.push({ code: 'attachment.truncated' });
  return { content, truncated, warnings };
}

// .msg → From/To/Subject/Date + 正文。
export function extractMsg(buffer: ArrayBuffer, maxChars: number): ExtractedText {
  if (!isCfb(buffer)) {
    return { content: '', truncated: false, warnings: [{ code: 'attachment.parse_failed' }] };
  }
  let cfb;
  try {
    cfb = readCfb(buffer);
  } catch {
    return { content: '', truncated: false, warnings: [{ code: 'attachment.parse_failed' }] };
  }
  const streams = new Map<string, Uint8Array>();
  for (const entry of cfb.entries) {
    if (entry.type !== 2) continue; // 只取流
    if (entry.name === '__properties_version1.0' || SUBSTG_RE.test(entry.name)) {
      streams.set(entry.name, cfb.read(entry));
    }
  }
  return extractMsgFields(streams, maxChars);
}
