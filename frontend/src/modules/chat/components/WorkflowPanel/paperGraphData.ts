export type PaperGraphData = {
  nodes: { id: string; label: string; tone: string }[];
  edges: { id: string; source: string; target: string; label: string; description?: string; relationType?: string; directed: boolean; dashed: boolean; status?: string }[];
};

export const paperRelationColors: Record<string, string> = {
  mechanism_comparison: '#4875c6', experimental_comparison: '#c57a35',
  design_inspiration: '#ad8739', component_use: '#438c79',
  method_modification: '#8964b2', background_citation: '#82909d',
};
