import { useCallback, useEffect, useState } from "react";
import i18n from "@/i18n";
import { ensureLocalSession, isLocalSessionEnabled } from "./localSession";

export interface LocalSessionGateState {
  enabled: boolean;
  loading: boolean;
  error: string;
  retry: () => Promise<void>;
}

export function useLocalSessionGate(
  refreshLayoutUser: () => Promise<void>,
): LocalSessionGateState {
  const enabled = isLocalSessionEnabled();
  const [loading, setLoading] = useState(enabled);
  const [error, setError] = useState("");

  const restore = useCallback(
    async (force = false) => {
      setLoading(true);
      setError("");
      try {
        await ensureLocalSession({ force });
        await refreshLayoutUser();
      } catch (restoreError: any) {
        console.error("Failed to restore local admin session:", restoreError);
        setError(i18n.t("errors.2000509"));
      } finally {
        setLoading(false);
      }
    },
    [refreshLayoutUser],
  );

  useEffect(() => {
    if (!enabled) {
      setLoading(false);
      return;
    }

    let cancelled = false;
    setLoading(true);
    setError("");
    void ensureLocalSession()
      .then(() => {
        if (!cancelled) {
          return refreshLayoutUser();
        }
        return undefined;
      })
      .catch((restoreError: any) => {
        if (!cancelled) {
          console.error("Failed to restore local admin session:", restoreError);
          setError(i18n.t("errors.2000509"));
        }
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
    };
  }, [enabled, refreshLayoutUser]);

  const retry = useCallback(() => restore(true), [restore]);

  return {
    enabled,
    loading,
    error,
    retry,
  };
}
