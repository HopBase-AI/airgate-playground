import { describe, expect, it } from 'vitest';
import { htmlToText } from '../htmlToText';
import { extractHtmlFile, extractMhtml } from '../webpage';

function buf(raw: string): ArrayBuffer {
  return new TextEncoder().encode(raw).buffer;
}

describe('htmlToText', () => {
  it('strips script/style and keeps block structure', () => {
    const { title, text } = htmlToText(
      '<html><head><title>Doc Title</title><style>body{}</style></head>'
      + '<body><script>alert(1)</script><h1>Heading</h1><p>para one</p><div>para two</div></body></html>',
    );
    expect(title).toBe('Doc Title');
    expect(text).toContain('Heading');
    expect(text).toContain('para one');
    expect(text).toContain('para two');
    expect(text).not.toContain('alert(1)');
    expect(text).not.toContain('body{}');
  });

  it('keeps separating whitespace between inline elements', () => {
    const { text } = htmlToText('<body><p><span>Total:</span> <span>42</span> and <a href="#">link</a></p></body>');
    expect(text).toContain('Total: 42 and link');
  });

  it('skips hidden elements and renders table cells', () => {
    const { text } = htmlToText(
      '<body><div style="display:none">secret</div><table><tr><td>a</td><td>b</td></tr></table></body>',
    );
    expect(text).not.toContain('secret');
    expect(text).toContain('a | b');
  });
});

describe('extractHtmlFile', () => {
  it('produces title and body text', () => {
    const result = extractHtmlFile(buf('<html><head><title>T</title></head><body><p>hello</p></body></html>'), 1000);
    expect(result.content).toContain('Title: T');
    expect(result.content).toContain('hello');
  });

  it('warns when no body text', () => {
    const result = extractHtmlFile(buf('<html><body><script>x()</script></body></html>'), 1000);
    expect(result.warnings.some(w => w.code === 'attachment.webpage_empty')).toBe(true);
  });
});

describe('extractMhtml', () => {
  it('extracts the html part from multipart/related', () => {
    const raw = [
      'From: <Saved by Blink>',
      'Subject: page',
      'MIME-Version: 1.0',
      'Content-Type: multipart/related; type="text/html"; boundary="----MultipartBoundary--abc"',
      '',
      '------MultipartBoundary--abc',
      'Content-Type: text/html; charset=utf-8',
      'Content-Transfer-Encoding: quoted-printable',
      'Content-Location: https://example.com/',
      '',
      '<html><head><title>Saved Page</title></head><body><p>saved=20content</p></body></html>',
      '------MultipartBoundary--abc',
      'Content-Type: image/png',
      'Content-Transfer-Encoding: base64',
      'Content-Location: https://example.com/logo.png',
      '',
      'iVBORw0KGgo=',
      '------MultipartBoundary--abc--',
    ].join('\r\n');

    const result = extractMhtml(buf(raw), 10_000);
    expect(result.content).toContain('Title: Saved Page');
    expect(result.content).toContain('saved content');
    expect(result.content).not.toContain('iVBORw0KGgo');
  });
});
