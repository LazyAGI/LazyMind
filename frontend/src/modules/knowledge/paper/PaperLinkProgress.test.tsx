import { render, screen, cleanup } from '@testing-library/react';
import { afterEach, expect, it } from 'vitest';
import PaperLinkProgress, { PaperBibliographyProgress } from './PaperLinkProgress';
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
it('keeps provider warnings collapsed until the user expands them', () => {
 const {container} = render(<PaperLinkProgress phase="ready" failurePhase="pdf" hasPDF={false} warnings={['HTTP 403']} />);
 expect(container.querySelector('details')).not.toHaveAttribute('open');
 expect(screen.getByText('HTTP 403')).toBeInTheDocument();
});

it('offers DOI-specific public search links without claiming a downloadable PDF', () => {
 render(<PaperLinkProgress phase="ready" failurePhase="pdf" hasPDF={false} warnings={[]} doi="10.1109/ISM.2012.84" />);
 expect(screen.getByRole('link', {name:'OpenAlex'})).toHaveAttribute('href','https://openalex.org/works?filter=doi%3Ahttps%3A%2F%2Fdoi.org%2F10.1109%2FISM.2012.84');
 expect(screen.getByRole('link', {name:'Europe PMC'})).toHaveAttribute('href','https://europepmc.org/search?query=DOI%3A10.1109%2FISM.2012.84');
 expect(screen.getByRole('status')).toHaveTextContent('未找到可用 PDF');
});
it('keeps bibliography enrichment explicitly deferred until after saving', () => {
 render(<PaperBibliographyProgress phase="ready" count={10} />);
 expect(screen.getByRole('status')).toHaveTextContent('已识别 10 篇文献，可立即入库');
 expect(screen.getByText('入库后后台处理')).toBeInTheDocument();
 expect(screen.queryByText('已找到可用 PDF')).not.toBeInTheDocument();
});
