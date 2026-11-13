import { useCallback, useState } from 'react';

export type WriterPageWidth = 'default' | 'wide' | 'reading';
const STORAGE_KEY = 'writer-page-width';

function preferredWidth(): WriterPageWidth {
  try {
    const value = localStorage.getItem(STORAGE_KEY);
    if (value === 'wide' || value === 'reading') return value;
  } catch { /* Browser storage may be unavailable; sizing still works locally. */ }
  return 'default';
}

export function useWriterPageWidth() {
  const [width, setWidth] = useState(preferredWidth);
  const selectWidth = useCallback((value: WriterPageWidth) => {
    setWidth(value);
    try { localStorage.setItem(STORAGE_KEY, value); } catch { /* Optional preference. */ }
  }, []);
  return [width, selectWidth] as const;
}
