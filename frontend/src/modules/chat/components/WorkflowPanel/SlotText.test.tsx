import { render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import type { SlotRevision } from '@/modules/chat/store/workflowPanel';
vi.mock('./FilePreviewDrawer', () => ({ FilePreviewDrawer: () => null }));
vi.mock('./MarkdownArtifactEditor', () => ({
  MarkdownArtifactEditor: ({ markdown }: { markdown: string }) => <div data-testid='markdown-editor'>{markdown}</div>,
}));
import { SlotRenderer } from './SlotComponents';

it('preserves MCP text and newlines in the existing Markdown editor without JSON quoting', () => {
  const slot: SlotRevision = { slot_id: 'slide_outline', slot: 'slide_outline', revision: 1, selected: true,
    created_at: '2026-09-08T00:00:00Z', change_source: 'host', content_type: 'text',
    artifact_value: '# Slide title\n\nBody', list_index: 0 };
  render(<SlotRenderer slot={slot} widget={{widgetType: 'text-markdown'}}
    sessionId='mcp-text-test' slotId='slide_outline' revisionCount={1} />);
  expect(screen.getByTestId('markdown-editor').textContent).toBe('# Slide title\n\nBody');
});
