import { createPaperRecord, type PaperLibraryData, type PaperRecord, type PaperMetadata } from '../model';
import { emptyLibrary } from '../libraryModel';
type PaperOverrides = Omit<Partial<PaperRecord>, 'metadata'> & {
    metadata?: Partial<PaperMetadata>;
};
export function paperFixture(overrides: PaperOverrides = {}): PaperRecord {
    const { metadata, ...fields } = overrides;
    return createPaperRecord({ title: 'A study', authors: ['A. Author'], venue: 'Journal', year: 2024, identifiers: {}, ...metadata }, { id: 'paper', academicWorkId: overrides.id || 'paper', revision: 1, ...fields });
}
export function libraryFixture(input: Partial<PaperLibraryData> = {}): PaperLibraryData {
    const papers = (input.papers || [paperFixture()]).map(p => ({ ...p, attachmentIds: [`attachment-${p.id}`] }));
    return { ...emptyLibrary(), ...input, papers, attachments: input.attachments || papers.map(p => ({ id: `attachment-${p.id}`, paperId: p.id, name: `${p.metadata.title}.pdf`, kind: 'pdf', source: p.source, addedAt: '2026-10-21T00:00:00Z', isPrimary: true, available: true })) };
}
