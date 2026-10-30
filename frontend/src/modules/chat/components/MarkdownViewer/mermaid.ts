let initialized = false;

export async function getMermaid() {
  const { default: mermaid } = await import('mermaid');
  if (!initialized) {
    mermaid.initialize({ startOnLoad: false, securityLevel: 'strict', theme: 'default' });
    initialized = true;
  }
  return mermaid;
}
