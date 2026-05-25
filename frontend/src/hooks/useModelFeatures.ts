import { useEffect, useState } from 'react';
import { axiosInstance, BASE_URL } from '@/components/request';

export interface ModelFeatures {
  image_embed_enabled: boolean;
}

type FeaturesState =
  | { status: 'loading' }
  | { status: 'ready'; features: ModelFeatures }
  | { status: 'error' };

// Module-level cache: fetched at most once per page load.
let cachedFeatures: ModelFeatures | null = null;
let pendingPromise: Promise<ModelFeatures> | null = null;

export function fetchModelFeatures(): Promise<ModelFeatures> {
  if (cachedFeatures !== null) {
    return Promise.resolve(cachedFeatures);
  }
  if (pendingPromise !== null) {
    return pendingPromise;
  }
  pendingPromise = axiosInstance
    .get<{ data?: ModelFeatures } | ModelFeatures>(
      `${BASE_URL}/api/core/model_providers/features`,
    )
    .then((resp) => {
      const body = resp.data;
      const features: ModelFeatures =
        body && typeof body === 'object' && 'data' in body && body.data
          ? (body as { data: ModelFeatures }).data
          : (body as ModelFeatures);
      cachedFeatures = features;
      return features;
    })
    .catch(() => {
      // On error, default to enabled so existing behaviour is preserved.
      const fallback: ModelFeatures = { image_embed_enabled: true };
      cachedFeatures = fallback;
      return fallback;
    })
    .finally(() => {
      pendingPromise = null;
    });
  return pendingPromise;
}

/**
 * Returns model feature flags from GET /api/core/model_providers/features.
 * The result is cached at module level after the first successful fetch.
 */
export function useModelFeatures(): FeaturesState {
  const [state, setState] = useState<FeaturesState>(() =>
    cachedFeatures !== null
      ? { status: 'ready', features: cachedFeatures }
      : { status: 'loading' },
  );

  useEffect(() => {
    if (cachedFeatures !== null) {
      setState({ status: 'ready', features: cachedFeatures });
      return;
    }
    let cancelled = false;
    fetchModelFeatures().then((features) => {
      if (!cancelled) {
        setState({ status: 'ready', features });
      }
    });
    return () => {
      cancelled = true;
    };
  }, []);

  return state;
}
