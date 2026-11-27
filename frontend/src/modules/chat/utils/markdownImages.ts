import MarkdownIt from 'markdown-it';
import { axiosInstance } from '@/components/request';
import { coreApiUrl } from '@/runtime/apiBase';

const parser = new MarkdownIt() as MarkdownIt & { validateLink: (value: string) => boolean };
const validateLink = parser.validateLink.bind(parser);
// This parser only discovers dependencies; it never renders file URIs into browser HTML.
parser.validateLink = value => /^file:/i.test(value) || validateLink(value);
// Optional image work must not hold up the Markdown upload indefinitely.
const imageTimeoutMs = 5000;
interface ImageToken { type: string; attrGet: (name: string) => string | null; children?: ImageToken[] }
export interface LocalMarkdownImage { source: string; name: string; mime_type: string; content_base64: string }

export function localMarkdownImageSources(text: string): string[] {
  const images = new Set<string>();
  const visit = (tokens: ImageToken[]) => {
    for (const token of tokens) {
      const source = token.type === 'image' ? token.attrGet('src') ?? '' : '';
      if (source && !/^(?:https?:|data:|asset:|\/\/|\/static-files\/|\/api\/core\/)/i.test(source)) images.add(source);
      if (token.children) visit(token.children);
    }
  };
  visit(parser.parse(text, {}) as ImageToken[]);
  return [...images].slice(0, 32);
}

function bounded<T>(promise: Promise<T>, ms: number): Promise<T> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error('image collection timed out')), Math.max(1, ms));
    promise.then(resolve, reject).finally(() => clearTimeout(timer));
  });
}

// A small immutable manifest travels with the MD snapshot; image bytes remain owned Core input resources.
export async function prepareMarkdownUpload(file: File): Promise<File> {
  if (!/\.(?:md|markdown)$/i.test(file.name)) return file;
  try {
    const text = await bounded(file.text(), imageTimeoutMs);
    const references = localMarkdownImageSources(text);
    if (!references.length) return file;
    const deadline = Date.now() + Math.max(30000, Math.min(180000, references.length * 10000));
    // Match Desktop's per-image read budget and allow 5s for IPC overhead.
    const collectTimeoutMs = Math.max(15000, Math.min(120000, references.length * 5000)) + 5000;
    const images: Array<{ source: string; resource_id: string }> = [];
    const warnings: string[] = [];
    const collect = window.lazymindDesktop?.collectMarkdownImages;
    let collected: { images: LocalMarkdownImage[]; warnings: string[] } = { images: [], warnings: [] };
    if (collect) {
      try { collected = await bounded(collect(file, references), Math.min(collectTimeoutMs, deadline - Date.now())); }
      catch { warnings.push('引用图片读取失败或超时，正文仍可使用。'); }
    } else {
      warnings.push('当前上传入口无法读取原始目录中的引用图片，正文仍可使用。');
    }
    warnings.push(...collected.warnings);
    const imported = new Map<string, string>();
    for (const image of collected.images.slice(0, 32)) {
      try {
        const remaining = deadline - Date.now();
        if (remaining <= 0) { warnings.push('引用图片收集超时，已使用成功导入的图片。'); break; }
        const bytes = Uint8Array.from(atob(image.content_base64), value => value.charCodeAt(0));
        if (!references.includes(image.source) || !image.mime_type.startsWith('image/') || bytes.length > 20 * 1024 * 1024) continue;
        const digest = [...new Uint8Array(await crypto.subtle.digest('SHA-256', bytes))]
          .map(value => value.toString(16).padStart(2, '0')).join('');
        let resourceId = imported.get(digest);
        if (!resourceId) {
          const response = await bounded(axiosInstance.post(coreApiUrl('workflow-input-resources'), {
            contract_version: 'workflow.v1', name: image.name, mime_type: image.mime_type,
            size: bytes.length, content_hash: `sha256:${digest}`, content_base64: image.content_base64,
          }, { timeout: Math.min(imageTimeoutMs, deadline - Date.now()), headers: { 'Workflow-Contract-Version': 'workflow.v1' }, silentError: true } as never), Math.min(imageTimeoutMs, deadline - Date.now()));
          resourceId = response.data?.result?.resource_id;
          if (!resourceId || !/^[A-Za-z0-9_-]{1,128}$/.test(resourceId)) throw new Error('image import failed');
          imported.set(digest, resourceId);
        }
        images.push({ source: image.source, resource_id: resourceId });
      } catch { warnings.push(`图片未导入：${image.source}`); }
    }
    const manifest = JSON.stringify({ images, warnings });
    if (new TextEncoder().encode(manifest).length > 48 * 1024) return file;
    const encoded = btoa(String.fromCharCode(...new TextEncoder().encode(manifest)));
    return new File([text, `\n\n<!-- lazymind-markdown-images:v1:${encoded} -->\n`], file.name,
      { type: file.type || 'text/markdown', lastModified: file.lastModified });
  } catch { return file; }
}
