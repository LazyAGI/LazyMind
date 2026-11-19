import { render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import RenderWord from './render-word';

vi.mock('docx-preview', () => ({
  renderAsync: async (_file: ArrayBuffer, container: HTMLElement) => {
    container.innerHTML = '<a href="https://example.com/article" target="_self">Article</a><a href="#section">Section</a><div id="section">Body</div>';
  },
}));

it('opens document external links separately while keeping internal bookmarks local', async () => {
  render(<RenderWord fileData={new ArrayBuffer(1)} content={null} />);
  const external = await screen.findByRole('link', { name: 'Article' });
  expect(external).toHaveAttribute('target', '_blank');
  expect(external).toHaveAttribute('rel', 'noopener noreferrer');
  expect(screen.getByRole('link', { name: 'Section' })).not.toHaveAttribute('target');
});
