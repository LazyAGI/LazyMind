/** Preserve only a local embedded run route after its own browser signs in. */
export function workflowRunLoginReturnPath(value: unknown): string | undefined {
  if (typeof value === 'string' && /^\/workflow-runs\/[a-zA-Z0-9_-]{1,256}\/embed(?:\?[^#\r\n]*)?$/.test(value)) {
    return value;
  }
  return undefined;
}
