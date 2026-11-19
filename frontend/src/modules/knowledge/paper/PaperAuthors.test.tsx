import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { ConfigProvider } from 'antd';
import { afterEach, expect, it } from 'vitest';
import PaperAuthors from './PaperAuthors';
afterEach(cleanup);
it('previews the first three authors and opens the complete list in order on click', async () => {
  const authors = ['Alice', 'Bob', 'Carol', 'David', 'Eve'];
  render(<ConfigProvider theme={{ token: { motion: false } }}><PaperAuthors authors={authors} /></ConfigProvider>);
  expect(screen.getByText('Alice, Bob, Carol')).toBeInTheDocument();
  expect(screen.queryByText('Eve')).not.toBeInTheDocument();
  const trigger = screen.getByRole('button', { name: '查看全部 5 位作者' });
  expect(trigger).toHaveTextContent('+2 位');

  fireEvent.click(trigger);
  const dialog = await screen.findByRole('dialog', { name: '全部作者' });
  expect(within(dialog).getAllByRole('listitem').map(item => item.textContent)).toEqual(authors);

  fireEvent.keyDown(within(dialog).getByRole('button', { name: '关闭作者名单' }), { key: 'Escape' });
  await waitFor(() => expect(screen.queryByRole('dialog', { name: '全部作者' })).not.toBeInTheDocument());
  expect(trigger).toHaveFocus();
});
it('shows all authors when there are at most three', () => {
  render(<PaperAuthors authors={['Alice', 'Bob', 'Carol']} />);
  expect(screen.getByText('Alice, Bob, Carol')).toBeInTheDocument();
  expect(screen.queryByRole('button')).not.toBeInTheDocument();
});
