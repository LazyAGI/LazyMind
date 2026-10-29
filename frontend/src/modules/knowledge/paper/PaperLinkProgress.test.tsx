import { render, screen, cleanup } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import PaperLinkProgress from './PaperLinkProgress';
afterEach(cleanup);
it('offers manual sources after failed PDF lookup, without treating them as PDF candidates', () => {
 render(<PaperLinkProgress phase="ready" failurePhase="pdf" hasPDF={false} warnings={['需要验证']} manualURLs={['https://sci-hub.ru/10.1234/test', 'javascript:alert(1)']} />);
 expect(screen.getByRole('link', {name:'sci-hub.ru'})).toHaveAttribute('href','https://sci-hub.ru/10.1234/test');
 expect(screen.getAllByRole('link')).toHaveLength(1);
 expect(screen.getByRole('status')).toHaveTextContent('未找到可用 PDF');
});
it('does not offer manual fallback while lookup is still running', () => {
 render(<PaperLinkProgress phase="pdf" failurePhase="pdf" hasPDF={false} warnings={[]} manualURLs={['https://sci-hub.ru/test']} />);
 expect(screen.queryByRole('link')).not.toBeInTheDocument();
});
