import { AgentAppsAuth } from '@/components/auth';
import { BASE_URL, localizeErrorCode } from '@/components/request';
import { normalizeProxyableUrl } from '@/modules/knowledge/utils/request';

const IMAGE_MD_RE = /!\[(.*?)\]\((.*?)\)/g;
const UPLOAD_ROOT_MARKER = '/var/lib/lazymind/uploads/';
const SUBAGENT_ROOT_MARKER = '/data/subagent/';
const signCache = new Map<string, string>();
const signInflight = new Map<string, Promise<string>>();

function extractStaticFilesPath(raw: string): string {
  const trimmed = (raw || '').trim();
  const marker = '/static-files/';
  const idx = trimmed.indexOf(marker);
  if (idx < 0) {
    return '';
  }
  return trimmed.slice(idx).split('#')[0];
}

function staticFilesStorageKey(staticPath: string): string {
  return staticPath.split('?')[0];
}

function cacheKeyForUploadPath(uploadPath: string): string {
  if (uploadPath.startsWith('/static-files/')) {
    return staticFilesStorageKey(uploadPath);
  }
  return uploadPath;
}

function signRequestPath(uploadPath: string): string {
  return cacheKeyForUploadPath(uploadPath);
}

function parseExpires(url: string): number {
  const match = (url || '').match(/[?&]expires=(\d+)/);
  if (!match) {
    return 0;
  }
  const value = Number(match[1]);
  return Number.isFinite(value) ? value : 0;
}

export function isExpiredSignedUrl(url: string): boolean {
  const expires = parseExpires(url);
  if (!expires) {
    return false;
  }
  return Date.now() >= (expires - 30) * 1000;
}

export function basenameFromPath(path: string): string {
  const withoutQuery = path.split('?')[0] || path;
  const parts = withoutQuery.split('/');
  return parts[parts.length - 1] || withoutQuery;
}

function extractUploadPath(raw: string): string {
  const trimmed = (raw || '').trim();
  if (!trimmed) {
    return '';
  }
  const staticPath = extractStaticFilesPath(trimmed);
  if (staticPath) {
    return staticPath;
  }
  const markerIndex = trimmed.indexOf(UPLOAD_ROOT_MARKER);
  if (markerIndex >= 0) {
    return trimmed.slice(markerIndex).split('?', 1)[0];
  }
  if (trimmed.startsWith(UPLOAD_ROOT_MARKER)) {
    return trimmed.split('?', 1)[0];
  }
  const subIdx = trimmed.indexOf(SUBAGENT_ROOT_MARKER);
  if (subIdx >= 0) {
    return trimmed.slice(subIdx);
  }
  if (trimmed.startsWith(SUBAGENT_ROOT_MARKER)) {
    return trimmed;
  }
  return trimmed;
}

export function resolveCoreAssetUrl(path?: string): string {
  const normalized = extractUploadPath(path || '');
  if (!normalized) {
    return '';
  }

  if (/^https?:\/\//i.test(normalized)) {
    return normalizeProxyableUrl(normalized);
  }

  if (normalized.startsWith('/api/core/')) {
    const origin =
      typeof window !== 'undefined' ? window.location.origin : '';
    return normalizeProxyableUrl(`${origin}${normalized}`);
  }

  if (normalized.startsWith('/static-files/')) {
    const origin =
      typeof window !== 'undefined' ? window.location.origin : '';
    return normalizeProxyableUrl(`${origin}/api/core${normalized}`);
  }

  return normalized;
}

async function signUploadPaths(paths: string[]): Promise<Record<string, string>> {
  const requestPaths = paths.map((path) => signRequestPath(path));
  const pending = requestPaths.filter((path) => path && !signCache.has(path));
  if (!pending.length) {
    return Object.fromEntries(
      paths.map((path) => [path, signCache.get(signRequestPath(path)) || '']),
    );
  }

  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...AgentAppsAuth.getAuthHeaders(),
  };

  const response = await fetch(`${BASE_URL}/api/core/static-files:sign`, {
    method: 'POST',
    headers,
    body: JSON.stringify({ paths: pending }),
  });

  if (!response.ok) {
    throw new Error(localizeErrorCode('2000509'));
  }

  const data = (await response.json()) as { urls?: Record<string, string> };
  const urls = data.urls || {};
  Object.entries(urls).forEach(([path, signed]) => {
    if (signed) {
      signCache.set(path, signed);
    }
  });
  return urls;
}

export async function resolveMarkdownImageUrlAsync(
  url: string,
): Promise<string> {
  const trimmed = (url || '').trim();
  if (!trimmed || trimmed.startsWith('data:')) {
    return trimmed;
  }
  if (
    /^https?:\/\//i.test(trimmed) &&
    !trimmed.includes(UPLOAD_ROOT_MARKER) &&
    !trimmed.includes('/static-files/')
  ) {
    return normalizeProxyableUrl(trimmed);
  }

  const uploadPath = extractUploadPath(trimmed);
  if (!uploadPath) {
    return trimmed;
  }

  const cacheKey = cacheKeyForUploadPath(uploadPath);
  if (signCache.has(cacheKey)) {
    const cached = signCache.get(cacheKey) || '';
    if (cached && !isExpiredSignedUrl(cached)) {
      return resolveCoreAssetUrl(cached);
    }
    signCache.delete(cacheKey);
  }

  if (!signInflight.has(cacheKey)) {
    signInflight.set(
      cacheKey,
      signUploadPaths([uploadPath])
        .then((urls) => urls[cacheKey] || '')
        .finally(() => {
          signInflight.delete(cacheKey);
        }),
    );
  }

  const signed = await signInflight.get(cacheKey);
  if (signed) {
    return resolveCoreAssetUrl(signed);
  }
  return trimmed;
}

function findMatchingImageKey(
  url: string,
  keys: string[],
): string | undefined {
  if (!keys.length) {
    return undefined;
  }
  const urlBase = basenameFromPath(url);

  for (const key of keys) {
    if (!key) {
      continue;
    }
    if (url === key || url.includes(key) || key.includes(url)) {
      return key;
    }
    if (basenameFromPath(key) === urlBase) {
      return key;
    }
  }
  return undefined;
}

export function resolveMarkdownImageUrl(
  url: string,
  imageKeys: string[] = [],
): string {
  const trimmed = (url || '').trim();
  if (!trimmed || trimmed.startsWith('data:')) {
    return trimmed;
  }

  if (trimmed.includes('/static-files/')) {
    return resolveCoreAssetUrl(trimmed);
  }

  const matchedKey = findMatchingImageKey(trimmed, imageKeys);
  if (matchedKey) {
    if (isExpiredSignedUrl(matchedKey)) {
      signCache.delete(cacheKeyForUploadPath(extractUploadPath(matchedKey)));
      return trimmed;
    }
    return resolveCoreAssetUrl(matchedKey);
  }

  if (/^https?:\/\//i.test(trimmed) && trimmed.includes(UPLOAD_ROOT_MARKER)) {
    return resolveCoreAssetUrl(trimmed);
  }

  return trimmed;
}

export function expandImagesInMarkdown(
  srcText: string,
  imageKeys: string[] = [],
): string {
  if (typeof srcText !== 'string' || !srcText) {
    return srcText;
  }

  return srcText.replace(IMAGE_MD_RE, (match, alt, url) => {
    const resolved = resolveMarkdownImageUrl(url, imageKeys);
    if (!resolved || resolved === url) {
      return match;
    }
    return `![${alt}](${resolved})`;
  });
}

export function collapseImagesToKeys(srcText: string, keys: string[]): string {
  if (typeof srcText !== 'string' || !Array.isArray(keys)) {
    return srcText;
  }

  return srcText.replace(IMAGE_MD_RE, (match, alt, url) => {
    const found = keys.find((k) => {
      if (!k) {
        return false;
      }
      const keyBase = basenameFromPath(k);
      return (
        url.indexOf(k) !== -1 ||
        url.indexOf(keyBase) !== -1 ||
        basenameFromPath(url) === keyBase
      );
    });
    if (found) {
      const storageKey = basenameFromPath(found) || found.split('?')[0];
      return `![${alt}](${storageKey})`;
    }
    return match;
  });
}
