import { MAX_VIDEO_RAW_BYTES, VIDEO_CONTENT_TYPE_BY_EXT, mbLabel } from './limits';
import { AttachmentError, type AttachmentIssue } from './types';

export interface ProcessedVideo {
  url: string; // data:video/*;base64,...
  bytes: number;
  warnings: AttachmentIssue[];
}

// 浏览器对 .mov/.m4v 常给空 type，按扩展名兜底成明确的 video/* mime，
// 保证 data URL 前缀可被网关与上游按 inline 视频消费。
export function videoContentType(file: File): string {
  const type = (file.type || '').toLowerCase();
  if (type.startsWith('video/')) return type;
  for (const [re, mime] of VIDEO_CONTENT_TYPE_BY_EXT) {
    if (re.test(file.name)) return mime;
  }
  return 'video/mp4';
}

function arrayBufferToBase64(buffer: ArrayBuffer): string {
  const bytes = new Uint8Array(buffer);
  let binary = '';
  const chunk = 0x8000;
  for (let i = 0; i < bytes.length; i += chunk) {
    binary += String.fromCharCode(...bytes.subarray(i, i + chunk));
  }
  return btoa(binary);
}

// 视频不做压缩（浏览器端转码不现实），只校验大小并原样转 data URL。
export async function processVideoFile(file: File): Promise<ProcessedVideo> {
  if (file.size > MAX_VIDEO_RAW_BYTES) {
    throw new AttachmentError({
      code: 'attachment.file_too_large',
      params: { limit_mb: mbLabel(MAX_VIDEO_RAW_BYTES) },
    });
  }
  const buffer = await file.arrayBuffer();
  const url = `data:${videoContentType(file)};base64,${arrayBufferToBase64(buffer)}`;
  return {
    url,
    bytes: file.size,
    warnings: [{
      code: 'attachment.video_gemini_only',
      params: { defaultValue: 'Video input is only supported by Gemini-series models' },
    }],
  };
}
