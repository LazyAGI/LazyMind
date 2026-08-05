import { createContext } from 'react';
import i18n from '@/i18n';

export function tr(key: string, options?: Record<string, unknown>): string {
  return i18n.t(key, options);
}

/** When false, slot renderers hide download/export affordances. */
export const SlotDownloadContext = createContext(true);

/** Shown when the slot has no artifact yet (backend returned no artifact_value). */
export function SlotPending({ type, cardMode }: { type: 'image' | 'file' | 'text'; cardMode?: boolean }) {
  if (type === 'image') {
    return (
      <div className={`plugin-slot plugin-slot--image plugin-slot--pending${cardMode ? ' plugin-slot--image-card' : ''}`}>
        <span className='plugin-slot__placeholder-icon' aria-hidden='true'>🖼</span>
        <span className='plugin-slot__placeholder'>{tr('chat.slots.inProgress')}</span>
      </div>
    );
  }
  if (type === 'file') {
    return (
      <div className='plugin-slot plugin-slot--file plugin-slot--pending'>
        <span className='plugin-slot__placeholder'>{tr('chat.slots.pendingGeneration')}</span>
      </div>
    );
  }
  return (
    <div className='plugin-slot plugin-slot--text plugin-slot--pending'>
      <p className='plugin-slot__text plugin-slot__text--pending'>{tr('chat.slots.pendingCalculation')}</p>
    </div>
  );
}
