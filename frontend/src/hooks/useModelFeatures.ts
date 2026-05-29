import { useEffect, useState } from 'react';
import { axiosInstance, BASE_URL } from '@/components/request';

export interface ModelFeatures {
  image_embed_enabled: boolean;
  image_embed_required?: boolean;
}

export const MODEL_FEATURES_CHANGED_EVENT = 'lazymind:model-features-changed';

type FeaturesState =
  | { status: 'loading' }
  | { status: 'ready'; features: ModelFeatures }
  | { status: 'error' };

// Module-level cache: fetched at most once per page load unless invalidated.
let cachedFeatures: ModelFeatures | null = null;
let pendingPromise: Promise<ModelFeatures> | null = null;

export function isImageEmbedRequired(features: ModelFeatures): boolean {
  return features.image_embed_required === true;
}

export function invalidateModelFeaturesCache(): void {
  cachedFeatures = null;
  pendingPromise = null;
}

export function notifyModelFeaturesChanged(): void {
  invalidateModelFeaturesCache();
  window.dispatchEvent(new Event(MODEL_FEATURES_CHANGED_EVENT));
}

export function fetchModelFeatures(force = false): Promise<ModelFeatures> {
  if (!force && cachedFeatures !== null) {
    return Promise.resolve(cachedFeatures);
  }
  if (!force && pendingPromise !== null) {
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
      const fallback: ModelFeatures = {
        image_embed_enabled: true,
        image_embed_required: false,
      };
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
    const syncFromCache = () => {
      if (cachedFeatures !== null) {
        setState({ status: 'ready', features: cachedFeatures });
      }
    };

    const reload = () => {
      setState({ status: 'loading' });
      fetchModelFeatures(true).then((features) => {
        setState({ status: 'ready', features });
      });
    };

    syncFromCache();
    if (cachedFeatures === null) {
      reload();
    }

    window.addEventListener(MODEL_FEATURES_CHANGED_EVENT, reload);
    return () => {
      window.removeEventListener(MODEL_FEATURES_CHANGED_EVENT, reload);
    };
  }, []);

  return state;
}
