import type { ComposerFileCandidate, ComposerFileInput } from '../components/ChatInput/composerReferences';

export const RESUME_CASE_ID = 'tailored-resume-generator';
export const RESUME_TEMPLATES = [
  { id: 'business', name: '商务蓝', color: '#255378' },
  { id: 'modern', name: '现代色带', color: '#22485c' },
  { id: 'sidebar', name: '专业侧栏', color: '#203e50' },
  { id: 'editorial', name: '创意版式', color: '#9d483a' },
  { id: 'campus', name: '校园清新', color: '#2e7a6e' },
  { id: 'minimal', name: '黑白 ATS', color: '#343b43' },
] as const;

export type ResumeTemplateId = typeof RESUME_TEMPLATES[number]['id'];
export interface ResumePresentation {
  template: ResumeTemplateId;
  photoUri?: string;
  photoFit: 'contain' | 'cover';
}

export const DEFAULT_RESUME_PRESENTATION: ResumePresentation = { template: 'business', photoFit: 'contain' };

export function normalizeResumePresentation(value?: Partial<ResumePresentation>): ResumePresentation {
  return {
    template: RESUME_TEMPLATES.some(template => template.id === value?.template) ? value!.template! : 'business',
    photoFit: value?.photoFit === 'cover' ? 'cover' : 'contain',
    ...(value?.photoUri ? { photoUri: value.photoUri } : {}),
  };
}

export function resumePhotoCandidates(files: ComposerFileCandidate[]): ComposerFileCandidate[] {
  const byUri = new Map<string, ComposerFileCandidate>();
  for (const file of files) {
    if (!file.disabled && file.input?.uri && file.input.input_type === 'image'
      && /\.(?:png|jpe?g|webp)$/i.test(file.name)) byUri.set(file.input.uri, file);
  }
  return [...byUri.values()];
}

/** Carry visible presentation choices through the existing text + attachment API. */
export function prepareResumeSubmission(
  text: string,
  presentation: ResumePresentation,
  files: ComposerFileCandidate[],
  inputs: ComposerFileInput[],
) {
  const value = normalizeResumePresentation(presentation);
  const photo = value.photoUri ? resumePhotoCandidates(files).find(file => file.input?.uri === value.photoUri) : undefined;
  if (value.photoUri && !photo?.input) return { text, inputs, unavailablePhoto: true };
  const template = RESUME_TEMPLATES.find(item => item.id === value.template)!;
  const lines = [text, '', '【简历排版】', `模板：${template.name}（${template.id}）。`];
  if (photo?.input) {
    lines.push(`照片：使用已选择的附件 ${JSON.stringify(photo.name)}；附件定位：${JSON.stringify(photo.input.uri)}。`,
      `照片呈现：${value.photoFit === 'cover' ? '居中裁切（cover）' : '完整显示（contain）'}，保持原始比例，合理留出照片位置。`);
  } else lines.push('照片：不添加照片，不使用其他图片替代。');
  lines.push('生成简历成品时，请使用所选模板同时提供排好版的 PDF、可编辑 Word 文件和成品预览。完整保留已提供的真实经历，修改说明与补充建议放在对话中。');
  return {
    text: lines.join('\n'),
    inputs: photo?.input && !inputs.some(input => input.uri === photo.input!.uri) ? [...inputs, photo.input] : inputs,
    unavailablePhoto: false,
  };
}
