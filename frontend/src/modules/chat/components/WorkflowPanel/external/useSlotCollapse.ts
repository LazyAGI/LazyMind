import { useState } from 'react';

/** Follow content availability until the user explicitly chooses a display state. */
export function useSlotCollapse(hasContent: boolean, collapseWhenEmpty: boolean, initiallyCollapsed = false) {
  const [choice, setChoice] = useState<boolean>();
  const collapsed = choice ?? (initiallyCollapsed || collapseWhenEmpty && !hasContent);
  return [collapsed, () => setChoice(!collapsed)] as const;
}
