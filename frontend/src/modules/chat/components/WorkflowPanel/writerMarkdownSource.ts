import MarkdownIt from 'markdown-it';
import { diffChars, diffLines, type Change } from 'diff';
import { markdownBlockText, markdownTextBlocks } from './artifactRewriteSelection';
const parser = new MarkdownIt({ html: true, linkify: true });
interface Token { type: string; content: string; map?: [number, number]; level: number; children?: Token[] }

function sourceBlocks(source: string) {
  const offsets = [0];
  for (const line of source.split('\n')) offsets.push(offsets[offsets.length - 1] + line.length + 1);
  const tokens = parser.parse(source, {}) as Token[];
  const parents: string[] = [];
  return tokens.flatMap(token => {
    if (token.type.endsWith('_open')) { parents.push(token.type); return []; }
    if (token.type.endsWith('_close')) { parents.pop(); return []; }
    if (!token.map || token.type !== 'inline'
      || parents.some(type => !['paragraph_open', 'heading_open', 'list_item_open', 'bullet_list_open', 'ordered_list_open'].includes(type))) return [];
    const kind = parents.includes('heading_open') ? 'heading' : parents.includes('list_item_open') ? 'list_item' : 'paragraph';
    const content = kind === 'list_item' ? token.content.replace(/^\[[ xX]\][ \t]+/, '') : token.content;
    const positions: number[] = [];
    const contentLines = content.split('\n');
    for (let index = 0; index < contentLines.length; index++) {
      const lineStart = offsets[token.map[0] + index];
      const line = source.slice(lineStart, offsets[token.map[0] + index + 1] - 1).replace(/\r$/, '');
      let prefix: RegExpMatchArray | null = null;
      if (index === 0 && kind === 'heading') prefix = line.match(/^ {0,3}#{1,6}(?:\s+|$)/);
      if (index === 0 && kind === 'list_item') prefix = line.match(/^\s*(?:[-+*]|\d+[.)])\s+(?:\[[ xX]\][ \t]+)?/);
      const at = line.indexOf(contentLines[index], prefix?.[0].length ?? 0);
      if (at < 0) return [];
      for (let j = 0; j < contentLines[index].length; j++) positions.push(lineStart + at + j);
      if (index < contentLines.length - 1) positions.push(offsets[token.map[0] + index + 1] - 1);
    }
    if (!positions.length) return [];
    const start = positions[0], end = positions[positions.length - 1] + 1;
    const raw = source.slice(start, end);
    const inline = (content === token.content ? token.children : parser.parseInline(content, {})[0]?.children) ?? [];
    const text = inline.map((item) => ['text', 'code_inline'].includes(item.type) ? item.content : ['softbreak','hardbreak'].includes(item.type) ? '\n' : '').join('');
    return [{ start, end, raw, text, kind, content, positions }];
  });
}

function blockKind(element: HTMLElement): string {
  return /^H[1-6]$/.test(element.tagName) ? 'heading' : element.closest('li') ? 'list_item' : 'paragraph';
}

/** Reattach a reviewed paragraph after the rich editor rebuilds its DOM. */
export function markdownParagraphAtRange(root: HTMLElement, source: string, start: number, end: number): HTMLElement | null {
  const paragraphs = sourceBlocks(source);
  const block = paragraphs.find(item => item.start <= start && item.end >= end);
  if (!block) return null;
  const normalize = (text: string) => text.replace(/\u00a0/g, ' ');
  const matches = paragraphs.filter(item => item.kind === block.kind && normalize(item.text) === normalize(block.text));
  const elements = markdownTextBlocks(root.querySelector<HTMLElement>('.mdxeditor-root-contenteditable') ?? root)
    .filter(element => blockKind(element) === block.kind && normalize(markdownBlockText(element)) === normalize(block.text));
  return matches.length === elements.length ? elements[matches.indexOf(block)] ?? null : null;
}

// Reject syntax we cannot map instead of using the first occurrence of a quote.
function inlinePositions(source: string) {
  let text = '';
  const starts: number[] = [], ends: number[] = [];
  const append = (value: string, start: number, end: number) => {
    text += value;
    for (let j = 0; j < value.length; j++) { starts.push(start); ends.push(end); }
  };
  const emphasis = new Set<string>();
  for (let i = 0; i < source.length;) {
    const rest = source.slice(i);
    const escape = rest.match(/^\\([!"#$%&'()*+,\-./:;<=>?@[\]\\^_`{|}~])/);
    if (escape) { append(escape[1], i, i + 2); i += 2; continue; }
    const entity = rest.match(/^&(?:#[0-9]+|#x[0-9a-f]+|[a-z]+);/i);
    if (entity) {
      const element = document.createElement('textarea'); element.innerHTML = entity[0];
      append(element.value, i, i + entity[0].length); i += entity[0].length; continue;
    }
    const code = rest.match(/^(`+)([\s\S]*?)\1(?!`)/);
    if (code) {
      const content = code[2].replace(/\n/g, ' ');
      const trim = content.startsWith(' ') && content.endsWith(' ') && /[^ ]/.test(content) ? 1 : 0;
      for (let j = trim; j < content.length - trim; j++) append(content[j], i + code[1].length + j, i + code[1].length + j + 1);
      i += code[0].length; continue;
    }
    const autolink = rest.match(/^<((?:https?:\/\/)[^<>]+|[^ <>]+@[^ <>]+)>/);
    if (autolink) {
      for (let j=0; j<autolink[1].length; j++) append(autolink[1][j], i+1+j, i+2+j);
      i += autolink[0].length; continue;
    }
    if (rest.startsWith('](')) {
      let depth = 1, j = i + 2;
      for (; j < source.length && depth; j++) {
        if (source[j] === '\\') { j++; continue; }
        if (source[j] === '(') depth++;
        if (source[j] === ')') depth--;
      }
      if (depth) throw new Error('selection mapping unavailable');
      i = j; continue;
    }
    const reference = rest.match(/^\]\[[^\]]*\]/);
    if (reference) { i += reference[0].length; continue; }
    if (source[i] === '[' && /^\[[^\n]*?\](?:\(|\[)/.test(rest)) { i++; continue; }
    const marker = rest.match(/^(\*{1,3}|_{1,3}|~~)/)?.[0];
    if (marker && !(marker[0] === '_' && /[\p{L}\p{N}]/u.test(source[i-1] ?? '') && /[\p{L}\p{N}]/u.test(source[i+marker.length] ?? ''))) {
      if (emphasis.has(marker)) { emphasis.delete(marker); i += marker.length; continue; }
      if (source.indexOf(marker, i + marker.length) >= 0) { emphasis.add(marker); i += marker.length; continue; }
    }
    if (source[i] === '<') {
      const tag = rest.match(/^<[^>]*>/);
      if (tag) { i += tag[0].length; continue; }
    }
    append(source[i], i, i + 1); i++;
  }
  return { text, starts, ends };
}

export function markdownSelectionRange(source: string, selection: {
  selectedText: string; paragraph?: HTMLElement; startOffset?: number;
}) {
  const paragraph = selection.paragraph;
  const visible = paragraph ? markdownBlockText(paragraph).replace(/\u00a0/g, ' ') : undefined;
  const paragraphs = sourceBlocks(source);
  let target: (typeof paragraphs)[number] | undefined;
  if (paragraph && visible !== undefined) {
    const editor = paragraph.closest('.mdxeditor-root-contenteditable, [data-writer-source-preview]');
    const kind = blockKind(paragraph);
    const siblings = editor ? markdownTextBlocks(editor)
      .filter(p => blockKind(p) === kind && markdownBlockText(p).replace(/\u00a0/g, ' ') === visible) : [paragraph];
    const candidates = paragraphs.filter(p => p.kind === kind && p.text.replace(/\u00a0/g, ' ') === visible);
    if (candidates.length !== siblings.length) throw new Error('selection is missing or ambiguous');
    target = candidates[siblings.indexOf(paragraph)];
  } else {
    const candidates = paragraphs.filter((p) => p.text.includes(selection.selectedText));
    if (candidates.length === 1) target = candidates[0];
  }
  if (!target) throw new Error('selection is missing or ambiguous');
  const map = inlinePositions(target.content);
  if (map.text !== target.text) throw new Error('selection mapping unavailable');
  const local = selection.startOffset ?? map.text.indexOf(selection.selectedText);
  if (local < 0 || map.text.slice(local, local + selection.selectedText.length) !== selection.selectedText) throw new Error('selection changed');
  if (selection.startOffset === undefined && map.text.indexOf(selection.selectedText, local + 1) >= 0) throw new Error('selection is ambiguous');
  const from = target.positions[map.starts[local]], to = target.positions[map.ends[local + selection.selectedText.length - 1] - 1] + 1;
  return { selected_text: source.slice(from, to), start: Array.from(source.slice(0, from)).length, end: Array.from(source.slice(0, to)).length };
}

interface SourceEdit { from: number; to: number; value: string }

function changesToEdits(changes: Change[]): SourceEdit[] {
  const edits: SourceEdit[] = [];
  let offset = 0;
  let pending: SourceEdit | undefined;
  for (const change of changes) {
    if (!change.added && !change.removed) {
      if (pending) { edits.push(pending); pending = undefined; }
      offset += change.value.length;
      continue;
    }
    pending ??= { from: offset, to: offset, value: '' };
    if (change.removed) { offset += change.value.length; pending.to = offset; }
    else pending.value += change.value;
  }
  if (pending) edits.push(pending);
  return edits;
}

function sourceEdits(before: string, after: string): SourceEdit[] {
  if (before === after) return [];
  // A whole-document character diff can time out on ordinary README tables.
  // First anchor unchanged lines, then refine only the changed spans. Never
  // substitute the normalized export when a mapping cannot be completed.
  const lines = diffLines(before, after, { timeout: 100 });
  if (!lines) throw new Error('Markdown source mapping timed out');
  const refine = (from: number, oldText: string, newText: string): SourceEdit[] => {
    if (oldText === newText) return [];
    if (!oldText || !newText) return [{ from, to: from + oldText.length, value: newText }];
    const changes = diffChars(oldText, newText, { timeout: 100 });
    if (!changes) throw new Error('Markdown source mapping timed out');
    return changesToEdits(changes).map(edit => ({ ...edit, from: from + edit.from, to: from + edit.to }));
  };
  // Empty lines are separators, not reliable anchors: a removed leading blank
  // must not pair with a later paragraph break and move text across a user edit.
  const anchoredLines = lines.flatMap(change => !change.added && !change.removed && !change.value.trim()
    ? [{ ...change, removed: true }, { ...change, added: true }]
    : [change]);
  return changesToEdits(anchoredLines).flatMap(edit => {
    const oldText = before.slice(edit.from, edit.to);
    const oldLines = oldText.match(/[^\n]*\n|[^\n]+$/g) ?? [];
    const newLines = edit.value.match(/[^\n]*\n|[^\n]+$/g) ?? [];
    if (oldLines.length !== newLines.length) return refine(edit.from, oldText, edit.value);
    let offset = edit.from;
    return oldLines.flatMap((line, index) => {
      const edits = refine(offset, line, newLines[index]);
      offset += line.length;
      return edits;
    });
  });
}

function sourceLinkRanges(markdown: string) {
  const opening = /!?\[(?:\\.|[^\]\\\n])*\]\(/g;
  const links: Array<{from:number;to:number}> = [];
  let match: RegExpExecArray | null;
  while ((match = opening.exec(markdown))) {
    let depth = 1, quote = '', angle = false, index = opening.lastIndex;
    for (; index < markdown.length && depth; index++) {
      const character = markdown[index];
      if (character === '\\') { index++; continue; }
      if (quote) { if (character === quote) quote = ''; continue; }
      if (angle) { if (character === '>') angle = false; continue; }
      if (character === '<') { angle = true; continue; }
      if ((character === '"' || character === "'") && /\s/.test(markdown[index - 1] ?? '')) { quote = character; continue; }
      if (character === '(') depth++;
      if (character === ')') depth--;
    }
    if (!depth) links.push({from:match.index,to:index});
    opening.lastIndex = index;
  }
  return links;
}

/** Undo import normalization only where the subsequent user edit did not touch it.
 * Keeping the edited export as the starting point preserves text and formatting
 * together, and does not require paragraphs to retain their count or order.
 */
export function preserveMarkdownSource(original: string, previousExport: string, nextExport: string): string {
  if (previousExport === nextExport) return original;
  if (!nextExport.trim()) return nextExport;
  const normalized = sourceEdits(original, previousExport);
  const changes = sourceEdits(previousExport, nextExport);
  const touches = (start: number, end: number) => changes.some((change) =>
    change.from === change.to ? start < change.from && change.from < end : start < change.to && change.from < end);
  const mapBoundary = (position: number, right: boolean) => {
    let shift = 0;
    for (const change of changes) {
      if (change.from === change.to && change.from === position) {
        if (right) shift += change.value.length;
      } else if (change.to <= position) shift += change.value.length - (change.to - change.from);
      else if (change.from < position) return undefined;
    }
    return position + shift;
  };
  const editedLinks = sourceLinkRanges(previousExport).filter((link) => touches(link.from,link.to));
  const replacements: SourceEdit[] = [];
  let shift = 0;
  for (const normalization of normalized) {
    const start = normalization.from + shift;
    const end = start + normalization.value.length;
    const source = original.slice(normalization.from, normalization.to);
    shift += normalization.value.length - (normalization.to - normalization.from);
    if (touches(start,end) || editedLinks.some((link) => start < link.to && link.from < end)) continue;
    if (start === end && source.trim() && !source.includes('\n')
      && changes.some((change) => change.from < start && start <= change.to)) continue;
    // Core's anchor protection may already have restored an omitted anchor.
    const insertions = changes.filter((change) => change.from === start && change.to === start).map((change) => change.value).join('');
    if (source && insertions.includes(source)) continue;
    const anchorIds = [...source.matchAll(/<a\b[^>]*\bid=["']([^"']+)["']/g)].map((match) => match[1]);
    if (anchorIds.length) continue; // Existing anchor protection owns restoration/removal.
    const from = mapBoundary(start, start !== end || end === previousExport.length);
    const to = start === end ? from : mapBoundary(end, false);
    if (from === undefined || to === undefined) continue;
    replacements.push({from,to,value:source});
  }
  let result = nextExport;
  for (const edit of replacements.sort((a,b) => b.from-a.from || b.to-a.to)) {
    result = result.slice(0,edit.from) + edit.value + result.slice(edit.to);
  }
  // Rich editors do not represent file-edge whitespace. Explicit source edits
  // bypass this function and can intentionally change those margins.
  const leading = original.match(/^\s*/)?.[0] ?? '';
  const trailing = original.match(/\s*$/)?.[0] ?? '';
  return leading + result.trim() + trailing;
}

/** Source-authoritative paragraph set for a batch; response ranges cannot expand it. */
export function markdownRewriteTargets(source: string, selections: Array<{start:number;end:number;selected_text:string}>) {
  const runes=Array.from(source);
  const paragraphs=sourceBlocks(source).map(block=>({...block,
    start:Array.from(source.slice(0,block.start)).length,end:Array.from(source.slice(0,block.end)).length}));
  const selected=new Map<number,(typeof paragraphs)[number]>();
  for(const range of selections) {
    if(!Number.isInteger(range.start)||!Number.isInteger(range.end)||range.start<0||range.end<=range.start||range.end>runes.length||runes.slice(range.start,range.end).join('')!==range.selected_text)throw new Error('Invalid source selection');
    let cursor=range.start,matched=false;
    for(const block of paragraphs) {
      const from=Math.max(range.start,block.start),to=Math.min(range.end,block.end);
      if(from>=to||!runes.slice(from,to).join('').trim())continue;
      if(runes.slice(cursor,from).join('').trim())throw new Error('Unsupported source structure');
      cursor=to;matched=true;selected.set(block.start,block);
    }
    if(!matched||runes.slice(cursor,range.end).join('').trim())throw new Error('Unsupported source structure');
  }
  return [...selected.values()].sort((a,b)=>a.start-b.start);
}
