import MarkdownIt from 'markdown-it';
import { isWriterSystemAnchorBlock, type WriterBlock, type WriterDocument } from './writerIR';
import { writerMarkdownForEditing } from './writerMarkdownAnchors';

export interface WriterImageRef { source: string; occurrence: number }
export interface WriterImageGroup { images: WriterImageRef[]; layout: 'sliding' | 'vertical' }
export interface WriterImageCandidate { images: WriterImageRef[]; nodeIds?: string[] }
interface MarkdownToken { type: string; content: string; map?: [number, number] }
const markdownParser = new MarkdownIt({ html: true });

export function normalizeImageSource(source: string): string {
  return markdownParser.normalizeLink(source);
}

function parseImageGroupComment(html: string): WriterImageGroup[] | undefined {
  const comment = new DOMParser().parseFromString(html.trim(), 'text/html').firstChild;
  if (comment?.nodeType !== Node.COMMENT_NODE || html.trim() !== `<!--${comment.nodeValue}-->`) return undefined;
  const value = comment.nodeValue!.trim();
  const prefix = 'writer-image-groups:';
  if (!value.startsWith(prefix)) return undefined;
  const groups: unknown = JSON.parse(value.slice(prefix.length));
  if (!Array.isArray(groups)) throw new Error('Invalid image group settings.');
  return imageGroups(groups);
}

function imageGroupComment(groups: WriterImageGroup[]): string {
  return `<!-- writer-image-groups: ${JSON.stringify(groups).replace(/</g, '\\u003c').replace(/>/g, '\\u003e')} -->`;
}

function parseGroupMarkdown(markdown: string) {
  const tokens = markdownParser.parse(markdown, {}) as MarkdownToken[];
  const token = tokens[tokens.length - 1];
  const groups = token?.type === 'html_block' ? parseImageGroupComment(token.content) : undefined;
  return {
    groups: groups ?? [],
    content: groups ? markdown.split(/(?<=\n)/).slice(0, token!.map![0]).join('') : markdown,
  };
}

export function stripImageGroupMarkdown(markdown: string): string {
  return parseGroupMarkdown(markdown).content;
}

export function imageGroupMarkdown(markdown: string, groups: WriterImageGroup[]): string {
  const content = stripImageGroupMarkdown(markdown);
  return groups.length ? `${content.trimEnd()}\n\n${imageGroupComment(groups)}\n` : content;
}

export function imageGroupKey(images: WriterImageRef[]): string {
  return JSON.stringify(images.map(image => JSON.stringify({ source: normalizeImageSource(image.source), occurrence: image.occurrence })).sort());
}

export function imageGroups(value: unknown): WriterImageGroup[] {
  if (value == null) return [];
  if (!Array.isArray(value)) throw new Error('Invalid image group settings.');
  return value.map(group => {
    if (!group || !['sliding', 'vertical'].includes(group.layout) || !Array.isArray(group.images) || group.images.length < 2
      || group.images.some((image: WriterImageRef) => typeof image.source !== 'string' || !image.source || !Number.isInteger(image.occurrence) || image.occurrence < 0)
      || new Set(group.images.map((image: WriterImageRef) => imageGroupKey([image]))).size !== group.images.length) throw new Error('Invalid image group settings.');
    return { images: group.images.map((image: WriterImageRef) => ({ source: image.source, occurrence: image.occurrence })), layout: group.layout };
  });
}

export function imageGroupLayout(groups: WriterImageGroup[], images: WriterImageRef[]) {
  return groups.find(group => imageGroupKey(group.images) === imageGroupKey(images))?.layout;
}

export function setImageGroup(groups: WriterImageGroup[], images: WriterImageRef[], layout: WriterImageGroup['layout']) {
  return [...groups.filter(group => imageGroupKey(group.images) !== imageGroupKey(images)), { images, layout }];
}

export function imageSource(block: WriterBlock): string {
  const reference = block.references?.find(item => item.type === 'media_asset')
    ?? block.references?.find(item => item.type === 'preview_asset')
    ?? block.references?.find(item => item.type === 'wechat_image');
  return normalizeImageSource(String(reference?.url ?? reference?.path ?? reference?.id ?? ''));
}

export function irImageCandidates(document: WriterDocument): WriterImageCandidate[] {
  const candidates: WriterImageCandidate[] = [];
  const counts = new Map<string, number>();
  const visit = (blocks: WriterBlock[]) => {
    let current: WriterImageCandidate = { images: [], nodeIds: [] };
    const finish = () => { if (current.images.length >= 2) candidates.push(current); current = { images: [], nodeIds: [] }; };
    for (const block of blocks) {
      if (block.type === 'image' && !block.children?.length) {
        const source = imageSource(block);
        if (!source) { finish(); continue; }
        const occurrence = counts.get(source) ?? 0;
        counts.set(source, occurrence + 1);
        current.images.push({ source, occurrence }); current.nodeIds!.push(block.node_id);
      } else { finish(); if (block.children?.length) visit(block.children); }
    }
    finish();
  };
  visit(document.blocks);
  return candidates;
}

export function markdownImageCandidates(markdown: string): WriterImageCandidate[] {
  const html = markdownParser.render(writerMarkdownForEditing(stripImageGroupMarkdown(markdown)));
  const body = new DOMParser().parseFromString(html, 'text/html').body;
  const counts = new Map<string, number>();
  const result: WriterImageCandidate[] = [];
  let current: WriterImageRef[] = [];
  const finish = () => { if (current.length >= 2) result.push({ images: current }); current = []; };
  const isAnchor = (node: Node) => node instanceof Element
    && isWriterSystemAnchorBlock({ node_id: '', type: 'paragraph', content: node.outerHTML });
  const isWhitespace = (node: Node) => node.nodeType === Node.TEXT_NODE && !node.textContent?.trim();
  for (const node of Array.from(body.childNodes)) {
    if (isWhitespace(node) || isAnchor(node)) continue;
    if (!(node instanceof Element)) { finish(); continue; }
    const elements = node.tagName === 'IMG' ? [node] : Array.from(node.querySelectorAll('img'));
    const images = elements.flatMap(element => {
      const source = normalizeImageSource(element.getAttribute('src') ?? '');
      if (!source) return [];
      const occurrence = counts.get(source) ?? 0; counts.set(source, occurrence + 1);
      return [{ source, occurrence }];
    });
    const imageOnly = node.tagName === 'IMG' || (node.tagName === 'P' && Array.from(node.childNodes).every(child =>
      isWhitespace(child) || isAnchor(child) || (child instanceof Element && ['IMG', 'BR'].includes(child.tagName))));
    if (imageOnly && images.length && images.length === elements.length) current.push(...images);
    else finish();
  }
  finish(); return result;
}

export function documentImageGroups(value: string | WriterDocument) {
  if (typeof value !== 'string') return imageGroups(value.metadata?.image_groups);
  return parseGroupMarkdown(value).groups;
}

export function pendingImageGroups(value: string | WriterDocument) {
  const groups = documentImageGroups(value);
  const candidates = typeof value === 'string' ? markdownImageCandidates(value) : irImageCandidates(value);
  return candidates.filter(candidate => candidate.images.length >= 3 && !imageGroupLayout(groups, candidate.images));
}

export function keepVerticalImageGroups(value: string | WriterDocument, pending = pendingImageGroups(value)): string | WriterDocument {
  const groups = [...documentImageGroups(value), ...pending.map(({ images }) => ({ images, layout: 'vertical' as const }))];
  if (typeof value !== 'string') return { ...value, metadata: { ...value.metadata, image_groups: groups } };
  return imageGroupMarkdown(value, groups);
}
