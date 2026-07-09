import { useCallback, useEffect, useState } from "react";
import { AgentAppsAuth } from "@/components/auth";
import { ensureDesktopSession, isDesktopSessionEnabled } from "./desktopSession";

export interface DesktopSessionGateState {
  enabled: boolean;
  loading: boolean;
  error: string;
  retry: () => Promise<void>;
}

export function useDesktopSessionGate(
  isLoggedIn: boolean,
  refreshLayoutUser: () => Promise<void>,
): DesktopSessionGateState {
  const enabled = isDesktopSessionEnabled();
  const [loading, setLoading] = useState(() => enabled && !AgentAppsAuth.isLoggedIn());
  const [error, setError] = useState("");

  const restore = useCallback(
    async (force = false) => {
      setLoading(true);
      setError("");
      try {
        await ensureDesktopSession({ force });
        await refreshLayoutUser();
      } catch (restoreError: any) {
        console.error("Failed to restore desktop admin session:", restoreError);
        setError(restoreError?.message || "Desktop session could not be restored.");
      } finally {
        setLoading(false);
      }
    },
    [refreshLayoutUser],
  );

  useEffect(() => {
    if (!enabled || isLoggedIn) {
      setLoading(false);
      return;
    }

    let cancelled = false;
    setLoading(true);
    setError("");
    void ensureDesktopSession()
      .then(() => {
        if (!cancelled) {
          return refreshLayoutUser();
        }
        return undefined;
      })
      .catch((restoreError: any) => {
        if (!cancelled) {
          console.error("Failed to restore desktop admin session:", restoreError);
          setError(restoreError?.message || "Desktop session could not be restored.");
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
  }, [enabled, isLoggedIn, refreshLayoutUser]);

  const retry = useCallback(() => restore(true), [restore]);

  return {
    enabled,
    loading,
    error,
    retry,
  };
}
