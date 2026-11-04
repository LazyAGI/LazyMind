/** Only application-local destinations may be used for repair and return links. */
export function localWorkflowRepairPath(value?: string | null): string | undefined {
  if (!value?.startsWith('/') || value.startsWith('//')) return undefined;
  const target = new URL(value, window.location.origin);
  if (target.origin !== window.location.origin) return undefined;
  return target.pathname + target.search + target.hash;
}
