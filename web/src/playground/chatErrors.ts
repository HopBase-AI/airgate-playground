// ── 网关错误文案本地化 ───────────────────────────────────────────────────────
// 2026-09-10：一位西语客户收到了中文网关报错。后端 /chat/completions 的错误体一律英文
// （OpenAI 兼容端点，外部 API 调用方拿到的就是英文），但插件自己的前端拿得到 error.code，
// 因此在这里按界面语言本地化，取不到 code 时原样回落后端英文文案。
// 五语字典自带（不动 core 的 i18n 资源文件），与 studio 的 VIDEO_STRINGS 同款做法。

export const CHAT_ERROR_STRINGS = {
  en: {
    upstream_error: 'The request could not be completed. Please try again later.',
    invalid_request: 'The request could not be completed. Check the request parameters and try again.',
    insufficient_quota: 'Insufficient balance.',
    member_group_forbidden: 'Your organization administrator has not granted access to this model. Contact your administrator or pick another model.',
  },
  zh: {
    upstream_error: '请求暂时无法完成，请稍后重试。',
    invalid_request: '请求无法完成，请检查输入后重试。',
    insufficient_quota: '余额不足。',
    member_group_forbidden: '企业管理员未授予该模型的使用权限，请联系企业管理员或换一个模型。',
  },
  'zh-HK': {
    upstream_error: '請求暫時無法完成，請稍後重試。',
    invalid_request: '請求無法完成，請檢查輸入後重試。',
    insufficient_quota: '餘額不足。',
    member_group_forbidden: '企業管理員未授予該模型的使用權限，請聯絡企業管理員或改用其他模型。',
  },
  ja: {
    upstream_error: 'リクエストを完了できませんでした。しばらくしてから再試行してください。',
    invalid_request: 'リクエストを完了できませんでした。入力内容を確認してから再試行してください。',
    insufficient_quota: '残高が不足しています。',
    member_group_forbidden: 'このモデルの利用権限が組織管理者から付与されていません。管理者に連絡するか、別のモデルをお選びください。',
  },
  es: {
    upstream_error: 'No se pudo completar la solicitud. Inténtelo de nuevo más tarde.',
    invalid_request: 'No se pudo completar la solicitud. Revise los parámetros e inténtelo de nuevo.',
    insufficient_quota: 'Saldo insuficiente.',
    member_group_forbidden: 'El administrador de su organización no le ha concedido acceso a este modelo. Contacte con el administrador o elija otro modelo.',
  },
} as const;

export type ChatErrorLang = keyof typeof CHAT_ERROR_STRINGS;
export type ChatErrorCode = keyof typeof CHAT_ERROR_STRINGS['en'];

export function pickChatErrorLang(lang: string): ChatErrorLang {
  const normalized = (lang || 'en').toLowerCase();
  if (normalized.startsWith('zh')) {
    return normalized.includes('hk') || normalized.includes('hant') || normalized.includes('tw') ? 'zh-HK' : 'zh';
  }
  if (normalized.startsWith('ja')) return 'ja';
  if (normalized.startsWith('es')) return 'es';
  return 'en';
}

/** localizeChatError 按 error.code 取本地化文案；未知 code 原样返回后端英文文案。 */
export function localizeChatError(lang: string, code: string | undefined, fallback: string): string {
  const dict = CHAT_ERROR_STRINGS[pickChatErrorLang(lang)];
  const localized = code ? (dict as Record<string, string | undefined>)[code] : undefined;
  return localized || fallback;
}
