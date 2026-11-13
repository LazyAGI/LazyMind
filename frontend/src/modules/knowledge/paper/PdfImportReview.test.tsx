import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { useState } from 'react';
import PdfImportReview from './PdfImportReview';
import { paperFixture } from './__tests__/fixtures';
import type { PaperMetadata } from './model';
afterEach(cleanup);
const paper = paperFixture({ metadata: { title: 'Recognized title', authors: [], year: 0 } });
const defaults = { metadata: paper, papers: [], candidateIds: [], fixedTarget: false, conflict: false, requiresConfirmation: false, sameVersion: false, confirmed: false, disabled: false, onTargetChange: vi.fn(), onConfirmedChange: vi.fn() };
it('allows correcting new PDF bibliography before confirmation while retaining recognized evidence', () => {
 function Review() {
  const [metadata, setMetadata] = useState<PaperMetadata>(paper.metadata);
  return <PdfImportReview {...defaults} draftMetadata={metadata} onMetadataChange={setMetadata} />;
 }
 render(<Review />);
 fireEvent.change(screen.getByRole('textbox', { name: '题名' }), { target: { value: 'Corrected title' } });
 fireEvent.change(screen.getByRole('textbox', { name: '作者（分号分隔）' }), { target: { value: 'OpenAI; Research' } });
 fireEvent.change(screen.getByRole('spinbutton', { name: '年份' }), { target: { value: '2025' } });
 fireEvent.change(screen.getByRole('textbox', { name: 'DOI' }), { target: { value: '10.1234/test' } });
 expect(screen.getByRole('textbox', { name: '题名' })).toHaveValue('Corrected title');
 expect(screen.getByRole('spinbutton', { name: '年份' })).toHaveValue('2025');
 expect(screen.getByText('Recognized title')).toBeVisible();
});
it('does not edit existing target metadata when associating an attachment', () => {
 render(<PdfImportReview {...defaults} target={paper} draftMetadata={paper.metadata} onMetadataChange={vi.fn()} />);
 expect(screen.queryByRole('textbox', { name: '题名' })).not.toBeInTheDocument();
});
