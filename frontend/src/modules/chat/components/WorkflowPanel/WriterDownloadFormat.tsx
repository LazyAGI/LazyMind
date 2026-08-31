import { useCallback, useEffect, useMemo, useState } from 'react';
import { Button, Modal } from 'antd';
import {
  CodeOutlined,
  DownloadOutlined,
  FileTextOutlined,
} from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { axiosInstance } from '@/components/request';
import { coreApiUrl } from '@/runtime/apiBase';
import './WriterDownloadFormat.scss';

export type WriterDownloadFormat = 'markdown' | 'lmd';
export type WriterDownloadSourceFormat = WriterDownloadFormat | 'writer_document';

function markdownTitleText(value: string): string {
  return value
    .replace(/!\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/\\([\\`*_[\]{}#+.!|>-])/g, '$1')
    .replace(/<[^>]+>/g, '')
    .replace(/[*_~`]/g, '')
    .trim();
}

export function writerMarkdownTitle(markdown: string): string {
  const lines = markdown.replace(/\r\n?/g, '\n').split('\n');
  let fence: { marker: string; length: number } | null = null;
  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    const fenceMatch = /^ {0,3}(`{3,}|~{3,})/.exec(line);
    if (fenceMatch) {
      const marker = fenceMatch[1][0];
      if (!fence) fence = { marker, length: fenceMatch[1].length };
      else if (fence.marker === marker && fenceMatch[1].length >= fence.length) fence = null;
      continue;
    }
    if (fence) continue;

    const atx = /^ {0,3}#(?!#)[ \t]+(.+?)[ \t]*#*[ \t]*$/.exec(line);
    if (atx) return markdownTitleText(atx[1]);
    if (
      line.trim()
      && index + 1 < lines.length
      && /^ {0,3}=+[ \t]*$/.test(lines[index + 1])
    ) {
      return markdownTitleText(line.trim());
    }
  }
  return '';
}

function writerFilenameStem(value: string): string {
  const withoutExtension = value.trim()
    .replace(/_ir(?=\.(?:lmd|json)$)/i, '')
    .replace(/\.(?:md|markdown|lmd|json)$/i, '');
  const sanitized = withoutExtension
    .normalize('NFC')
    .replace(/[<>:"/\\|?*\u0000-\u001f]/g, '-')
    .replace(/\s+/g, ' ')
    .replace(/-+/g, '-')
    .replace(/^[ .-]+|[ .-]+$/g, '');
  const shortened = Array.from(sanitized).slice(0, 120).join('').replace(/[ .]+$/g, '');
  if (/^(?:con|prn|aux|nul|com[1-9]|lpt[1-9])$/i.test(shortened)) return `_${shortened}`;
  return shortened;
}

export function writerDownloadFilename(
  title: string,
  extension: 'md' | 'lmd',
  fallback = 'document',
): string {
  const basename = writerFilenameStem(title) || writerFilenameStem(fallback) || 'document';
  return `${basename}.${extension}`;
}

export interface WriterDownloadSource {
  filename: string;
  href?: string;
  content?: string | (() => string | Promise<string>);
  /** Stable identity for converted content that should be reused across downloads. */
  cacheKey?: string;
  /** Canonical source used to persist a derived conversion across page loads. */
  conversionSource?: string;
  /** Format of conversionSource as understood by the LazyLLM Writer converter. */
  conversionSourceFormat?: WriterDownloadSourceFormat;
  mimeType?: string;
}

interface WriterDownloadFormatDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  markdown: WriterDownloadSource;
  lmd: WriterDownloadSource;
}

interface WriterDownloadFormatButtonProps {
  disabled?: boolean;
  className?: string;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  markdown: WriterDownloadSource;
  lmd: WriterDownloadSource;
}

const preparedFileCache = new Map<string, Promise<Blob>>();
const WRITER_DOWNLOAD_CONVERSION_VERSION = 'lazyllm-writer-conversion-v1';

function hashText(value: string): string {
  let hash = 2166136261;
  for (let index = 0; index < value.length; index += 1) {
    hash ^= value.charCodeAt(index);
    hash = Math.imul(hash, 16777619);
  }
  return (hash >>> 0).toString(16);
}

export function writerDownloadCacheKey(scope: string, content: string): string {
  return `${scope}:${content.length}:${hashText(content)}`;
}

function sourceCacheKey(source: WriterDownloadSource): string | undefined {
  if (source.cacheKey) return source.cacheKey;
  if (typeof source.content === 'string') {
    return `${source.filename}:${hashText(source.content)}`;
  }
  return undefined;
}

export async function writerDownloadSourceHash(
  source: string,
  sourceFormat = '',
): Promise<string | undefined> {
  const subtle = globalThis.crypto?.subtle;
  if (!subtle) return undefined;
  const bytes = new TextEncoder().encode(
    `${WRITER_DOWNLOAD_CONVERSION_VERSION}\0${sourceFormat}\0${source}`,
  );
  const digest = await subtle.digest('SHA-256', bytes);
  return Array.from(new Uint8Array(digest), (byte) => byte.toString(16).padStart(2, '0')).join('');
}

function persistentConversionUrl(sourceHash: string, format: WriterDownloadFormat): string {
  return coreApiUrl(
    `writer-download-conversions/${encodeURIComponent(sourceHash)}/${encodeURIComponent(format)}`,
  );
}

async function loadPersistentConversion(
  sourceHash: string,
  format: WriterDownloadFormat,
): Promise<Blob | undefined> {
  try {
    const response = await axiosInstance.get<Blob>(persistentConversionUrl(sourceHash, format), {
      responseType: 'blob',
      silentError: true,
    } as never);
    return response.data;
  } catch (error) {
    const status = (error as { response?: { status?: number } })?.response?.status;
    if (status !== 404) {
      console.warn('Failed to read persisted Writer download conversion', error);
    }
    return undefined;
  }
}

async function savePersistentConversion(
  sourceHash: string,
  format: WriterDownloadFormat,
  source: WriterDownloadSource,
  blob: Blob,
): Promise<void> {
  try {
    await axiosInstance.put(
      persistentConversionUrl(sourceHash, format),
      blob,
      {
        params: { filename: source.filename },
        headers: { 'Content-Type': source.mimeType ?? blob.type },
        silentError: true,
      } as never,
    );
  } catch (error) {
    // A cache outage must not prevent the user from receiving the converted file.
    console.warn('Failed to persist Writer download conversion', error);
  }
}

async function convertWriterDownload(
  source: WriterDownloadSource,
  format: WriterDownloadFormat,
): Promise<Blob> {
  if (source.conversionSource === undefined || !source.conversionSourceFormat) {
    throw new Error('download conversion source is incomplete');
  }
  const response = await axiosInstance.post<Blob>(
    coreApiUrl('writer-download-conversions:convert'),
    {
      source_format: source.conversionSourceFormat,
      target_format: format,
      content: source.conversionSource,
      document_id: writerFilenameStem(source.filename) || 'writer-document',
    },
    {
      responseType: 'blob',
      silentError: true,
    } as never,
  );
  return response.data;
}

export function clearWriterDownloadMemoryCache(): void {
  preparedFileCache.clear();
}

export async function prepareWriterDownloadBlob(
  source: WriterDownloadSource,
  format: WriterDownloadFormat,
): Promise<Blob> {
  const key = sourceCacheKey(source);
  if (key) {
    const cached = preparedFileCache.get(key);
    if (cached) return cached;
  }

  const pending = (async () => {
    const sourceHash = source.conversionSource === undefined
      ? undefined
      : await writerDownloadSourceHash(source.conversionSource, source.conversionSourceFormat);
    if (sourceHash) {
      const persisted = await loadPersistentConversion(sourceHash, format);
      if (persisted) return persisted;
    }

    let blob: Blob;
    if (source.conversionSource !== undefined) {
      blob = await convertWriterDownload(source, format);
    } else {
      const content = await Promise.resolve(
        typeof source.content === 'function' ? source.content() : source.content,
      );
      if (content === undefined) {
        throw new Error('download source has no content');
      }
      blob = new Blob([content], {
        type: source.mimeType ?? 'application/octet-stream',
      });
    }
    if (sourceHash) {
      await savePersistentConversion(sourceHash, format, source, blob);
    }
    return blob;
  })();

  if (key) preparedFileCache.set(key, pending);
  try {
    return await pending;
  } catch (error) {
    if (key) preparedFileCache.delete(key);
    throw error;
  }
}

function triggerDownload(source: WriterDownloadSource, blob?: Blob): void {
  const anchor = document.createElement('a');
  anchor.href = source.href ?? URL.createObjectURL(blob!);
  anchor.download = source.filename;
  anchor.rel = 'noopener';
  anchor.click();
  if (blob) {
    window.setTimeout(() => URL.revokeObjectURL(anchor.href), 0);
  }
}

async function downloadSource(
  source: WriterDownloadSource,
  format: WriterDownloadFormat,
): Promise<void> {
  if (source.href) {
    triggerDownload(source);
    return;
  }
  triggerDownload(source, await prepareWriterDownloadBlob(source, format));
}

function formatLabel(format: WriterDownloadFormat): string {
  return format === 'markdown' ? 'Markdown' : '.lmd';
}

export function WriterDownloadFormatDialog({
  open,
  onOpenChange,
  markdown,
  lmd,
}: WriterDownloadFormatDialogProps) {
  const { t } = useTranslation();
  const [selectedFormat, setSelectedFormat] = useState<WriterDownloadFormat>('markdown');
  const [downloading, setDownloading] = useState<WriterDownloadFormat | null>(null);
  const [error, setError] = useState(false);

  useEffect(() => {
    if (!open) return;
    setSelectedFormat('markdown');
    setDownloading(null);
    setError(false);
  }, [open]);

  const sources = useMemo(() => ({ markdown, lmd }), [lmd, markdown]);

  const handleConfirm = useCallback(async () => {
    const format = selectedFormat;
    const source = sources[format];
    setDownloading(format);
    setError(false);
    try {
      await downloadSource(source, format);
      onOpenChange(false);
    } catch {
      setError(true);
    } finally {
      setDownloading(null);
    }
  }, [onOpenChange, selectedFormat, sources]);

  return (
    <Modal
      className='writer-download-format-modal'
      open={open}
      centered
      width={420}
      destroyOnClose
      maskClosable
      onCancel={() => onOpenChange(false)}
      title={t('chat.writer.downloadFormatTitle')}
      footer={(
        <div className='writer-download-format__footer'>
          {error && (
            <span className='writer-download-format__error' role='alert'>
              {t('chat.writer.downloadFormatFailed')}
            </span>
          )}
          <Button onClick={() => onOpenChange(false)} disabled={Boolean(downloading)}>
            {t('common.cancel')}
          </Button>
          <Button
            type='primary'
            icon={<DownloadOutlined aria-hidden />}
            loading={Boolean(downloading)}
            onClick={() => void handleConfirm()}
          >
            {t('chat.writer.downloadFormatConfirm', { format: formatLabel(selectedFormat) })}
          </Button>
        </div>
      )}
    >
      <p className='writer-download-format__description'>
        {t('chat.writer.downloadFormatDescription')}
      </p>
      <div className='writer-download-format__options' role='group' aria-label={t('chat.writer.downloadFormatTitle')}>
        {([
          {
            format: 'markdown' as const,
            icon: <FileTextOutlined aria-hidden />,
            hint: t('chat.writer.downloadFormatMarkdownHint'),
          },
          {
            format: 'lmd' as const,
            icon: <CodeOutlined aria-hidden />,
            hint: t('chat.writer.downloadFormatLmdHint'),
          },
        ]).map(({ format, icon, hint }) => {
          const selected = selectedFormat === format;
          return (
            <button
              key={format}
              type='button'
              className={`writer-download-format__option${selected ? ' writer-download-format__option--selected' : ''}`}
              aria-pressed={selected}
              disabled={Boolean(downloading)}
              onClick={() => setSelectedFormat(format)}
            >
              <span className='writer-download-format__option-icon'>{icon}</span>
              <span className='writer-download-format__option-copy'>
                <strong>{formatLabel(format)}</strong>
                <small>{hint}</small>
              </span>
            </button>
          );
        })}
      </div>
    </Modal>
  );
}

export function WriterDownloadFormatButton({
  disabled = false,
  className = 'workflow-slot__file-action-btn writer-artifact__download-btn',
  open: controlledOpen,
  onOpenChange: controlledOnOpenChange,
  markdown,
  lmd,
}: WriterDownloadFormatButtonProps) {
  const { t } = useTranslation();
  const [uncontrolledOpen, setUncontrolledOpen] = useState(false);
  const isControlled = controlledOpen !== undefined;
  const open = isControlled ? controlledOpen : uncontrolledOpen;
  const onOpenChange = controlledOnOpenChange ?? setUncontrolledOpen;

  return (
    <>
      <button
        type='button'
        className={className}
        disabled={disabled}
        aria-haspopup='dialog'
        aria-expanded={open}
        onClick={(event) => {
          event.stopPropagation();
          onOpenChange(true);
        }}
      >
        <DownloadOutlined aria-hidden />
        {t('chat.slots.download')}
      </button>
      <WriterDownloadFormatDialog
        open={open}
        onOpenChange={onOpenChange}
        markdown={markdown}
        lmd={lmd}
      />
    </>
  );
}
