import { expect, it } from 'vitest';
import { paperRelationEvidence, paperReportDiagrams, paperReportNarrative, visiblePaperAnalysis } from './paperReport';

it('opens the relationship evidence without repeating diagrams or the reading guide', () => {
  const source = '## 论文关系图\n```mermaid\nflowchart LR\np_a --> p_b\n```\n\n### 逐边依据\n| 起点 | 终点 | 依据 |\n|---|---|---|\n| A | B | 原文方法节 |\n\n## 阅读路线图\n```mermaid\nflowchart LR\nq --- route\n```';
  const evidence = paperRelationEvidence(source);
  expect(evidence).toContain('| A | B | 原文方法节 |');
  expect(evidence).toContain('### 逐边依据');
  expect(evidence).not.toMatch(/mermaid|flowchart|阅读路线图/);
});

it('finds report diagrams while ignoring the retired overview and unrelated examples', () => {
  expect(paperReportDiagrams('## 研究问题\n\n旧报告没有图。')).toEqual({});
  const report = '## 研究脉络图\n\n~~~mermaid\nflowchart TD\nroute_a --- p_123\n~~~\n\n## 论文关系图\n\n```mermaid\nflowchart LR\np_123 -.- p_456\n```\n\n## 其他例子\n```mermaid\nA --> B\n```';
  expect(paperReportDiagrams(report)).toEqual({ relations: 'flowchart LR\np_123 -.- p_456\n' });
});
it('only suppresses the publisher placeholder and retains meaningful uncertainty', () => {
  expect(visiblePaperAnalysis('该项具体结论尚未获得充分正文支持，需进一步核验。')).toBe('');
  expect(visiblePaperAnalysis('仅限单跳，尚需核验多步效果。')).toBe('仅限单跳，尚需核验多步效果。');
});
it('reads the dedicated reading map without taking unrelated diagrams', () => {
  const code = 'flowchart LR\nroute_base --- p_paper-a\n';
  for (const title of ['阅读导图', '阅读路线图', 'Reading map']) {
    expect(paperReportDiagrams(`## ${title}\n\n\`\`\`mermaid\n${code}\`\`\`\n\n## Other\n\`\`\`mermaid\nA --> B\n\`\`\``)).toEqual({ reading: code });
  }
});

it('keeps research context and reading advice in the report body without repeating its diagrams', () => {
  const body = '## 研究报告\n\n### 研究脉络\n已有路线比较。\n\n### 阅读建议\n先读 B，再读 A。';
  expect(paperReportNarrative(`## 阅读导图\n\n\`\`\`mermaid\nflowchart LR\nq --- route\n\`\`\`\n\n${body}`)).toBe(body);
  const plainReport = '## 研究问题\n\n未单独分节的研究正文。';
  expect(paperReportNarrative(plainReport)).toBe(plainReport);
});
