import { useCallback, useLayoutEffect, useRef } from "react";
import { useLocation, useNavigate } from "react-router-dom";

export const KNOWLEDGE_LIST_PATH = "/lib/knowledge/list";

interface ListScroll {
  url: string;
  top: number;
  left: number;
}

interface ListOrigin {
  url: string;
  scroll: ListScroll;
}

function readScroll(value: unknown): ListScroll | undefined {
  if (!value || typeof value !== "object") return;
  const scroll = value as ListScroll;
  if (typeof scroll.url === "string" && Number.isFinite(scroll.top) && scroll.top >= 0 &&
      Number.isFinite(scroll.left) && scroll.left >= 0) return scroll;
}

export function getKnowledgeListOrigin(state: unknown): ListOrigin | undefined {
  if (!state || typeof state !== "object") return;
  const origin = (state as { knowledgeListOrigin?: ListOrigin }).knowledgeListOrigin;
  if (!origin || typeof origin.url !== "string") return;
  // Return targets are restricted to this list, including its query and hash.
  if (!/^\/lib\/knowledge\/list(?:[?#]|$)/.test(origin.url)) return;
  const scroll = readScroll(origin.scroll);
  if (scroll?.url === origin.url) return { url: origin.url, scroll };
}

export function useKnowledgeListNavigation(ready: boolean) {
  const navigate = useNavigate();
  const location = useLocation();
  const rootRef = useRef<HTMLDivElement>(null);
  const restoredEntry = useRef<string>();
  const currentLocation = useRef(location);
  currentLocation.current = location;

  const openDetail = useCallback((datasetId: string) => {
    const current = currentLocation.current;
    const root = rootRef.current;
    const url = current.pathname + current.search + current.hash;
    const scroll: ListScroll = {
      url,
      top: root?.closest(".sub-app-container")?.scrollTop || 0,
      left: root?.querySelector(".list-page-table-scroll")?.scrollLeft || 0,
    };
    // Save on the list entry BEFORE pushing detail, so native browser Back also works.
    navigate(url, {
      replace: true,
      state: { ...current.state, knowledgeListScroll: scroll },
      preventScrollReset: true,
    });
    navigate(`/lib/knowledge/detail/${encodeURIComponent(datasetId)}`, {
      state: { knowledgeListOrigin: { url, scroll } satisfies ListOrigin },
    });
  }, [navigate]);

  useLayoutEffect(() => {
    if (!ready || restoredEntry.current === location.key) return;
    const scroll = readScroll(location.state?.knowledgeListScroll);
    if (!scroll || scroll.url !== location.pathname + location.search + location.hash) return;
    const container = rootRef.current?.closest(".sub-app-container");
    if (!container) return;
    container.scrollTop = Math.min(scroll.top, Math.max(0, container.scrollHeight - container.clientHeight));
    const table = rootRef.current?.querySelector(".list-page-table-scroll");
    if (table) table.scrollLeft = Math.min(scroll.left, Math.max(0, table.scrollWidth - table.clientWidth));
    restoredEntry.current = location.key;
  }, [ready, location]);

  return { rootRef, openDetail };
}

export function useKnowledgeListReturn() {
  const navigate = useNavigate();
  const location = useLocation();
  const origin = getKnowledgeListOrigin(location.state);

  const returnToList = (afterDelete = false) => {
    if (origin && !afterDelete) {
      navigate(-1);
    } else {
      navigate(origin?.url || KNOWLEDGE_LIST_PATH, {
        replace: true,
        state: origin ? { knowledgeListScroll: origin.scroll } : undefined,
      });
    }
  };
  return { returnToList, listUrl: origin?.url || KNOWLEDGE_LIST_PATH };
}
