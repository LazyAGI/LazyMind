import { useCallback, useState } from "react";

export function useThinkingCollapse() {
  const [thinkingCollapseMap, setThinkingCollapseMap] = useState<
    Map<string, boolean>
  >(new Map());

  const toggleThinkingCollapse = useCallback((key: string) => {
    setThinkingCollapseMap((prev) => {
      const newMap = new Map(prev);
      newMap.set(key, !(prev.get(key) || false));
      return newMap;
    });
  }, []);

  const isThinkingCollapsed = useCallback(
    (key: string) => thinkingCollapseMap.get(key) || false,
    [thinkingCollapseMap],
  );

  return {
    thinkingCollapseMap,
    toggleThinkingCollapse,
    isThinkingCollapsed,
  };
}
