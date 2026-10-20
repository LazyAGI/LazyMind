import type { SlotFooterAction } from './slotEditingContext';

// Keep every format and owner callback, including its save/disabled behavior.
export function groupDownloadActions(
  actions: Map<string, SlotFooterAction>,
  title: (key: string) => string,
  execute: (action: SlotFooterAction, callback: () => void) => void,
): Map<string, SlotFooterAction> {
  const downloads = [...actions].filter(([, action]) => action.icon === 'download')
    .sort(([a], [b]) => a.localeCompare(b));
  if (downloads.length < 2) return actions;
  const result = new Map(actions);
  downloads.forEach(([key]) => result.delete(key));
  const [, first] = downloads.find(([, action]) => !action.disabled) ?? downloads[0];
  result.set('workflow:download', {
    ...first, flushBeforeAction: false, selectedMenuKey: undefined,
    disabled: downloads.every(([, action]) => action.disabled),
    onClick: () => execute(first, first.onClick),
    menu: downloads.flatMap(([key, action]) => (action.menu ?? [{ key: 'default', label: action.label, onClick: action.onClick }])
      .map(option => ({ ...option, key: `${key}:${option.key}`,
        label: `${title(key)} · ${typeof option.label === 'string' ? option.label : action.label}`,
        disabled: action.disabled,
        onClick: () => { if (!action.disabled) execute(action, option.onClick); },
      }))),
  });
  return result;
}
