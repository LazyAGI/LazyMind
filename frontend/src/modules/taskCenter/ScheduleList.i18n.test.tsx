import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { createInstance } from 'i18next';
import { I18nextProvider, initReactI18next } from 'react-i18next';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import enUS from '@/i18n/locales/en-US';
import zhCN from '@/i18n/locales/zh-CN';
import ScheduleList from './ScheduleList';

const mocks = vi.hoisted(() => ({
  datasetServiceListDatasets: vi.fn(),
  getModelReadiness: vi.fn(),
  listAutomationGroups: vi.fn(),
  listScheduleTasks: vi.fn(),
  listSchedules: vi.fn(),
  navigate: vi.fn(),
  uploadFileInChunks: vi.fn(),
}));

vi.mock('react-router-dom', () => ({
  useNavigate: () => mocks.navigate,
}));

vi.mock('./api', () => ({
  batchCreateAutomationGroup: vi.fn(),
  cancelSchedule: vi.fn(),
  createSchedule: vi.fn(),
  deleteAutomationGroup: vi.fn(),
  deleteSchedule: vi.fn(),
  enableSchedule: vi.fn(),
  listAutomationGroups: mocks.listAutomationGroups,
  listSchedules: mocks.listSchedules,
  listScheduleTasks: mocks.listScheduleTasks,
  moveSchedule: vi.fn(),
  runScheduleNow: vi.fn(),
  updateSchedule: vi.fn(),
}));

vi.mock('@/modules/chat/utils/request', () => ({
  KnowledgeBaseServiceApi: () => ({
    datasetServiceListDatasets: mocks.datasetServiceListDatasets,
  }),
}));

vi.mock('@/modules/chat/utils/chunkUpload', () => ({
  uploadFileInChunks: mocks.uploadFileInChunks,
}));

vi.mock('@/components/request', () => ({
  axiosInstance: { get: mocks.getModelReadiness },
  BASE_URL: '',
  localizeErrorCode: (code: string) => code,
}));

const CJK_COPY = /[\u3400-\u4dbf\u4e00-\u9fff]/;
const UNRESOLVED_I18N_KEY = /(?:taskCenter|settingsPage)\.[A-Za-z0-9_.]+/;

function surfaceCopy(root: HTMLElement): string {
  const attributes = [root, ...root.querySelectorAll<HTMLElement>('[placeholder], [aria-label], [title], [alt]')]
    .flatMap((node) => ['placeholder', 'aria-label', 'title', 'alt'].map((name) => node.getAttribute(name) ?? ''));
  return [root.textContent ?? '', ...attributes].join('\n');
}

function expectEnglishSurface(root: HTMLElement) {
  const copy = surfaceCopy(root);
  expect(copy).not.toMatch(CJK_COPY);
  expect(copy).not.toMatch(UNRESOLVED_I18N_KEY);
}

async function renderEnglishScheduleList() {
  const i18n = createInstance();
  await i18n.use(initReactI18next).init({
    resources: {
      'zh-CN': { translation: zhCN },
      'en-US': { translation: enUS },
    },
    lng: 'en-US',
    fallbackLng: 'zh-CN',
    supportedLngs: ['zh-CN', 'en-US'],
    nonExplicitSupportedLngs: false,
    interpolation: { escapeValue: false },
  });

  const result = render(
    <I18nextProvider i18n={i18n}>
      <ScheduleList active />
    </I18nextProvider>,
  );
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
  return result;
}

async function waitForScheduleListToSettle() {
  await waitFor(() => {
    expect(mocks.listSchedules).toHaveBeenCalled();
    expect(mocks.listAutomationGroups).toHaveBeenCalled();
    expect(mocks.datasetServiceListDatasets).toHaveBeenCalled();
    expect(mocks.getModelReadiness).toHaveBeenCalled();
  });
  await screen.findByText('No tasks');
}

describe('ScheduleList English localization', () => {
  beforeEach(() => {
    Object.defineProperty(window, 'matchMedia', {
      configurable: true,
      writable: true,
      value: vi.fn().mockImplementation((query: string) => ({
        matches: false,
        media: query,
        onchange: null,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
      })),
    });
    mocks.datasetServiceListDatasets.mockReset();
    mocks.datasetServiceListDatasets.mockResolvedValue({ data: { datasets: [] } });
    mocks.getModelReadiness.mockReset();
    mocks.getModelReadiness.mockResolvedValue({ data: { data: { ready: true } } });
    mocks.listAutomationGroups.mockReset();
    mocks.listAutomationGroups.mockResolvedValue({ items: [], total: 0 });
    mocks.listScheduleTasks.mockReset();
    mocks.listScheduleTasks.mockResolvedValue({ items: [], total: 0 });
    mocks.listSchedules.mockReset();
    mocks.listSchedules.mockResolvedValue({ items: [], total: 0 });
    mocks.navigate.mockReset();
    mocks.uploadFileInChunks.mockReset();
  });

  afterEach(() => {
    cleanup();
  });

  it('renders the schedule toolbar without Chinese or unresolved translation keys', async () => {
    const { container } = await renderEnglishScheduleList();

    await waitForScheduleListToSettle();
    const toolbar = container.querySelector<HTMLElement>('.schedule-toolbar');
    expect(toolbar).not.toBeNull();
    expect(within(toolbar!).getByText('Grouped')).toBeInTheDocument();
    expect(within(toolbar!).getByText('Individual')).toBeInTheDocument();
    expect(within(toolbar!).getByRole('button', { name: /New Scheduled Task/ })).toBeInTheDocument();
    expectEnglishSurface(toolbar!);
  });

  it('renders both task and task-group creation modes fully in English', async () => {
    await renderEnglishScheduleList();

    await waitForScheduleListToSettle();
    fireEvent.click(screen.getByRole('button', { name: /New Scheduled Task/ }));

    const dialog = await screen.findByRole('dialog');
    expect(within(dialog).getByText('New Scheduled Task')).toBeInTheDocument();
    expect(within(dialog).getByText('Task')).toBeInTheDocument();
    const groupMode = within(dialog).getByRole('button', { name: /Task group.*Create a task group/i });
    expect(within(dialog).getByPlaceholderText('Please enter a task name')).toBeInTheDocument();
    expectEnglishSurface(dialog);

    fireEvent.click(groupMode);

    expect(await within(dialog).findByText('Task group name')).toBeInTheDocument();
    expect(within(dialog).getByText('Tasks in this group')).toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: 'Delete task' })).toBeInTheDocument();
    expect(within(dialog).getByRole('button', { name: /Add task/ })).toBeInTheDocument();
    expect(within(dialog).getByText('Task 1')).toBeInTheDocument();
    expectEnglishSurface(dialog);
  });
});
