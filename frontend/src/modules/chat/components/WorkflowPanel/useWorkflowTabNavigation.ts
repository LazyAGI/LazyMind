import { useState } from 'react';

interface NavigationTab { id: string; step_id?: string }

/** Runtime focus is only the default. A reading/navigation gesture owns focus afterwards. */
export function useWorkflowTabNavigation({ tabs, sessionId, currentStepId, terminal, persistedTab, persist }: {
  tabs: NavigationTab[];
  sessionId?: string;
  currentStepId?: string;
  terminal: boolean;
  persistedTab?: string;
  persist(tabId: string): void;
}) {
  const [choice, setChoice] = useState<{ sessionId?: string; tabId: string }>();
  const chosenTab = choice?.sessionId === sessionId ? choice?.tabId : undefined;
  let index = tabs.findIndex(tab => tab.id === (persistedTab ?? chosenTab));
  if (index < 0) index = tabs.findIndex(tab => (tab.step_id ?? tab.id) === currentStepId);
  if (index < 0) index = terminal ? Math.max(0, tabs.length - 1) : 0;
  const select = (tabId: string) => {
    if (!tabs.some(tab => tab.id === tabId)) return;
    if (chosenTab !== tabId) setChoice({ sessionId, tabId });
    if (persistedTab !== tabId) persist(tabId);
  };
  return { index, select, preserve: () => { if (tabs[index]) select(tabs[index].id); } };
}
