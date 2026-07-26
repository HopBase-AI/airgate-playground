import {
  IMAGE_COMPRESSION_LADDER,
  IMAGE_PASSTHROUGH_MAX_EDGE,
  IMAGE_TARGET_BYTES,
  MAX_IMAGE_PIXELS,
  MAX_IMAGE_RAW_BYTES,
  MODEL_FRIENDLY_IMAGE_TYPES,
  mbLabel,
} from './limits';
import { AttachmentError, type AttachmentIssue } from './types';

export interface ImageInfo {
  sizeBytes: number;
  width: number;
  height: number;
  type: string;
}

// 压缩决策（纯函数，便于单测）：已达标的小图（含合规 GIF，保留动画）原样发送，
// 避免截图文字被二次 JPEG 压糊；尺寸/像素校验由调用方在 decode 后统一做。
export function shouldPassthrough(info: ImageInfo): boolean {
  if (info.sizeBytes > IMAGE_TARGET_BYTES) return false;
  if (Math.max(info.width, info.height) > IMAGE_PASSTHROUGH_MAX_EDGE) return false;
  if (!MODEL_FRIENDLY_IMAGE_TYPES.has(info.type)) return false;
  return true;
}

// 对给定尺寸生成实际生效的压缩阶梯（目标边不放大原图）。
export function effectiveLadder(width: number, height: number): Array<{ maxEdge: number; quality: number }> {
  const longEdge = Math.max(width, height);
  return IMAGE_COMPRESSION_LADDER.map(step => ({
    maxEdge: Math.min(step.maxEdge, longEdge),
    quality: step.quality,
  }));
}

export function scaledDimensions(width: number, height: number, maxEdge: number): { width: number; height: number } {
  const longEdge = Math.max(width, height);
  if (longEdge <= maxEdge) return { width, height };
  const scale = maxEdge / longEdge;
  return {
    width: Math.max(1, Math.round(width * scale)),
    height: Math.max(1, Math.round(height * scale)),
  };
}

// 按魔数嗅探真实图片类型（纯函数可测）。浏览器 file.type 只看扩展名，
// 微信导出的 .jpg 常是 PNG 字节，错标 media_type 会被严格上游拒绝。
export function sniffImageBytes(head: Uint8Array): string | null {
  if (head.length < 12) return null;
  if (head[0] === 0x89 && head[1] === 0x50 && head[2] === 0x4e && head[3] === 0x47) return 'image/png';
  if (head[0] === 0xff && head[1] === 0xd8 && head[2] === 0xff) return 'image/jpeg';
  if (head[0] === 0x47 && head[1] === 0x49 && head[2] === 0x46 && head[3] === 0x38) return 'image/gif';
  const ascii = (start: number, end: number) => String.fromCharCode(...head.slice(start, end));
  if (ascii(0, 4) === 'RIFF' && ascii(8, 12) === 'WEBP') return 'image/webp';
  return null;
}

async function sniffImageFileType(file: File): Promise<string> {
  try {
    const head = new Uint8Array(await file.slice(0, 16).arrayBuffer());
    return sniffImageBytes(head) || file.type;
  } catch {
    return file.type;
  }
}

// ── 浏览器管线 ──

type DecodedImage = {
  source: CanvasImageSource;
  width: number;
  height: number;
  close: () => void;
};

async function decodeImage(file: File): Promise<DecodedImage> {
  if (typeof createImageBitmap === 'function') {
    try {
      // from-image：应用 EXIF 方向（手机照片必需）
      const bitmap = await createImageBitmap(file, { imageOrientation: 'from-image' });
      return { source: bitmap, width: bitmap.width, height: bitmap.height, close: () => bitmap.close() };
    } catch {
      // 某些浏览器不支持 options 或该格式，走 <img> 回退
    }
  }
  const url = URL.createObjectURL(file);
  try {
    const img = new Image();
    img.src = url;
    await img.decode();
    return {
      source: img,
      width: img.naturalWidth,
      height: img.naturalHeight,
      close: () => URL.revokeObjectURL(url),
    };
  } catch {
    URL.revokeObjectURL(url);
    throw new AttachmentError({ code: 'attachment.image_decode_failed' });
  }
}

function createCanvas(width: number, height: number): OffscreenCanvas | HTMLCanvasElement {
  if (typeof OffscreenCanvas === 'function') return new OffscreenCanvas(width, height);
  const canvas = document.createElement('canvas');
  canvas.width = width;
  canvas.height = height;
  return canvas;
}

async function canvasToJpegBlob(canvas: OffscreenCanvas | HTMLCanvasElement, quality: number): Promise<Blob> {
  if ('convertToBlob' in canvas) {
    return canvas.convertToBlob({ type: 'image/jpeg', quality });
  }
  return new Promise<Blob>((resolve, reject) => {
    canvas.toBlob(blob => {
      if (blob) resolve(blob);
      else reject(new AttachmentError({ code: 'attachment.image_compress_failed' }));
    }, 'image/jpeg', quality);
  });
}

async function renderToJpeg(decoded: DecodedImage, maxEdge: number, quality: number): Promise<Blob> {
  const { width, height } = scaledDimensions(decoded.width, decoded.height, maxEdge);
  const canvas = createCanvas(width, height);
  const ctx = canvas.getContext('2d');
  if (!ctx) throw new AttachmentError({ code: 'attachment.image_compress_failed' });
  // JPEG 无透明通道：先铺白底，避免透明 PNG 变黑底
  ctx.fillStyle = '#ffffff';
  ctx.fillRect(0, 0, width, height);
  ctx.drawImage(decoded.source, 0, 0, width, height);
  return canvasToJpegBlob(canvas, quality);
}

export async function blobToDataURL(blob: Blob): Promise<string> {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result || ''));
    reader.onerror = () => reject(reader.error || new Error('Failed to read blob'));
    reader.readAsDataURL(blob);
  });
}

export interface CompressedImageResult {
  url: string;
  originalBytes: number;
  finalBytes: number;
  compressed: boolean;
  warnings: AttachmentIssue[];
}

export async function processImageFile(file: File): Promise<CompressedImageResult> {
  if (file.size > MAX_IMAGE_RAW_BYTES) {
    throw new AttachmentError({
      code: 'attachment.image_too_large',
      params: { limit_mb: mbLabel(MAX_IMAGE_RAW_BYTES) },
    });
  }

  const warnings: AttachmentIssue[] = [];
  // 以字节嗅探的真实类型为准，file.type（扩展名推断）只作回退
  const actualType = await sniffImageFileType(file);

  const decoded = await decodeImage(file);
  try {
    if (decoded.width * decoded.height > MAX_IMAGE_PIXELS) {
      throw new AttachmentError({
        code: 'attachment.image_pixels_exceeded',
        params: { limit_mp: Math.round(MAX_IMAGE_PIXELS / 1_000_000) },
      });
    }

    if (shouldPassthrough({ sizeBytes: file.size, width: decoded.width, height: decoded.height, type: actualType })) {
      const source = actualType === file.type ? file : new Blob([file], { type: actualType });
      return {
        url: await blobToDataURL(source),
        originalBytes: file.size,
        finalBytes: file.size,
        compressed: false,
        warnings,
      };
    }

    if (actualType === 'image/gif') {
      warnings.push({ code: 'attachment.gif_flattened' });
    }

    for (const step of effectiveLadder(decoded.width, decoded.height)) {
      const blob = await renderToJpeg(decoded, step.maxEdge, step.quality);
      if (blob.size <= IMAGE_TARGET_BYTES) {
        return {
          url: await blobToDataURL(blob),
          originalBytes: file.size,
          finalBytes: blob.size,
          compressed: true,
          warnings,
        };
      }
    }
    throw new AttachmentError({ code: 'attachment.image_compress_failed' });
  } finally {
    decoded.close();
  }
}
