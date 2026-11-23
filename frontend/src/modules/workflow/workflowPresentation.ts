import type { WorkflowIconName } from './components/WorkflowIcon';

// Classify by the built-in workflow's identity, not its localized name or incidental keywords.
const builtinPresentation: Record<string, { icon: WorkflowIconName; priority?: number }> = {
  academic_research_pipeline: { icon: 'graduation-cap', priority: 4 },
  bid_tech_proposal_writer: { icon: 'clipboard-check' },
  'image-workflow': { icon: 'image' },
  'image-workflow-v2': { icon: 'images', priority: 1 },
  'paper-writing': { icon: 'file-pen-line', priority: 2 },
  paper_expansion: { icon: 'file-plus-2' },
  paper_ppt_with_reflection_mock: { icon: 'panels-top-left', priority: 5 },
  paper_workflow_mock: { icon: 'file-symlink' },
  'ppt-workflow': { icon: 'presentation', priority: 0 },
  product_solution_delivery: { icon: 'package-check' },
  'superpowers-brief-showcase': { icon: 'clipboard-list' },
  'superpowers-reliability-rc1': { icon: 'shield-check' },
  'test-workflow': { icon: 'flask-conical' },
  'writer-workflow': { icon: 'feather', priority: 3 },
};

export function getWorkflowIcon(builtinId?: string): WorkflowIconName {
  return (builtinId && builtinPresentation[builtinId]?.icon) || 'workflow';
}

export function getWorkflowPriority(builtinId?: string): number {
  return (builtinId ? builtinPresentation[builtinId]?.priority : undefined) ?? 6;
}
