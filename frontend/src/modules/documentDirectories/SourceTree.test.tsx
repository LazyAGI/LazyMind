import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import SourceTree from './SourceTree';
import { getSourceNodes } from './api';
const t = (key: string) => key;
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t }) }));
vi.mock('./api', () => ({ getSourceNodes: vi.fn() }));

it('navigates paginated source children and preserves a verified empty directory', async () => {
  vi.mocked(getSourceNodes).mockImplementation(async (_, parent, offset) => {
    if (parent === 'empty') return { nodes: [], total: 0, next_offset: 0 };
    return { nodes: [{ node_key: offset ? 'last' : 'empty', parent_key: '', path: offset ? 'last.txt' : 'Empty', name: offset ? 'last.txt' : 'Empty', kind: offset ? 'file' : 'directory', listed: true }], total: 101, next_offset: offset ? 101 : 100 };
  });
  render(<SourceTree directoryId="d" />);
  fireEvent.click(await screen.findByRole('button', { name: 'Empty' }));
  expect(await screen.findByText('directories.emptySourceFolder')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'directories.sourceTree' }));
  await screen.findByRole('button', { name: 'Empty' });
  fireEvent.click(screen.getByTitle('2'));
  expect((await screen.findAllByText('last.txt')).length).toBeGreaterThan(0);
  expect(getSourceNodes).toHaveBeenLastCalledWith('d', '', 100);
});
