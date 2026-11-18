import { describe, expect, it } from 'vitest';
import type { ComposerFileCandidate } from '../components/ChatInput/composerReferences';
import { prepareResumeSubmission, resumePhotoCandidates, RESUME_TEMPLATES } from './resumePresentation';

const photo = (name: string, uri: string): ComposerFileCandidate => ({ id: `upload:${name}`, name, input: { input_type: 'image', uri, filename: name } });

describe('resume presentation submission', () => {
  it.each(RESUME_TEMPLATES)('sends the chosen $id template and both deliverables', template => {
    const result = prepareResumeSubmission('优化我的简历', { template: template.id, photoFit: 'contain' }, [], []);
    expect(result.text).toContain(`模板：${template.name}（${template.id}）`);
    expect(result.text).toContain('PDF');
    expect(result.text).toContain('Word');
    expect(result.text.startsWith('优化我的简历\n\n')).toBe(true);
    expect(result.unavailablePhoto).toBe(false);
  });

  it('attaches only the explicitly selected historical photo and preserves its fit', () => {
    const other = photo('screenshot.png', '/uploads/screenshot.png');
    const selected = photo('portrait.jpg', '/uploads/portrait.jpg');
    const result = prepareResumeSubmission('请生成', { template: 'sidebar', photoUri: selected.input!.uri, photoFit: 'cover' }, [other, selected], []);
    expect(result.inputs).toEqual([selected.input]);
    expect(result.text).toContain('portrait.jpg');
    expect(result.text).toContain('居中裁切（cover）');
    expect(result.text).not.toContain('screenshot.png');
  });

  it('does not infer a resume photo from other uploaded images', () => {
    const uploaded = photo('some-image.png', '/uploads/some-image.png');
    const result = prepareResumeSubmission('请生成', { template: 'business', photoFit: 'contain' }, [uploaded], [uploaded.input!]);
    expect(result.text).toContain('不添加照片，不使用其他图片替代');
    expect(result.text).not.toContain(uploaded.name);
  });

  it('refuses submission when a selected photo was removed instead of silently substituting', () => {
    const result = prepareResumeSubmission('草稿', { template: 'modern', photoFit: 'contain', photoUri: '/uploads/removed.jpg' }, [photo('another.jpg', '/uploads/another.jpg')], []);
    expect(result).toEqual({ text: '草稿', inputs: [], unavailablePhoto: true });
  });

  it('deduplicates historical photos and excludes unavailable or non-image attachments', () => {
    const image = photo('photo.png', '/uploads/photo.png');
    expect(resumePhotoCandidates([image, { ...image, id: 'history:1:0' }, { ...photo('bad.png', '/uploads/bad.png'), disabled: true },
      { id: 'pdf', name: 'resume.pdf', input: { input_type: 'file', uri: '/uploads/resume.pdf', filename: 'resume.pdf' } }])).toHaveLength(1);
  });
});
