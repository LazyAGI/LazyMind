import { render, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import MarkdownViewer from './index';

describe('MarkdownViewer image resolver', () => {
  it('uses an optional resolver without changing the Markdown source', async () => {
    const resolveImageUrl = vi.fn(async () => '/preview/diagram.png');
    const { container } = render(
      <MarkdownViewer resolveImageUrl={resolveImageUrl}>
        {'![diagram](docs/assets/diagram.png)'}
      </MarkdownViewer>,
    );

    await waitFor(() => {
      expect(resolveImageUrl).toHaveBeenCalledWith('docs/assets/diagram.png');
      expect(container.querySelector('img')).toHaveAttribute(
        'src',
        '/preview/diagram.png',
      );
    });
  });
});
