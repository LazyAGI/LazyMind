export function documentPublicationUrl(value: unknown, provider?: string): string | undefined {
  if (typeof value !== 'string') return undefined;
  try {
    const url = new URL(value);
    if (url.username || url.password) return undefined;
    if (provider === 'wechat' && (url.host !== 'mp.weixin.qq.com' || !(url.pathname === '/s' || url.pathname.startsWith('/s/')))) return undefined;
    if (['https:', 'http:'].includes(url.protocol)) return url.href;
    if (url.protocol === 'obsidian:' && url.host === 'open' && !url.pathname && !url.hash
      && [...url.searchParams.keys()].length === 1 && url.searchParams.has('path')
      && isAbsoluteNotePath(url.searchParams.get('path'))) return url.href;
  } catch { /* The provider did not return an accessible document URL. */ }
  return undefined;
}

function isAbsoluteNotePath(value: unknown): value is string {
  return typeof value === 'string' && /^(\/|[A-Za-z]:[\\/]|\\\\)/.test(value) && !/[\u0000-\u001f\u007f]/.test(value);
}

export function documentPublicationTargetUrl(target: { uri?: unknown; doc_id?: unknown; meta?: { pull_request_url?: unknown; browser_url?: unknown; local_path?: unknown } } | undefined, provider?: string): string | undefined {
  for (const candidate of [target?.meta?.pull_request_url, target?.meta?.browser_url, target?.uri]) {
    const url = documentPublicationUrl(candidate, provider);
    if (url) return url;
  }
  if (provider === 'obsidian' && isAbsoluteNotePath(target?.meta?.local_path)) {
    return documentPublicationUrl(`obsidian://open?path=${encodeURIComponent(target.meta.local_path)}`);
  }
  const uri = typeof target?.uri === 'string' ? target.uri : '';
  const documentId = typeof target?.doc_id === 'string' ? target.doc_id : '';
  if (provider === 'feishu') {
    const parts = /^feishu(?:@[^:/]+)?:\/{1,2}~(docx|doc|node)\/([A-Za-z0-9]+)\/?$/.exec(uri);
    if (parts) {
      const kind = parts[1] === 'node' ? 'wiki' : parts[1] === 'doc' ? 'docs' : 'docx';
      return `https://feishu.cn/${kind}/${parts[2]}`;
    }
    if (!uri && /^[A-Za-z0-9]+$/.test(documentId)) return `https://feishu.cn/docx/${documentId}`;
  }
  if (provider === 'notion' && (!uri || uri.startsWith('notion:/~page/'))) {
    const page = uri ? uri.slice('notion:/~page/'.length) : documentId;
    if (/^(?:[a-f0-9]{32}|[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12})$/i.test(page)) return `https://www.notion.so/${page.replace(/-/g, '')}`;
  }
  return undefined;
}
