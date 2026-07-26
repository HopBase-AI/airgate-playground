import { describe, expect, it } from 'vitest';
import { extractEml } from '../eml';
import { decodeEncodedWords } from '../mime';

function emlBuffer(raw: string): ArrayBuffer {
  return new TextEncoder().encode(raw).buffer;
}

describe('decodeEncodedWords', () => {
  it('decodes base64 utf-8 encoded words', () => {
    // "=?utf-8?B?5Lit5paH?=" → 中文
    expect(decodeEncodedWords('=?utf-8?B?5Lit5paH?= subject')).toBe('中文 subject');
  });

  it('decodes quoted-printable encoded words with underscores as spaces', () => {
    expect(decodeEncodedWords('=?utf-8?Q?hello=20world?=')).toBe('hello world');
    expect(decodeEncodedWords('=?utf-8?Q?a_b?=')).toBe('a b');
  });

  it('joins adjacent encoded words without the folding whitespace', () => {
    // 长中文主题被拆成多个 encoded-word，词间空白按 RFC 2047 丢弃
    expect(decodeEncodedWords('=?utf-8?B?5Lit?= =?utf-8?B?5paH?=')).toBe('中文');
    expect(decodeEncodedWords('=?utf-8?B?5Lit?=\r\n =?utf-8?B?5paH?=')).toBe('中文');
  });
});

describe('extractEml', () => {
  it('extracts headers and plain text body', () => {
    const raw = [
      'From: alice@example.com',
      'To: bob@example.com',
      'Subject: Quarterly report',
      'Date: Tue, 1 Jul 2026 10:00:00 +0800',
      'Content-Type: text/plain; charset=utf-8',
      '',
      'Hello Bob,',
      'Numbers attached below.',
    ].join('\r\n');

    const result = extractEml(emlBuffer(raw), 10_000);
    expect(result.content).toContain('From: alice@example.com');
    expect(result.content).toContain('Subject: Quarterly report');
    expect(result.content).toContain('Hello Bob,');
    expect(result.truncated).toBe(false);
  });

  it('prefers text/plain in multipart and lists attachments', () => {
    const raw = [
      'From: a@x.com',
      'Subject: multi',
      'Content-Type: multipart/mixed; boundary="BOUND"',
      '',
      '--BOUND',
      'Content-Type: text/plain; charset=utf-8',
      '',
      'plain body here',
      '--BOUND',
      'Content-Type: text/html; charset=utf-8',
      '',
      '<p>html body</p>',
      '--BOUND',
      'Content-Type: application/pdf; name="report.pdf"',
      'Content-Disposition: attachment; filename="report.pdf"',
      'Content-Transfer-Encoding: base64',
      '',
      'JVBERi0=',
      '--BOUND--',
    ].join('\r\n');

    const result = extractEml(emlBuffer(raw), 10_000);
    expect(result.content).toContain('plain body here');
    expect(result.content).not.toContain('html body');
    expect(result.content).toContain('report.pdf');
  });

  it('falls back to html body converted to text', () => {
    const raw = [
      'From: a@x.com',
      'Subject: htmlmail',
      'Content-Type: text/html; charset=utf-8',
      'Content-Transfer-Encoding: quoted-printable',
      '',
      '<html><body><style>p{color:red}</style><p>line one</p><p>line=20two</p></body></html>',
    ].join('\r\n');

    const result = extractEml(emlBuffer(raw), 10_000);
    expect(result.content).toContain('line one');
    expect(result.content).toContain('line two');
    expect(result.content).not.toContain('color:red');
  });

  it('falls back to gbk for charset-less chinese bodies', () => {
    const headers = new TextEncoder().encode([
      'From: a@x.com',
      'Subject: gbk',
      'Content-Type: text/plain',
      '',
      '',
    ].join('\r\n'));
    // "中文" 的 GBK 编码：D6 D0 CE C4
    const body = new Uint8Array([0xd6, 0xd0, 0xce, 0xc4]);
    const raw = new Uint8Array(headers.length + body.length);
    raw.set(headers, 0);
    raw.set(body, headers.length);

    const result = extractEml(raw.buffer, 10_000);
    expect(result.content).toContain('中文');
  });

  it('decodes encoded-word subject', () => {
    const raw = [
      'From: a@x.com',
      'Subject: =?utf-8?B?5a2j5bqm6LSi5oql?=',
      'Content-Type: text/plain',
      '',
      'body',
    ].join('\r\n');

    const result = extractEml(emlBuffer(raw), 10_000);
    expect(result.content).toContain('Subject: 季度财报');
  });
});
