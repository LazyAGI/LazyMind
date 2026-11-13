const PREFIX = '#lazymind-local-file=';

interface Node {
  type: string;
  value?: string;
  url?: string;
  children?: Node[];
}

export function localFilePath(value: string): string {
  let path = value;
  const explicitFile = value.startsWith(PREFIX) || /^file:\/\//i.test(value);
  if (path.startsWith(PREFIX)) {
    try { path = decodeURIComponent(path.slice(PREFIX.length)); } catch { return ''; }
  } else if (/^file:\/\//i.test(path)) {
    try {
      const url = new URL(path);
      if (url.hostname && url.hostname !== 'localhost') return '';
      path = decodeURIComponent(url.pathname);
      if (/^\/[A-Za-z]:\//.test(path)) path = path.slice(1);
    } catch { return ''; }
  } else {
    try { path = decodeURIComponent(path); } catch { return ''; }
  }
  path = path.replace(/(?::\d+(?::\d+)?|#L\d+(?:C\d+)?(?:-L?\d+(?:C\d+)?)?)$/, '');
  if (/[\u0000-\u001f\u007f]/.test(path)) return '';
  // Root-relative application routes are not filesystem links.
  if (explicitFile && /^\/(?!\/)/.test(path)) return path;
  return /^(?:\/(?:Users|home|tmp|private|var|Volumes|mnt|opt|etc|data|root|srv|usr|workspace|workspaces)\/|[A-Za-z]:[\\/])/.test(path) ? path : '';
}

export default function remarkLocalFiles() {
  return function transform(node: Node) {
    if (node.type === 'link' || node.type === 'definition') {
      const path = localFilePath(node.url || '');
      if (path) node.url = PREFIX + encodeURIComponent(path);
      return;
    }
    if (!node.children || node.type === 'code' || node.type === 'inlineCode') return;
    node.children = node.children.flatMap(child => {
      if (child.type !== 'text') { transform(child); return [child]; }
      const value = child.value || '';
      const pattern = /:{1,2}codex-file-citation\{([^{}]*)\}/g;
      const result: Node[] = [];
      let start = 0;
      for (const match of value.matchAll(pattern)) {
        const raw = /\bpath="([^"]+)"/.exec(match[1])?.[1];
        const path = raw && localFilePath(PREFIX + encodeURIComponent(raw));
        if (!path) continue;
        result.push({ type: 'text', value: value.slice(start, match.index) });
        result.push({ type: 'link', url: PREFIX + encodeURIComponent(path), children: [
          { type: 'text', value: path.split(/[\\/]/).pop() || path },
        ] });
        start = match.index! + match[0].length;
      }
      return result.length ? [...result, { type: 'text', value: value.slice(start) }] : [child];
    });
  };
}
