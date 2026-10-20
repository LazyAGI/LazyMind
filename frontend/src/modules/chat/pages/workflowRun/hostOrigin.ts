/** Electron registers this exact DSH origin as a standard, secure scheme. */
export function workflowHostOrigin(value: string | null): string | undefined {
  if (value === 'dsh-app://app' || value === 'dsh-app://app/') return 'dsh-app://app';
  try {
    const url = new URL(value ?? '');
    return ['http:', 'https:'].includes(url.protocol) ? url.origin : undefined;
  } catch { return undefined; }
}
