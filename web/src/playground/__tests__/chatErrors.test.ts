import { describe, expect, it } from 'vitest';
import { CHAT_ERROR_STRINGS, localizeChatError, pickChatErrorLang } from '../chatErrors';

describe('chat error localization', () => {
  it('maps interface language onto the five bundled dictionaries', () => {
    expect(pickChatErrorLang('zh-CN')).toBe('zh');
    expect(pickChatErrorLang('zh-HK')).toBe('zh-HK');
    expect(pickChatErrorLang('zh-Hant')).toBe('zh-HK');
    expect(pickChatErrorLang('ja-JP')).toBe('ja');
    expect(pickChatErrorLang('es-ES')).toBe('es');
    expect(pickChatErrorLang('en-US')).toBe('en');
    expect(pickChatErrorLang('')).toBe('en');
  });

  it('localizes known gateway error codes', () => {
    expect(localizeChatError('es', 'insufficient_quota', 'Insufficient balance.')).toBe('Saldo insuficiente.');
    expect(localizeChatError('zh', 'upstream_error', 'x')).toBe('请求暂时无法完成，请稍后重试。');
  });

  it('falls back to the backend English message when the code is unknown or absent', () => {
    expect(localizeChatError('es', undefined, 'HTTP 500')).toBe('HTTP 500');
    expect(localizeChatError('es', 'brand_new_code', 'HTTP 500')).toBe('HTTP 500');
  });

  it('keeps every dictionary in sync with the English key set', () => {
    const keys = Object.keys(CHAT_ERROR_STRINGS.en).sort();
    for (const [lang, dict] of Object.entries(CHAT_ERROR_STRINGS)) {
      expect(Object.keys(dict).sort(), lang).toEqual(keys);
    }
  });
});
