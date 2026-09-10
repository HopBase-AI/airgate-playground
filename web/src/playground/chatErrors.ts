// ── 网关错误文案本地化 ───────────────────────────────────────────────────────
// 2026-09-10：一位西语客户收到了中文网关报错。后端 /chat/completions 的错误体一律英文
// （OpenAI 兼容端点，外部 API 调用方拿到的就是英文），但插件自己的前端拿得到 error.code，
// 因此在这里按界面语言本地化，取不到 code 时原样回落后端英文文案。
// 五语字典自带（不动 core 的 i18n 资源文件），与 studio 的 VIDEO_STRINGS 同款做法。
//
// ⚠️ 只认 hopbase_ 命名空间的码，且只放**固定兜底句**。两条红线：
//  1. 上游错误体是原样透传的（routes.go 的 res.failBody 分支）。OpenAI 兼容上游用裸码
//     insufficient_quota 表示**上游账号**欠费、invalid_request_error 表示上游参数问题；
//     若按裸码本地化，会把上游计费故障显示成「你的 HopBase 余额不足，请充值」，定责定反。
//     裸码不进字典 → 透传体永远命不中 → 原文照显。
//  2. 带具体原因的错误（core 的 InvalidArgument reason、参数校验的 err.Error()、
//     conversation not found 等）走原码 invalid_request，同样**不进字典**——
//     否则具体信息会被通用兜底句盖掉，英文用户也一样受损。

export const CHAT_ERROR_STRINGS = {
  en: {
    hopbase_upstream_unavailable: 'The request could not be completed. Please try again later.',
    hopbase_invalid_request: 'The request could not be completed. Check the request parameters and try again.',
    hopbase_insufficient_balance: 'Insufficient balance.',
    hopbase_member_group_forbidden: 'Your organization administrator has not granted access to this model. Contact your administrator or pick another model.',
    hopbase_request_too_large: 'Conversation payload too large: history images and attachments exceed 30MB after expansion. Remove some images or start a new conversation.',
  },
  zh: {
    hopbase_upstream_unavailable: '请求暂时无法完成，请稍后重试。',
    hopbase_invalid_request: '请求无法完成，请检查输入后重试。',
    hopbase_insufficient_balance: '余额不足。',
    hopbase_member_group_forbidden: '企业管理员未授予该模型的使用权限，请联系企业管理员或换一个模型。',
    hopbase_request_too_large: '会话内容过大：历史图片与附件展开后超过 30MB。请减少图片数量或新建会话后重试。',
  },
  'zh-HK': {
    hopbase_upstream_unavailable: '請求暫時無法完成，請稍後重試。',
    hopbase_invalid_request: '請求無法完成，請檢查輸入後重試。',
    hopbase_insufficient_balance: '餘額不足。',
    hopbase_member_group_forbidden: '企業管理員未授予該模型的使用權限，請聯絡企業管理員或改用其他模型。',
    hopbase_request_too_large: '對話內容過大：歷史圖片與附件展開後超過 30MB。請減少圖片數量或另開新對話後重試。',
  },
  ja: {
    hopbase_upstream_unavailable: 'リクエストを完了できませんでした。しばらくしてから再試行してください。',
    hopbase_invalid_request: 'リクエストを完了できませんでした。入力内容を確認してから再試行してください。',
    hopbase_insufficient_balance: '残高が不足しています。',
    hopbase_member_group_forbidden: 'このモデルの利用権限が組織管理者から付与されていません。管理者に連絡するか、別のモデルをお選びください。',
    hopbase_request_too_large: '会話の内容が大きすぎます。履歴の画像と添付ファイルを展開すると 30MB を超えます。画像を減らすか、新しい会話を開始してください。',
  },
  es: {
    hopbase_upstream_unavailable: 'No se pudo completar la solicitud. Inténtelo de nuevo más tarde.',
    hopbase_invalid_request: 'No se pudo completar la solicitud. Revise los parámetros e inténtelo de nuevo.',
    hopbase_insufficient_balance: 'Saldo insuficiente.',
    hopbase_member_group_forbidden: 'El administrador de su organización no le ha concedido acceso a este modelo. Contacte con el administrador o elija otro modelo.',
    hopbase_request_too_large: 'El contenido de la conversación es demasiado grande: las imágenes del historial y los adjuntos superan 30 MB al expandirse. Reduzca el número de imágenes o inicie una conversación nueva.',
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

/**
 * localizeChatError 按 error.code 取本地化文案。
 * 只有本插件自产的 hopbase_ 兜底码会被替换；其它码（透传的上游码、带具体原因的
 * invalid_request 等）一律原样返回服务端文案。
 */
export function localizeChatError(lang: string, code: string | undefined, fallback: string): string {
  if (!code || !code.startsWith('hopbase_')) return fallback;
  const dict = CHAT_ERROR_STRINGS[pickChatErrorLang(lang)];
  const localized = (dict as Record<string, string | undefined>)[code];
  return localized || fallback;
}
