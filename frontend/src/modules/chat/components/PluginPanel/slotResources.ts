import { useEffect, useState } from 'react';
import {
  isExpiredSignedUrl,
  resolveCoreAssetUrl,
  resolveMarkdownImageUrlAsync,
} from '@/modules/knowledge/utils/imageUrl';
import { isBrowserReadyImageUrl, preloadImageUrl } from './slotUtils';

const SLOT_IMAGE_PRELOAD_RETRIES = 4;
const SLOT_IMAGE_PRELOAD_RETRY_MS = 800;

/**
 * Resolve a slot image URL and preload it before display.
 * Avoids flashing a broken <img> when the API returns a signed URL before the file exists.
 */
export function useSlotImageUrl(raw: Record<string, unknown> | undefined) {
  const pathForSign = String(raw?.path ?? raw?.url ?? '').trim();
  const apiUrlRaw = raw?.url ? String(raw.url).trim() : '';
  const [displayUrl, setDisplayUrl] = useState('');
  const [pending, setPending] = useState(Boolean(pathForSign));

  useEffect(() => {
    if (!pathForSign) {
      setDisplayUrl('');
      setPending(false);
      return;
    }

    let cancelled = false;

    async function resolveCandidate(): Promise<string> {
      const apiUrl = apiUrlRaw ? resolveCoreAssetUrl(apiUrlRaw) : '';
      if (apiUrl && isBrowserReadyImageUrl(apiUrl) && !isExpiredSignedUrl(apiUrl)) {
        return apiUrl;
      }
      const signed = await resolveMarkdownImageUrlAsync(pathForSign);
      return isBrowserReadyImageUrl(signed) ? signed : '';
    }

    async function load() {
      setPending(true);
      setDisplayUrl('');
      let candidate = await resolveCandidate();
      if (!candidate || cancelled) {
        if (!cancelled) setPending(false);
        return;
      }

      for (let attempt = 0; attempt < SLOT_IMAGE_PRELOAD_RETRIES && !cancelled; attempt++) {
        if (await preloadImageUrl(candidate)) {
          if (!cancelled) {
            setDisplayUrl(candidate);
            setPending(false);
          }
          return;
        }
        if (attempt + 1 >= SLOT_IMAGE_PRELOAD_RETRIES) break;
        await new Promise((r) => setTimeout(r, SLOT_IMAGE_PRELOAD_RETRY_MS));
        candidate = await resolveMarkdownImageUrlAsync(pathForSign);
        if (!isBrowserReadyImageUrl(candidate)) break;
      }

      if (!cancelled) {
        setDisplayUrl('');
        setPending(false);
      }
    }

    load();
    return () => {
      cancelled = true;
    };
  }, [pathForSign, apiUrlRaw]);

  return { displayUrl, pending, hasSource: Boolean(pathForSign) };
}

export function useArtifactFileUrl(
  raw: Record<string, unknown> | undefined,
  refreshToken: string | number = 0,
) {
  const pathForSign = String(raw?.path ?? raw?.url ?? '').trim();
  const apiUrlRaw = raw?.url ? String(raw.url).trim() : '';
  const stablePath = raw?.path
    ? String(raw.path).trim()
    : apiUrlRaw.split(/[?#]/, 1)[0];
  const sourceKey = `${stablePath}\n${refreshToken}`;
  const [url, setUrl] = useState('');
  const [resolving, setResolving] = useState(Boolean(pathForSign));
  const [resolvedSourceKey, setResolvedSourceKey] = useState(
    pathForSign ? '' : sourceKey,
  );

  useEffect(() => {
    if (!pathForSign) {
      setUrl('');
      setResolving(false);
      setResolvedSourceKey(sourceKey);
      return;
    }

    let cancelled = false;
    setResolving(true);

    async function resolveCandidate(): Promise<string> {
      const apiUrl = apiUrlRaw ? resolveCoreAssetUrl(apiUrlRaw) : '';
      if (apiUrl && !isExpiredSignedUrl(apiUrl)) {
        return apiUrl;
      }
      return resolveMarkdownImageUrlAsync(pathForSign);
    }

    resolveCandidate()
      .then((resolved) => {
        if (!cancelled) {
          setUrl(resolved);
          setResolvedSourceKey(sourceKey);
          setResolving(false);
        }
      })
      .catch(() => {
        if (!cancelled) {
          setUrl('');
          setResolvedSourceKey(sourceKey);
          setResolving(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [apiUrlRaw, pathForSign, refreshToken, sourceKey]);

  const sourceResolved = resolvedSourceKey === sourceKey;
  return {
    url: sourceResolved ? url : '',
    resolving: resolving || !sourceResolved,
    hasSource: Boolean(pathForSign),
    sourceKey,
  };
}
