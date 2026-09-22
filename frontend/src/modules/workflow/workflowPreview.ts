// Stored layout is separate from the definition; join it only for rendering.
export function withWorkflowLayout(stateYaml: string, layoutJson?: string): string {
  if (!layoutJson) return stateYaml;
  try {
    const layout: unknown = JSON.parse(layoutJson);
    if (!layout || typeof layout !== "object" || Array.isArray(layout) || !Object.keys(layout).length) return stateYaml;
    return `x-layout:\n${Object.entries(layout).map(([id, value]) => `  ${JSON.stringify(id)}: ${JSON.stringify(value)}`).join("\n")}\n${stateYaml}`;
  } catch {
    return stateYaml;
  }
}
