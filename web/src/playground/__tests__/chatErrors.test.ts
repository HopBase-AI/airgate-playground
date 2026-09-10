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

  it('localizes the plugin-generated fallback codes', () => {
    expect(localizeChatError('es', 'hopbase_insufficient_balance', 'Insufficient balance.')).toBe('Saldo insuficiente.');
    expect(localizeChatError('zh', 'hopbase_upstream_unavailable', 'x')).toBe('请求暂时无法完成，请稍后重试。');
    expect(localizeChatError('ja', 'hopbase_request_too_large', 'x')).toContain('30MB');
  });

  it('keeps the member-group-forbidden message specific instead of collapsing it to the generic one', () => {
    const generic = CHAT_ERROR_STRINGS.es.hopbase_upstream_unavailable;
    const specific = localizeChatError('es', 'hopbase_member_group_forbidden', 'backend english');
    expect(specific).not.toBe(generic);
    expect(specific).toContain('administrador');
  });

  it('never localizes proxied upstream codes (an upstream quota failure is not the customer balance)', () => {
    // OpenAI 兼容上游用裸码表示上游账号问题，错误体是原样透传的，不能被我们的字典改写。
    const upstream = 'You exceeded your current quota, please check your plan and billing details.';
    expect(localizeChatError('es', 'insufficient_quota', upstream)).toBe(upstream);
    expect(localizeChatError('zh', 'invalid_request_error', 'upstream said: bad tool schema')).toBe('upstream said: bad tool schema');
    expect(localizeChatError('ja', 'context_length_exceeded', 'too long')).toBe('too long');
  });

  it('keeps detailed invalid_request messages verbatim', () => {
    expect(localizeChatError('es', 'invalid_request', 'model required')).toBe('model required');
    expect(localizeChatError('es', 'invalid_request', 'conversation not found')).toBe('conversation not found');
  });

  it('falls back to the backend message when the code is unknown or absent', () => {
    expect(localizeChatError('es', undefined, 'HTTP 500')).toBe('HTTP 500');
    expect(localizeChatError('es', 'hopbase_brand_new_code', 'HTTP 500')).toBe('HTTP 500');
  });

  it('keeps every dictionary in sync with the English key set, and every key namespaced', () => {
    const keys = Object.keys(CHAT_ERROR_STRINGS.en).sort();
    expect(keys.every(k => k.startsWith('hopbase_'))).toBe(true);
    for (const [lang, dict] of Object.entries(CHAT_ERROR_STRINGS)) {
      expect(Object.keys(dict).sort(), lang).toEqual(keys);
    }
  });
});
