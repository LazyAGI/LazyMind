import { expect, it } from 'vitest';
import { paperReportNarrative, visiblePaperAnalysis } from './paperReport';

it('only suppresses the publisher placeholder and retains meaningful uncertainty', () => {
  expect(visiblePaperAnalysis('该项具体结论尚未获得充分正文支持，需进一步核验。')).toBe('');
  expect(visiblePaperAnalysis('仅限单跳，尚需核验多步效果。')).toBe('仅限单跳，尚需核验多步效果。');
});
it('keeps research context and later peer sections while omitting reading advice and diagrams', () => {
  for (const title of ['阅读建议', '阅读计划', '阅读顺序', '阅读路线', '阅读路线图', '阅读导图', 'Reading advice', 'Reading suggestions', 'Reading recommendations', 'Reading plan', 'Reading order', 'Reading route', 'Reading route map', 'Reading map']) {
    const body = `## 研究报告\n\n### 研究脉络\n已有路线比较。\n\n### ${title}\n先读 B，再读 A。\n\n#### 第一步\n阅读 B 的方法节。\n\n### 已核验关系\nA 复用 B 的组件。`;
    expect(paperReportNarrative(`## 阅读导图\n\n\`\`\`mermaid\nflowchart LR\nq --- route\n\`\`\`\n\n${body}`)).toBe('### 研究脉络\n已有路线比较。\n\n### 已核验关系\nA 复用 B 的组件。');
  }
  const plainReport = '## 研究问题\n\n未单独分节的研究正文。';
  expect(paperReportNarrative(plainReport)).toBe(plainReport);
});

it('removes saved investigation process text while preserving substantive checked findings', () => {
  const source = '## 方向一\n问题正文。\n\n### 前序调查\n已完成本轮调查\n\n已核验：A 使用 B 的索引组件。\n\n待解决：内部待查事项。\n\n### 同层比较\n仍有待查问题\n\n### 后续追踪\n本轮查证核实的后续直接方法改动：0项；内部过程记录。\n\n## 方向二\n其他正文。';
  const result = paperReportNarrative(source);
  expect(result).toContain('已核验：A 使用 B 的索引组件。');
  expect(result).toContain('## 方向二');
  expect(result).not.toMatch(/前序调查|同层比较|后续追踪|待解决|内部待查|本轮调查|后续直接方法改动/);
});
