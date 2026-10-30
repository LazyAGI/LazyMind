import { createPaperRecord, parseAuthorInput, type PaperRecord, type PaperLibraryData } from './model';

/** Data backup includes attachment references; original PDF bytes stay in document storage. */
export function serializeLibraryBackup(data: PaperLibraryData, datasetId?: string) {
    return JSON.stringify({ ...data, backupDatasetId: datasetId || data.backupDatasetId, schemaVersion: 4 }, null, 2);
}
export function parseLibraryBackup(text: string): PaperLibraryData {
    const value = JSON.parse(text);
    if (!value || ![3, 4].includes(value.schemaVersion) || !['papers', 'folders', 'attachments', 'notes'].every(key => Array.isArray(value[key])))
        throw new Error('不是有效的文献库备份');
    if (value.schemaVersion === 4) {
        if (!value.papers.every((paper: PaperRecord) => paper?.id && typeof paper.metadata?.title === 'string' && Array.isArray(paper.metadata.authors) && paper.metadata.identifiers && Array.isArray(paper.attachmentIds) && Array.isArray(paper.noteIds)))
            throw new Error('文献库备份的论文数据无效');
        return value;
    }
    const papers = value.papers.map((raw: Record<string, unknown>) => {
        const old = raw.metadata as Partial<PaperRecord['metadata']> | undefined;
        return createPaperRecord({ ...old, title: String(raw.title || ''), authors: parseAuthorInput(String(raw.authors || '')), venue: String(raw.venue || ''), year: Number(raw.year) || 0,
            abstract: typeof raw.abstract === 'string' ? raw.abstract : old?.abstract,
            identifiers: { ...old?.identifiers, doi: typeof raw.doi === 'string' ? raw.doi : old?.identifiers?.doi, arxiv: typeof raw.arxiv === 'string' ? raw.arxiv : old?.identifiers?.arxiv },
            citationCount: typeof raw.citationCount === 'number' ? raw.citationCount : old?.citationCount,
        }, { ...raw, revision: 0 } as Partial<PaperRecord>);
    });
    return { ...value, schemaVersion: 4, papers, legacy: value.legacy || value };
}

/** Cross-library files are represented as missing attachments, never linked across datasets. */
export function prepareLibraryRestore(data: PaperLibraryData, datasetId: string, includeReferences: boolean, restoreId: string): PaperLibraryData {
    const keepReferences = includeReferences && (!data.backupDatasetId || data.backupDatasetId === datasetId);
    return { ...data,
        attachments: keepReferences ? data.attachments : data.attachments.map(attachment => {
            const { contentRef: _reference, academicState: _state, ...rest } = attachment;
            return { ...rest, available: false };
        }),
        migrationSnapshots: [...(data.migrationSnapshots || []), { id: restoreId, createdAt: new Date().toISOString(), source: 'library-backup', data }],
    };
}
