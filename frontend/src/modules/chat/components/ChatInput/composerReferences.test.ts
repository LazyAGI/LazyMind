import { describe, expect, it } from 'vitest';
import { buildComposerReferences, collectConversationFiles } from './composerReferences';

describe('composer file references', () => {
  const file = { id: 'history:h1:0', name: 'report.pdf', input: { input_type: 'file' as const, uri: '/uploads/report.pdf', filename: 'report.pdf' } };
  const mention = { mention_id: 'm1', type: 'file' as const, resource_id: file.id, display_name: file.name };

  it('adapts file chips to attachments while preserving executable resource identities', () => {
    const skill = { ...mention, type: 'skill' as const, resource_id: 'skill-1', display_name: 'Summarize' };
    expect(buildComposerReferences([mention, skill], [file], [file.input])).toEqual({ mentions: [skill], inputs: [file.input], unavailable: false });
  });

  it('fails closed when a restored file reference is no longer available', () => {
    expect(buildComposerReferences([mention], [], [])).toEqual({ mentions: [], inputs: [], unavailable: true });
  });

  it('does not merge distinct files with the same display name', () => {
    const other = { ...file, id: 'history:h2:0', input: { ...file.input, uri: '/uploads/another-report.pdf' } };
    const result = buildComposerReferences([mention, { ...mention, mention_id: 'm2', resource_id: other.id }], [file, other], []);
    expect(result.inputs).toHaveLength(2);
  });

  it('keeps mention offsets aligned with trimmed submitted text after chip deletion', () => {
    const skill = { mention_id: 'skill', type: 'skill' as const, resource_id: 's1', display_name: 'Skill', start: 2, end: 7 };
    expect(buildComposerReferences([skill], [], [], '  Skill text').mentions[0]).toMatchObject({ start: 0, end: 5 });
  });

  it('collects only current-conversation user attachments with an accessible URI', () => {
    expect(collectConversationFiles([
      { role: 'User', history_id: 'h1', inputs: [file.input, { input_type: 'text', text: 'hello' }, { input_type: 'file', filename: 'missing.pdf' }] },
      { role: 'Assistant', history_id: 'h2', inputs: [file.input] },
      { role: 'User', history_id: 'h3', trail_source: 'parent', inputs: [file.input] },
    ])).toEqual([file]);
  });
});
