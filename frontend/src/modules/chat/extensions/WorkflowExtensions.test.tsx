import { render, screen, cleanup } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { WorkflowSession } from '../store/workflowPanel';
import { WorkflowExtensions } from './WorkflowExtensions';
vi.mock('../components/WorkflowPanel/ProductProject', () => ({
  ProductProject: ({ session }: { session: WorkflowSession }) => <div data-testid="project">{session.workflow_id}</div>,
}));
afterEach(cleanup);
const props = { disabled: false, beforeAction: async () => true, onRefresh: () => {} };
it('selects installed controls by capability for a copied package', () => {
  render(<WorkflowExtensions {...props} session={{ session_id: 's', workflow_id: 'copied-package' } as WorkflowSession} names={['product-project-v1']} />);
  expect(screen.getByTestId('project').textContent).toBe('copied-package');
});
it('keeps undeclared packages free of domain controls even with the original name', () => {
  render(<WorkflowExtensions {...props} session={{ session_id: 's', workflow_id: 'product_solution_delivery' } as WorkflowSession} />);
  expect(screen.queryByTestId('project')).toBeNull();
});
it('reports a missing installed extension', () => {
  render(<WorkflowExtensions {...props} session={{ session_id: 's' } as WorkflowSession} names={['unknown']} />);
  expect(screen.getByRole('alert').textContent).toContain('unknown');
});
