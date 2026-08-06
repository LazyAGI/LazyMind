import type { SlotRevision } from '@/modules/chat/store/pluginPanel';
import type { WriterBlock, WriterDocument } from './writerIR';

/**
 * Normalize the content_type returned by the Python backend.
 * Python stores short forms: 'text', 'json', 'image', 'file', 'file_list'.
 */
export function normalizeContentType(ct: string): 'image' | 'file' | 'text' {
  if (ct === 'image' || ct.startsWith('image/')) return 'image';
  if (ct === 'file' || ct === 'file_list' || ct.startsWith('application/')) return 'file';
  return 'text';
}

/** True when the URL can be used directly as an <img src> in the browser. */
export function isBrowserReadyImageUrl(url: string): boolean {
  const trimmed = (url || '').trim();
  if (!trimmed) return false;
  if (trimmed.startsWith('data:image/')) return true;
  if (/^https?:\/\//i.test(trimmed)) return true;
  return trimmed.includes('/api/core/static-files/') || trimmed.includes('/static-files/');
}

export function preloadImageUrl(src: string): Promise<boolean> {
  return new Promise((resolve) => {
    const img = new Image();
    img.onload = () => resolve(true);
    img.onerror = () => resolve(false);
    img.src = src;
  });
}

/**
 * The dev server answers unknown paths with the SPA shell, so a 200 response is
 * not proof that an artifact exists.
 */
export function isSpaFallbackHtml(content: string): boolean {
  const normalized = content.trim().toLowerCase();
  return normalized.startsWith('<!doctype html')
    && (normalized.includes('/@vite/client') || normalized.includes('id="root"'));
}

export function getFileIcon(filename: string): string {
  const ext = filename.split('.').pop()?.toLowerCase() ?? '';
  if (ext === 'pdf') return '📕';
  if (ext === 'doc' || ext === 'docx') return '📝';
  if (ext === 'xls' || ext === 'xlsx') return '📊';
  if (ext === 'ppt' || ext === 'pptx') return '📑';
  if (ext === 'txt' || ext === 'md') return '📄';
  if (ext === 'json' || ext === 'csv') return '📋';
  if (ext === 'zip' || ext === 'tar' || ext === 'gz' || ext === 'rar') return '🗜️';
  if (ext === 'jpg' || ext === 'jpeg' || ext === 'png' || ext === 'gif' || ext === 'webp') return '🖼️';
  return '📎';
}

export function formatFileSize(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function artifactNameAndPath(slot: SlotRevision): { name: string; path: string } {
  const raw = slot.artifact_value;
  return {
    name: String(raw?.filename ?? raw?.name ?? '').toLowerCase(),
    path: String(raw?.url ?? raw?.path ?? '').toLowerCase(),
  };
}

export function isJsonArtifactFile(slot: SlotRevision): boolean {
  const { name, path } = artifactNameAndPath(slot);
  return name.endsWith('.json') || path.endsWith('.json');
}

export function isWriterIrArtifactFile(slot: SlotRevision): boolean {
  return artifactNameAndPath(slot).name.endsWith('_ir.json');
}

export function isMarkdownArtifactFile(slot: SlotRevision): boolean {
  const { name, path } = artifactNameAndPath(slot);
  return name.endsWith('.md')
    || name.endsWith('.markdown')
    || path.endsWith('.md')
    || path.endsWith('.markdown');
}

export function isOffloadedArtifactReference(raw: Record<string, unknown>): boolean {
  const hasPath = Boolean(String(raw.path ?? raw.url ?? '').trim());
  return hasPath && (raw.type === 'text' || raw.type === 'json');
}

export function hasProviderTarget(document?: WriterDocument | null): boolean {
  const binding = document?.provider_binding;
  if (!binding) return false;

  return (
    (typeof binding.uri === 'string' && binding.uri.trim() !== '')
    || (
      typeof binding.document_id === 'string'
      && binding.document_id.trim() !== ''
    )
  );
}

export function ensureJsonFilename(name: string): string {
  const trimmed = name.trim() || 'writer-document.json';
  return trimmed.toLowerCase().endsWith('.json') ? trimmed : `${trimmed}.json`;
}

export function writerBlockToMarkdown(block: WriterBlock, depth = 0): string {
  if (block.type === 'document') {
    return (block.children ?? []).map((child) => writerBlockToMarkdown(child, depth)).filter(Boolean).join('\n\n');
  }

  const content = block.content?.trim() ?? '';
  const children = (block.children ?? [])
    .map((child) => writerBlockToMarkdown(child, depth + 1))
    .filter(Boolean)
    .join('\n\n');
  let current = content;

  if (block.type === 'heading') {
    const level = Math.min(6, Math.max(1, Number(block.numbering?.level ?? 2)));
    current = content ? `${'#'.repeat(level)} ${content}` : '';
  } else if (block.type === 'list_item') {
    current = content ? `${'  '.repeat(depth)}${block.numbering?.ordered ? '1.' : '-'} ${content}` : '';
  } else if (block.type === 'quote') {
    current = content ? content.split('\n').map((line) => `> ${line}`).join('\n') : '';
  } else if (block.type === 'code') {
    current = content ? `\`\`\`\n${content}\n\`\`\`` : '';
  } else if (block.type === 'divider') {
    current = '---';
  }

  return [current, children].filter(Boolean).join('\n\n');
}

export function writerDocumentToMarkdown(document: WriterDocument): string {
  const title = document.title.trim() ? `# ${document.title.trim()}` : '';
  const body = document.blocks.map((block) => writerBlockToMarkdown(block)).filter(Boolean).join('\n\n');
  return `${[title, body].filter(Boolean).join('\n\n')}\n`;
}

export function writerMarkdownFilename(name: string): string {
  const trimmed = name.trim() || 'document';
  if (trimmed.toLowerCase().endsWith('_ir.json')) return `${trimmed.slice(0, -8)}.md`;
  return `${trimmed.replace(/\.json$/i, '')}.md`;
}
