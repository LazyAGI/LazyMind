import MarkdownIt from 'markdown-it';
import type { DocumentRenderContext } from '@/api/generated/core-client';

/** Display-only adaptation. The editor always persists the original source. */
export function writerSourceMarkdown(source: string) {
  const notes: string[] = [];
  const emoji: Record<string,string> = {smile:'😄',rocket:'🚀',warning:'⚠️',note:'📝',heart:'❤️',thumbsup:'👍',tada:'🎉',white_check_mark:'✅',x:'❌',bulb:'💡',fire:'🔥',eyes:'👀'};
  let fence = '';
  let inComment = false;
  const lines = source.split('\n').map((line) => {
    const marker = line.match(/^ {0,3}(`{3,}|~{3,})(.*)$/);
    if (marker && !fence) { fence = marker[1]; return line; }
    if (marker && fence && marker[1][0] === fence[0] && marker[1].length >= fence.length && !marker[2].trim()) { fence = ''; return line; }
    if (fence) return line;
    const visible = line.split(/(`+[^`]*`+)/g).map((segment) => {
      if (segment.startsWith('`') && !inComment) return segment;
      let text = '';
      for (const [index, part] of segment.split('%%').entries()) {
        if (index) inComment = !inComment;
        if (!inComment) text += part;
      }
      return text;
    }).join('');
    return visible.split(/(`+[^`]*`+|<[^>]*>|!?\[[^\]]*\]\([^)]+\))/g).map((part) => part.startsWith('`') || part.startsWith('<') || /^!?\[[^\]]*\]\(/.test(part) ? part : part
      .replace(/(!?)\[\[([^\]\n]+)\]\]/g, (_all, embed: string, target: string) => {
        const [path, label] = target.split('|');
        const kind = embed ? 'writer-embed' : 'writer-note';
        return `${embed ? '!' : ''}[${(label || path).replace(/[\[\]]/g, '')}](${kind}:${encodeURIComponent(target)})`;
      })
      .replace(/\^\[([^\]\n]+)\]/g, (_all, text: string) => {
        const id = `writer-inline-${notes.length + 1}`; notes.push(`[^${id}]: ${text}`); return `[^${id}]`;
      })
      .replace(/\s+\^([\w-]+)\s*$/, '<a id="writer-block-$1"></a>')
      .replace(/(https?:\/\/[^\s<>\u3000-\u303f\uff00-\uffef]+)(?=[\u3000-\u303f\uff00-\uffef])/g, '<$1>')
      .replace(/:([a-z_]+):/g, (raw, name:string) => emoji[name] ?? raw)
    ).join('');
  });
  return [...lines, '', ...notes].join('\n');
}

export function writerSafeUrl(value: string): string | undefined {
  const url = value.trim();
  if (!url || /[\u0000-\u0020\\]/.test(url) || url.startsWith('//')) return undefined;
  if (/^(https?:|mailto:)/i.test(url) || !/^[a-z][a-z0-9+.-]*:/i.test(url)) return url;
  return undefined;
}

export function writerNoteTarget(raw: string) {
  const [target, label] = raw.split('|');
  let url: string | undefined;
  if (target.startsWith('#')) url = target.startsWith('#^') ? `#writer-block-${target.slice(2)}` : target;
  else if (/^https?:/i.test(target)) url = writerSafeUrl(target);
  return { target, label: label || target, url };
}

interface MarkdownNode { type: string; value?: string; children?: MarkdownNode[]; data?: Record<string, unknown> }
export function writerCallouts() {
  return (tree: MarkdownNode) => {
    const visit = (node: MarkdownNode) => {
      if (node.type === 'blockquote') {
        const first = node.children?.[0]?.children?.[0];
        const match = first?.value?.match(/^\[!([\w-]+)\]([+-]?)(?:[ \t]+([^\n]*))?(?:\n|$)/);
        if (match && first) {
          first.value = first.value!.slice(match[0].length);
          node.data = { hName: 'details', hProperties: { open: match[2] !== '-', 'data-callout': match[1].toLowerCase() } };
          node.children!.unshift({type:'paragraph',data:{hName:'summary'},children:[{type:'text',value:match[3] || match[1]}]});
        }
      }
      node.children?.forEach(visit);
    };
    visit(tree);
  };
}

export async function writerPreviewMarkdown(source: string, context?: DocumentRenderContext): Promise<string> {
  if (!context?.source_hash || !globalThis.crypto?.subtle) return source;
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(source));
  const hash = Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('');
  if (hash !== context.source_hash) return source;
  const points = Array.from(source);
  const edits: {start:number;end:number;value:string}[] = [];
  for (const hint of context.code_fences ?? []) {
    if (!/^[A-Za-z0-9_+.-]{1,40}$/.test(hint.language)) continue;
    const text = points.slice(hint.start,hint.end).join('');
    edits.push({...hint,value:text.replace(/^([ ]{0,3}(?:`{3,}|~{3,})[ \t]*)\S+/, `$1${hint.language}`)});
  }
  const escape = (value:string) => value.replace(/&/g,'&amp;').replace(/"/g,'&quot;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
  const parser = new MarkdownIt();
  for (const hint of context.images ?? []) {
    const text = points.slice(hint.start,hint.end).join('');
    const children = parser.parseInline(text, {})[0]?.children;
    const token = children?.length === 1 && children[0].type === 'image' ? children[0] : undefined;
    const url = token && writerSafeUrl(token.attrGet('src') ?? '');
    if (!url || !Number.isInteger(hint.width) || hint.width < 1 || hint.width > 10000 ||
        (hint.height != null && (!Number.isInteger(hint.height) || hint.height < 1 || hint.height > 10000))) continue;
    edits.push({...hint,value:`<img src="${escape(url)}" alt="${escape(token!.content)}" width="${hint.width}"${hint.height ? ` height="${hint.height}"` : ''}>`});
  }
  edits.sort((a,b)=>a.start-b.start);
  let end = 0;
  for (const edit of edits) {
    if (!Number.isInteger(edit.start) || !Number.isInteger(edit.end) || edit.start < end || edit.end <= edit.start || edit.end > points.length) return source;
    end = edit.end;
  }
  for (const edit of edits.reverse()) points.splice(edit.start,edit.end-edit.start,edit.value);
  return points.join('');
}
