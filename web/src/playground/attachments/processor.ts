import { detectAttachmentKind } from './detect';
import { extractEml } from './eml';
import { extractMsg } from './msg';
import { processImageFile } from './image';
import { processVideoFile } from './video';
import {
  MAX_ATTACHMENTS_PER_MESSAGE,
  MAX_EMAIL_CHARS,
  MAX_EMAIL_RAW_BYTES,
  MAX_IMAGES_PER_MESSAGE,
  MAX_TOTAL_IMAGE_BINARY_BYTES,
  MAX_VIDEOS_PER_MESSAGE,
  MAX_PDF_CHARS,
  MAX_PDF_RAW_BYTES,
  MAX_SHEET_CHARS,
  MAX_SHEET_RAW_BYTES,
  MAX_TEXT_CHARS,
  MAX_TEXT_RAW_BYTES,
  MAX_TOTAL_EXTRACTED_CHARS,
  MAX_TOTAL_RAW_BYTES,
  MAX_WEBPAGE_CHARS,
  MAX_WEBPAGE_RAW_BYTES,
  mbLabel,
} from './limits';
import { extractPdf } from './pdf';
import { extractWorkbook } from './sheet';
import { extractPlainText } from './textExtract';
import { extractHtmlFile, extractMhtml } from './webpage';
import {
  AttachmentError,
  type AttachmentIssue,
  type AttachmentKind,
  type ExtractedText,
  type ProcessResult,
  type ProcessedFile,
} from './types';

type FileKind = Exclude<AttachmentKind, 'image' | 'video'>;

// 已挂在输入框上的附件占用的额度，新文件在其基础上累计。
export interface PendingSnapshot {
  imageCount: number;
  videoCount: number;
  attachmentCount: number;
  totalRawBytes: number;
  extractedChars: number;
  // 已有图片/视频的二进制字节合计（控制单条消息 base64 总载荷）
  imageBinaryBytes: number;
}

const FILE_KIND_LIMITS: Record<FileKind, { rawBytes: number; chars: number; defaultType: string }> = {
  pdf: { rawBytes: MAX_PDF_RAW_BYTES, chars: MAX_PDF_CHARS, defaultType: 'application/pdf' },
  sheet: { rawBytes: MAX_SHEET_RAW_BYTES, chars: MAX_SHEET_CHARS, defaultType: 'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet' },
  email: { rawBytes: MAX_EMAIL_RAW_BYTES, chars: MAX_EMAIL_CHARS, defaultType: 'message/rfc822' },
  webpage: { rawBytes: MAX_WEBPAGE_RAW_BYTES, chars: MAX_WEBPAGE_CHARS, defaultType: 'text/html' },
  text: { rawBytes: MAX_TEXT_RAW_BYTES, chars: MAX_TEXT_CHARS, defaultType: 'text/plain' },
};

let attachmentIdCounter = 0;

function nextAttachmentId(file: File): string {
  attachmentIdCounter += 1;
  return `${file.name}-${file.lastModified}-${file.size}-${attachmentIdCounter}`;
}

async function extractByKind(kind: FileKind, file: File, maxChars: number): Promise<ExtractedText> {
  const buffer = await file.arrayBuffer();
  switch (kind) {
    case 'pdf':
      return extractPdf(buffer, maxChars);
    case 'sheet':
      return extractWorkbook(buffer, maxChars);
    case 'email':
      // .msg（Outlook CFB）与 .eml（MIME）按扩展名/字节分派
      return /\.msg$/i.test(file.name) || file.type === 'application/vnd.ms-outlook'
        ? extractMsg(buffer, maxChars)
        : extractEml(buffer, maxChars);
    case 'webpage':
      return /\.(mht|mhtml)$/i.test(file.name) || file.type === 'multipart/related'
        ? extractMhtml(buffer, maxChars)
        : extractHtmlFile(buffer, maxChars);
    case 'text':
      return extractPlainText(buffer, maxChars);
  }
}

function toIssue(err: unknown): AttachmentIssue {
  if (err instanceof AttachmentError) return err.issue;
  return { code: 'attachment.parse_failed' };
}

// 统一附件入口：识别类型 → 校验额度 → 分发抽取。
// 逐文件失败不阻断整批，错误收集在 result.errors。
export async function processAttachments(files: File[], snapshot: PendingSnapshot): Promise<ProcessResult> {
  const result: ProcessResult = { images: [], files: [], errors: [] };
  let { imageCount, videoCount, attachmentCount, totalRawBytes, extractedChars, imageBinaryBytes } = snapshot;

  for (const file of files) {
    try {
      const kind = detectAttachmentKind(file);
      if (!kind) {
        throw new AttachmentError({ code: 'attachment.unsupported_type' });
      }
      if (attachmentCount + 1 > MAX_ATTACHMENTS_PER_MESSAGE) {
        throw new AttachmentError({ code: 'attachment.too_many', params: { limit: MAX_ATTACHMENTS_PER_MESSAGE } });
      }
      if (totalRawBytes + file.size > MAX_TOTAL_RAW_BYTES) {
        throw new AttachmentError({ code: 'attachment.total_too_large', params: { limit_mb: mbLabel(MAX_TOTAL_RAW_BYTES) } });
      }

      if (kind === 'image') {
        if (imageCount + 1 > MAX_IMAGES_PER_MESSAGE) {
          throw new AttachmentError({ code: 'attachment.too_many_images', params: { limit: MAX_IMAGES_PER_MESSAGE } });
        }
        const compressed = await processImageFile(file);
        // 压缩后仍要受单条消息图片总载荷预算约束（base64 后需低于后端 30MB 转发守卫）
        if (imageBinaryBytes + compressed.finalBytes > MAX_TOTAL_IMAGE_BINARY_BYTES) {
          throw new AttachmentError({
            code: 'attachment.image_total_too_large',
            params: { limit_mb: mbLabel(MAX_TOTAL_IMAGE_BINARY_BYTES) },
          });
        }
        result.images.push({
          id: nextAttachmentId(file),
          name: file.name || 'pasted-image',
          url: compressed.url,
          originalBytes: compressed.originalBytes,
          finalBytes: compressed.finalBytes,
          compressed: compressed.compressed,
          warnings: compressed.warnings,
        });
        imageCount += 1;
        attachmentCount += 1;
        totalRawBytes += file.size;
        imageBinaryBytes += compressed.finalBytes;
        continue;
      }

      if (kind === 'video') {
        if (videoCount + 1 > MAX_VIDEOS_PER_MESSAGE) {
          throw new AttachmentError({
            code: 'attachment.too_many_videos',
            params: { limit: MAX_VIDEOS_PER_MESSAGE, defaultValue: 'Only one video per message' },
          });
        }
        const video = await processVideoFile(file);
        if (imageBinaryBytes + video.bytes > MAX_TOTAL_IMAGE_BINARY_BYTES) {
          throw new AttachmentError({
            code: 'attachment.image_total_too_large',
            params: { limit_mb: mbLabel(MAX_TOTAL_IMAGE_BINARY_BYTES) },
          });
        }
        result.images.push({
          id: nextAttachmentId(file),
          name: file.name || 'video',
          url: video.url,
          originalBytes: file.size,
          finalBytes: video.bytes,
          compressed: false,
          mediaKind: 'video',
          warnings: video.warnings,
        });
        videoCount += 1;
        attachmentCount += 1;
        totalRawBytes += file.size;
        imageBinaryBytes += video.bytes;
        continue;
      }

      const limits = FILE_KIND_LIMITS[kind];
      if (file.size > limits.rawBytes) {
        throw new AttachmentError({ code: 'attachment.file_too_large', params: { limit_mb: mbLabel(limits.rawBytes) } });
      }
      const remainingChars = MAX_TOTAL_EXTRACTED_CHARS - extractedChars;
      if (remainingChars <= 0) {
        throw new AttachmentError({ code: 'attachment.extract_budget_exhausted' });
      }

      const extracted = await extractByKind(kind, file, Math.min(limits.chars, remainingChars));
      if (!extracted.content.trim() && kind !== 'sheet') {
        throw new AttachmentError({ code: 'attachment.empty_content' });
      }

      const processed: ProcessedFile = {
        id: nextAttachmentId(file),
        name: file.name || 'attachment',
        type: file.type || limits.defaultType,
        size: file.size,
        origin: kind,
        content: extracted.content,
        truncated: extracted.truncated,
        warnings: extracted.warnings,
      };
      result.files.push(processed);
      attachmentCount += 1;
      totalRawBytes += file.size;
      extractedChars += extracted.content.length;
    } catch (err) {
      result.errors.push({ name: file.name || 'attachment', issue: toIssue(err) });
    }
  }

  return result;
}
