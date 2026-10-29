import { createInstance } from 'i18next';
import { describe, expect, it } from 'vitest';
import en from '@/i18n/locales/en-US';
import zh from '@/i18n/locales/zh-CN';
import { getWorkflowDiagnosticMessage } from './validator';

describe.each([
  ['en-US', 'The state graph has no start transition', 'UI tab cannot be empty'],
  ['zh-CN', zh.selfEvolutionRun.validationErrors.E_START_MISSING, zh.selfEvolutionRun.validationErrors.E_UI_TAB_EMPTY],
])('Workflow diagnostic messages (%s)', (language, startMessage, tabMessage) => {
  const i18n = createInstance();
  const ready = i18n.init({ lng: language, initImmediate: false,
    resources: { 'en-US': { translation: en }, 'zh-CN': { translation: zh } } });

  it('preserves specific compiler reasons without displaying raw backend messages', async () => {
    await ready;
    const t = i18n.getFixedT(language);
    expect(getWorkflowDiagnosticMessage(t, { code: 'E_START_MISSING', message: 'private backend detail' })).toBe(startMessage);
    expect(getWorkflowDiagnosticMessage(t, { code: 'E_UI_TAB_EMPTY', message: 'private backend detail' })).toBe(tabMessage);
  });

  it('interpolates authoritative identifiers in generated and editor diagnostics', async () => {
    await ready;
    const t = i18n.getFixedT(language);
    for (const context of [{ node_id: 'consumer', material_id: 'report' }, { nodeId: 'consumer', materialId: 'report' }]) {
      const text = getWorkflowDiagnosticMessage(t, { code: 'E_RUNTIME_POST_CHECK_MATERIAL_NOT_PRODUCED', ...context });
      expect(text).toContain('consumer');
      expect(text).toContain('report');
      expect(text).not.toContain('{{');
    }
  });

  it('falls back to the shared catalog, never backend prose', async () => {
    await ready;
    const t = i18n.getFixedT(language);
    expect(getWorkflowDiagnosticMessage(t, { code: '2000102' })).toBe(t('errors.2000102'));
    expect(getWorkflowDiagnosticMessage(t, { code: 'UNRECOGNIZED_WORKFLOW_TEST_CODE', message: 'private backend detail' })).toBe(t('errors.2000509'));
  });

  it('shows actionable integration failures and the exact missing user input', async () => {
    await ready;
    const t = i18n.getFixedT(language);
    expect(getWorkflowDiagnosticMessage(t, { code: 'E_REPAIR_NODE_REMOVED', node_id: 'review' })).toContain('review');
    expect(getWorkflowDiagnosticMessage(t, { code: 'E_REPAIR_NEEDS_INPUT', message: 'Where should a rejected review go?' })).toContain('Where should a rejected review go?');
    expect(getWorkflowDiagnosticMessage(t, { code: 'E_REPAIR_MODEL_FAILED', message: 'private provider detail' })).not.toContain('private provider detail');
  });

  it('shows the original protected branch and actual candidate edges', async () => {
    await ready;
    const text = getWorkflowDiagnosticMessage(i18n.getFixedT(language), {
      code: 'E_REPAIR_BRANCH_CHANGED', node_id: 'write_copy',
      details: { expected_when: 'copy ready', expected_target: 'compose_report',
        candidate_edges: [{ when: 'new condition', to: 'review' }] },
    });
    for (const value of ['write_copy', 'copy ready', 'compose_report', 'new condition', 'review']) expect(text).toContain(value);
    expect(text).not.toContain('selfEvolutionRun.');
  });

  it('localizes model failure categories and includes only safe diagnostic identifiers', async () => {
    await ready;
    const t = i18n.getFixedT(language);
    for (const code of ['E_REPAIR_MODEL_TIMEOUT', 'E_REPAIR_MODEL_TRANSPORT', 'E_REPAIR_MODEL_CONFIG', 'E_REPAIR_MODEL_LIMIT', 'E_REPAIR_MODEL_OUTPUT']) {
      const text = getWorkflowDiagnosticMessage(t, { code, message: 'private provider body', details: { diagnostic_id: 'diag-123' } });
      expect(text).toContain('diag-123');
      expect(text).not.toContain('private provider body');
      expect(text).not.toContain('selfEvolutionRun.');
    }
    expect(getWorkflowDiagnosticMessage(t, { code: 'E_REPAIR_MODEL_TIMEOUT', details: { diagnostic_id: 'https://private.invalid/key' } })).not.toContain('private.invalid');
  });
});
