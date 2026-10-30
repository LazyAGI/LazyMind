import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { ConfigProvider } from 'antd';
import i18n from '@/i18n';
import MermaidBlock from './MermaidBlock';

vi.mock('mermaid', () => ({ default: { initialize: vi.fn(), parse: vi.fn(), render: async () => ({
  svg: '<svg><g class="node" id="rag-mermaid-123-1-flowchart-p_123-0"><text>Paper</text></g><g class="node" id="rag-mermaid-123-1-flowchart-route_a-1"><text>Route</text></g></svg>',
}) } }));

it('opens selected paper nodes by mouse and keyboard without changing the ordinary graph preview', async () => {
  await i18n.changeLanguage('zh-CN');
  const select = vi.fn();
  render(<ConfigProvider theme={{ token: { motion: false } }}><MermaidBlock code='flowchart LR; p_123 --- route_a' nodeLinks={{ p_123: { label: '查看论文 Paper', onClick: select } }} /></ConfigProvider>);
  const node = await screen.findByRole('button', { name: '查看论文 Paper' });
  fireEvent.click(node);
  fireEvent.keyDown(node, { key: 'Enter' });
  expect(select).toHaveBeenCalledTimes(2);
  expect(screen.getByText('Route')).not.toHaveAttribute('role', 'button');
  fireEvent.click(screen.getByRole('button', { name: '放大图表' }));
  await waitFor(() => expect(screen.getByRole('dialog')).toBeVisible());
  fireEvent.click(screen.getByRole('button', { name: 'Close' }));
});
