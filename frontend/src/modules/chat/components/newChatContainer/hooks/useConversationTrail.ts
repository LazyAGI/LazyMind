import { useEffect, useState } from "react";
import { ChatServiceApi } from "@/modules/chat/utils/request";
import type { ConversationTrailRecord } from "@/modules/chat/utils/message";

interface UseConversationTrailOptions {
  conversationId?: string;
  refreshKey?: string | number;
  enabled?: boolean;
}

export function useConversationTrail({
  conversationId = "",
  refreshKey = "",
  enabled = true,
}: UseConversationTrailOptions) {
  const [items, setItems] = useState<ConversationTrailRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [retryKey, setRetryKey] = useState(0);

  useEffect(() => {
    if (!enabled || !conversationId || conversationId.startsWith("temp_")) {
      setItems([]);
      setLoading(false);
      setError(null);
      return;
    }

    const controller = new AbortController();
    let cancelled = false;
    setLoading(true);
    setError(null);

    const loadAllPages = async () => {
      const allItems: ConversationTrailRecord[] = [];
      let pageToken = "";
      do {
        const response = await ChatServiceApi().conversationServiceGetConversationTrail(
          {
            name: conversationId,
            pageSize: 100,
            ...(pageToken ? { pageToken } : {}),
          },
          { signal: controller.signal },
        );
        allItems.push(...((response.data?.items ?? []) as ConversationTrailRecord[]));
        const nextPageToken = response.data?.next_page_token || "";
        if (!nextPageToken || nextPageToken === pageToken) {
          break;
        }
        pageToken = nextPageToken;
      } while (!cancelled);
      return allItems;
    };

    loadAllPages()
      .then((nextItems) => {
        if (cancelled) {
          return;
        }
        setItems(nextItems);
      })
      .catch((requestError) => {
        const errorName = (requestError as { name?: string } | null)?.name;
        if (cancelled || errorName === "CanceledError") {
          return;
        }
        setItems([]);
        setError(requestError instanceof Error ? requestError : new Error("load trail failed"));
      })
      .finally(() => {
        if (!cancelled) {
          setLoading(false);
        }
      });

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, [conversationId, enabled, refreshKey, retryKey]);

  return {
    items,
    loading,
    error,
    retry: () => setRetryKey((value) => value + 1),
  };
}
