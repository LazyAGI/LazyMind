import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import PaperAuthors from './PaperAuthors';
afterEach(cleanup);
it('keeps the first two and last authors visible without dropping the complete accessible list', () => {
  const authors = ['Alice', 'Bob', 'Carol', 'David', 'Eve'];
  render(<PaperAuthors authors={authors} />);
  expect(screen.getByText('Alice, Bob')).toBeInTheDocument();
  expect(screen.getByText('Eve')).toBeInTheDocument();
  expect(screen.queryByText('Carol')).not.toBeInTheDocument();
  expect(screen.getByLabelText(authors.join(', '))).toHaveAttribute('title', authors.join(', '));
  expect(authors).toHaveLength(5);
});
it('shows all authors when there are at most three', () => {
  render(<PaperAuthors authors={['Alice', 'Bob', 'Carol']} />);
  expect(screen.getByText('Alice, Bob, Carol')).toBeInTheDocument();
  expect(screen.queryByText('…')).not.toBeInTheDocument();
});
