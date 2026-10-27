import { describe, expect, it } from 'vitest';
import { libraryFixture } from './__tests__/fixtures';
import { paperDocument } from './libraryDocuments';

describe('paper document selection and explicit restoration', () => {
  it('uses a confirmed available attachment when another is pending', () => {
    const data = libraryFixture();
    data.attachments[0].contentRef = 'pending';
    data.attachments[0].academicState = { document_id: 'pending', kind: 'unknown', status: 'pending', paper_id: '', manual: false };
    data.attachments.push({ ...data.attachments[0], id: 'confirmed', contentRef: 'pdf', academicState: { document_id: 'pdf', kind: 'paper', status: 'succeeded', paper_id: 'work', manual: true } });
    data.papers[0].attachmentIds.push('confirmed');
    expect(paperDocument(data, data.papers[0].id)?.state.document_id).toBe('pdf');
    data.attachments[1].available = false;
    expect(paperDocument(data, data.papers[0].id)?.state.document_id).toBe('pending');
  });
  it('does not select an unassociated original file', () => {
    const data = libraryFixture();
    data.attachments[0].contentRef = 'raw';
    data.attachments[0].academicState = { document_id: 'raw', kind: 'paper', status: 'succeeded', paper_id: 'paper', manual: true };
    data.papers[0].attachmentIds = [];
    expect(paperDocument(data, 'paper')).toBeUndefined();
  });
});
