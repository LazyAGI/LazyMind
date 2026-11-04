import { expect, it } from 'vitest';
import { visiblePaperAnalysis } from './paperReport';

it('only suppresses the publisher placeholder and retains meaningful uncertainty', () => {
  expect(visiblePaperAnalysis('该项具体结论尚未获得充分正文支持，需进一步核验。')).toBe('');
  expect(visiblePaperAnalysis('仅限单跳，尚需核验多步效果。')).toBe('仅限单跳，尚需核验多步效果。');
});
