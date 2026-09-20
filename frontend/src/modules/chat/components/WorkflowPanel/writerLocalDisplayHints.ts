import { createContext, useEffect, useState, type RefObject } from 'react';
import MarkdownIt from 'markdown-it';
import type { DocumentRenderContext } from '@/api/generated/core-client';
import { resolveMarkdownImageUrlAsync, type MarkdownImageResolver } from '@/modules/knowledge/utils/imageUrl';
import { writerPreviewMarkdown } from './writerSourceSyntax';

export const WriterCodeDisplayContext = createContext<ReadonlyMap<string, string[]>>(new Map());

/** Display metadata is validated against the original source and never serialized into the draft. */
export function useWriterLocalDisplayHints(root: RefObject<HTMLElement>, source: string, context?: DocumentRenderContext, resolve: MarkdownImageResolver = resolveMarkdownImageUrlAsync) {
  const [languages, setLanguages] = useState<ReadonlyMap<string, string[]>>(new Map());
  useEffect(() => {
    let active = true;
    let observer: MutationObserver | undefined;
    const cleanup: Array<() => void> = [];
    setLanguages(new Map());
    void (async () => {
      if (!context || await writerPreviewMarkdown(source, context) === source || !active) return;
      const points = Array.from(source);
      const parser = new MarkdownIt();
      type Fence = { type: string; info: string; content: string; map: [number, number] };
      const fences = (parser.parse(source, {}) as Fence[]).filter(item => item.type === 'fence');
      const codes = new Map<string, string[]>();
      const lineStarts = [0];
      for (const line of source.split('\n')) lineStarts.push(lineStarts[lineStarts.length - 1] + Array.from(line).length + 1);
      for (const hint of context.code_fences ?? []) {
        const token = (parser.parse(points.slice(hint.start, hint.end).join(''), {}) as Fence[])[0];
        if (token?.type !== 'fence') continue;
        const key = `${token.info.trim()}\n${token.content.replace(/\n$/, '')}`;
        const matches = fences.filter(item => `${item.info.trim()}\n${item.content.replace(/\n$/, '')}` === key);
        const index = matches.findIndex(item => lineStarts[item.map[0]] === hint.start);
        if (index < 0) continue;
        const values = codes.get(key) ?? matches.map(item => item.info.trim());
        values[index] = hint.language;
        codes.set(key, values);
      }
      if (active) setLanguages(codes);
      const sizes = await Promise.all((context.images ?? []).map(async hint => {
        const token = parser.parseInline(points.slice(hint.start, hint.end).join(''), {})[0]?.children?.[0];
        const src = token?.type === 'image' && token.attrGet('src');
        return src ? { url: await resolve(src), width: hint.width, height: hint.height } : undefined;
      }));
      if (!active || !root.current) return;
      const visited = new WeakSet<HTMLImageElement>();
      const apply = () => root.current?.querySelectorAll<HTMLImageElement>('img').forEach(image => {
        if (visited.has(image)) return;
        const matches = sizes.filter(size => size?.url === image.getAttribute('src'));
        const images = Array.from(root.current?.querySelectorAll<HTMLImageElement>('img') ?? []).filter(item => item.getAttribute('src') === image.getAttribute('src'));
        if (matches.length !== images.length) return;
        const size = matches[images.indexOf(image)];
        if (!size) return;
        if (!Number.isInteger(size.width) || size.width < 1 || size.width > 10000) return;
        visited.add(image);
        const previous = { width: image.style.width, height: image.style.height };
        image.style.width = `${size.width}px`;
        if (size.height && size.height > 0 && size.height <= 10000) image.style.height = `${size.height}px`;
        cleanup.push(() => { image.style.width = previous.width; image.style.height = previous.height; });
      });
      observer = new MutationObserver(apply);
      observer.observe(root.current, { subtree: true, childList: true, attributes: true, attributeFilter: ['src'] });
      apply();
    })().catch(() => { /* Keep the source's own display when metadata or a resource is unavailable. */ });
    return () => { active = false; observer?.disconnect(); cleanup.forEach(fn => fn()); };
  }, [context, resolve, root, source]);
  return languages;
}
