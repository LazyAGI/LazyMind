import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ConfigProvider } from 'antd';
import { createInstance } from 'i18next';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import zhCN from '@/i18n/locales/zh-CN';
import NewWorkflowModal from './index';

const api = vi.hoisted(() => ({
  createWorkflowDraft: vi.fn(),
  aiGenerateWorkflowDraft: vi.fn(),
  updateWorkflowDraftContent: vi.fn(),
  preflightSkillWorkflowConversion: vi.fn(),
  listWorkflowDrafts: vi.fn(),
}));
const { listSkillAssetsPage } = vi.hoisted(() => ({ listSkillAssetsPage: vi.fn() }));
vi.mock('../../workflowDraftApi', () => api);
vi.mock('@/modules/memory/skillApi', () => ({ listSkillAssetsPage }));
vi.mock('react-router-dom', () => ({ useNavigate: () => vi.fn() }));

const skill = { id: 'source_skill', name: 'Source Skill' };
const getComputedStyle = window.getComputedStyle.bind(window);

beforeEach(() => {
  vi.spyOn(window, 'getComputedStyle').mockImplementation((element) => getComputedStyle(element));
  vi.clearAllMocks();
  listSkillAssetsPage.mockResolvedValue({ records: [skill], total: 1 });
  api.listWorkflowDrafts.mockResolvedValue({ records: [], total: 0 });
  api.preflightSkillWorkflowConversion.mockResolvedValue({
    skill_id: skill.id, status: 'warning', summary: 'Review conversion risks.',
    checks: [{ code: 'DEPENDENCY_RESOURCE_MISSING', severity: 'warning', message: 'A referenced file is missing.' }],
  });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); });

describe('NewWorkflowModal during translation refresh', () => {
  it.each([false, true])('preserves skill conversion and details when the page regains focus (preset: %s)', async (preset) => {
    const i18n = createInstance();
    await i18n.use(initReactI18next).init({ lng: 'zh-CN', resources: { 'zh-CN': { translation: zhCN } } });
    const onCancel = vi.fn();
    render(
      <I18nextProvider i18n={i18n}>
        <ConfigProvider virtual={false} theme={{ token: { motion: false } }}>
          <NewWorkflowModal open initialSkill={preset ? skill : undefined} onCancel={onCancel} onCreated={vi.fn()} />
        </ConfigProvider>
      </I18nextProvider>,
    );
    if (!preset) {
      fireEvent.click(screen.getByRole('button', { name: /从技能转化/ }));
      const picker = screen.getByRole('combobox', { name: '搜索并选择技能' });
      fireEvent.focus(picker);
      fireEvent.mouseDown(picker);
      fireEvent.click(await screen.findByRole('option', { name: skill.name, exact: true }));
    }
    await screen.findByText('Review conversion risks.');
    await waitFor(() => expect(screen.getByRole('button', { name: '开始转换' })).toBeEnabled());
    fireEvent.change(screen.getByRole('textbox', { name: /显示名称/ }), { target: { value: '自定义工作流' } });
    fireEvent.click(screen.getByText('高级：工作流标识（已自动生成）'));
    const idInput = screen.getByRole('textbox', { name: /工作流标识/ });
    fireEvent.change(idInput, { target: { value: 'custom-workflow' } });

    fireEvent.click(document.querySelector('.ant-modal-wrap')!);
    // MainLayout refreshes developer preferences on focus. Its terminology subscriber
    // calls changeLanguage even when the language has not changed, replacing `t`.
    await act(async () => { await i18n.changeLanguage(i18n.language); });
    expect(screen.getByRole('button', { name: /从技能转化/ })).toHaveAttribute('aria-pressed', 'true');
    fireEvent.click(screen.getByRole('button', { name: /查看详情/ }));
    expect(screen.getByText('A referenced file is missing.')).toBeVisible();

    await act(async () => { await i18n.changeLanguage(i18n.language); });
    expect(screen.getByRole('textbox', { name: /显示名称/ })).toHaveValue('自定义工作流');
    expect(screen.getByRole('textbox', { name: /工作流标识/ })).toHaveValue('custom-workflow');
    expect(screen.getByRole('button', { name: /收起详情/ })).toHaveAttribute('aria-expanded', 'true');
    expect(api.preflightSkillWorkflowConversion).toHaveBeenCalledTimes(1);
    expect(api.listWorkflowDrafts).toHaveBeenCalledTimes(1);
    expect(api.createWorkflowDraft).not.toHaveBeenCalled();
    expect(onCancel).not.toHaveBeenCalled();
  });
});
