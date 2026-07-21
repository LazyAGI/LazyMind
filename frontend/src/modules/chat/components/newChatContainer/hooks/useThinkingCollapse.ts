import { useCallback, useState } from "react";

export function useThinkingCollapse() {
  const [thinkingCollapseMap, setThinkingCollapseMap] = useState<
    Map<string, boolean>
  >(new Map());

  const toggleThinkingCollapse = useCallback((key: string, currentCollapsed = false) => {
    setThinkingCollapseMap((prev) => {
      const newMap = new Map(prev);
      newMap.set(key, !currentCollapsed);
      return newMap;
    });
  }, []);

  const collapseAllThinking = useCallback(() => {
    setThinkingCollapseMap((prev) => {
      const next = new Map(prev);
      for (const key of next.keys()) next.set(key, true);
      return next;
    });
  }, []);

  const isThinkingCollapsed = useCallback(
    (key: string, defaultCollapsed = false) =>
      thinkingCollapseMap.get(key) ?? defaultCollapsed,
    [thinkingCollapseMap],
  );

  return {
    thinkingCollapseMap,
    toggleThinkingCollapse,
    isThinkingCollapsed,
    collapseAllThinking,
  };
}
