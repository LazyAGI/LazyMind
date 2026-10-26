import MarkdownIt from 'markdown-it';
import { isWriterDocument, type WriterBlock } from './writerIR';

export interface DocumentImageReference { source: string; alt: string; preview?: string }
const markdownParser = new MarkdownIt({ html: true });
type ImageMarkdownToken = { type: string; content: string; attrGet: (name: string) => string | null; children?: ImageMarkdownToken[] };

/** UI inventory only. The server independently validates the captured document. */
export function documentImages(document: unknown): DocumentImageReference[] {
  const images: DocumentImageReference[] = [];
  const seen = new Set<string>();
  const add = (image: DocumentImageReference) => {
    if (image.source && seen.has(image.source)) return;
    if (image.source) seen.add(image.source);
    images.push(image);
  };
  if (typeof document === 'string') {
    const visit = (tokens: ImageMarkdownToken[]) => {
      for (const token of tokens) {
        if (token.type === 'image') add({ source: token.attrGet('src') ?? '', alt: token.content });
        else if (token.type === 'html_inline' || token.type === 'html_block') {
          const html = new DOMParser().parseFromString(token.content, 'text/html');
          html.querySelectorAll('img').forEach(image => {
            if (!image.closest('pre, code, script, style')) add({ source: image.getAttribute('src') ?? '', alt: image.alt });
          });
        } else if (token.children) visit(token.children);
      }
    };
    visit(markdownParser.parse(document, {}) as ImageMarkdownToken[]);
  } else if (isWriterDocument(document)) {
    const visit = (blocks: WriterBlock[]) => {
      for (const block of blocks) {
        if (block.type === 'image') {
          const ref = block.references?.find(item => item.type === 'media_asset');
          const preview = block.references?.find(item => item.type === 'preview_asset');
          const source = ref?.id ? `asset://${String(ref.id)}` : String(ref?.path ?? ref?.url ?? preview?.url ?? preview?.path ?? '');
          add({ source, alt: block.content ?? '', preview: String(preview?.url ?? preview?.path ?? ref?.path ?? ref?.url ?? '') || undefined });
        }
        if (block.children) visit(block.children);
      }
    };
    visit(document.blocks);
  }
  return images;
}
