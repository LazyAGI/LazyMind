import type { SlotFooterAction } from './slotEditingContext';

const DOCUMENT_FOOTER_LINK_ORDER = 20;

type DocumentFooterItem =
  | { kind: 'button'; key: string; order: number; action: SlotFooterAction }
  | { kind: 'link'; key: string; order: number; href: string; label: string };

export function buildDocumentFooterItems(footerActions: Map<string, SlotFooterAction>): {
  statusMessages: Array<{ key: string; text: string; tone?: SlotFooterAction['statusTone'] }>;
  actionItems: DocumentFooterItem[];
} {
  const statusMessages: Array<{ key: string; text: string; tone?: SlotFooterAction['statusTone'] }> = [];
  const actionItems: DocumentFooterItem[] = [];

  // Keep the same owner when a hook re-registers after a format/busy-state change.
  const owners = new Map<string, string>();
  for (const [key, action] of footerActions.entries()) {
    if (action.dedupKey && (!owners.has(action.dedupKey) || key < owners.get(action.dedupKey)!)) {
      owners.set(action.dedupKey, key);
    }
  }
  for (const [key, action] of footerActions.entries()) {
    if (action.dedupKey && owners.get(action.dedupKey) !== key) continue;
    if (action.statusText) {
      statusMessages.push({ key: `${key}:status`, text: action.statusText, tone: action.statusTone });
    }
    if (action.statusLink) {
      actionItems.push({
        kind: 'link',
        key: `${key}:link`,
        order: DOCUMENT_FOOTER_LINK_ORDER,
        href: action.statusLink.href,
        label: action.statusLink.label,
      });
    }
    actionItems.push({
      kind: 'button',
      key,
      order: action.order ?? 100,
      action,
    });
  }

  actionItems.sort((left, right) => left.order - right.order);
  return { statusMessages, actionItems };
}
