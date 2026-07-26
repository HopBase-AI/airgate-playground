import { describe, expect, it } from 'vitest';
import { decodeTextBuffer, extractPlainText, truncateExtractedText } from '../textExtract';

function utf8Buffer(text: string): ArrayBuffer {
  return new TextEncoder().encode(text).buffer;
}

describe('truncateExtractedText', () => {
  it('keeps short content untouched', () => {
    const { content, truncated } = truncateExtractedText('hello', 10);
    expect(content).toBe('hello');
    expect(truncated).toBe(false);
  });

  it('truncates and appends a marker', () => {
    const { content, truncated } = truncateExtractedText('a'.repeat(100), 10);
    expect(truncated).toBe(true);
    expect(content.startsWith('a'.repeat(10))).toBe(true);
    expect(content).toContain('内容已截断');
    expect(content).toContain('100 字符');
  });
});

describe('decodeTextBuffer', () => {
  it('decodes utf-8 without fallback', () => {
    const { text, charset } = decodeTextBuffer(utf8Buffer('中文内容 hello'));
    expect(text).toBe('中文内容 hello');
    expect(charset).toBe('utf-8');
  });

  it('falls back to gbk for gbk-encoded chinese', () => {
    // "中文" 的 GBK 编码：D6 D0 CE C4
    const gbkBytes = new Uint8Array([0xd6, 0xd0, 0xce, 0xc4]);
    const { text, charset } = decodeTextBuffer(gbkBytes.buffer);
    expect(charset).toBe('gbk');
    expect(text).toBe('中文');
  });
});

describe('extractPlainText', () => {
  it('normalizes CRLF and strips NUL', () => {
    const { content } = extractPlainText(utf8Buffer('a\r\nb\rc\u0000d'), 100);
    expect(content).toBe('a\nb\ncd');
  });

  it('reports truncation warning', () => {
    const result = extractPlainText(utf8Buffer('x'.repeat(50)), 10);
    expect(result.truncated).toBe(true);
    expect(result.warnings.some(w => w.code === 'attachment.truncated')).toBe(true);
  });
});
