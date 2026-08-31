import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const readFrontendSource = (relativePath) => readFileSync(
  new URL(`../../frontend/src/${relativePath}`, import.meta.url),
  'utf8',
);

describe('settings task controls contract', () => {
  it('keeps conversation defaults separate from the scheduled-task control', () => {
    const settingsSource = readFrontendSource('modules/settings/index.tsx');

    expect(settingsSource).toContain('<TaskEntryDefaults');
    expect(settingsSource).not.toContain('<h2>{t("settingsPage.tasks.defaultsTitle")}</h2>');
    expect(settingsSource).toContain('? "settingsPage.tasks.defaultsDescription"');
    expect(settingsSource).toContain(': "settingsPage.tasks.taskDescription"');
    expect(settingsSource).not.toContain('t("settingsPage.tasks.description")');
    expect(settingsSource).toContain(
      'subtasksEnabled={Boolean(overview?.controls.task_center_enabled)}',
    );
    expect(settingsSource).toContain(
      'workflowsEnabled={Boolean(overview?.controls.workflows_enabled)}',
    );
    expect(settingsSource).not.toContain('className="settings-task-controls is-conversation"');
    expect(settingsSource).toContain(
      'taskControl("schedules_enabled", <ClockCircleOutlined />, t("settingsPage.tasks.enableSchedules")',
    );
    expect(settingsSource).toContain('className="settings-task-controls is-schedules"');
    expect(settingsSource).toContain('switchControl("task_center_enabled"');
    expect(settingsSource).toContain('switchControl("workflows_enabled"');
    expect(settingsSource).toContain('t("settingsPage.confirm.availableWorkflows")');
    expect(settingsSource).not.toContain('settingsPage.open") : t("settingsPage.paused');
    expect(settingsSource).toContain(
      '<SettingsScheduleList schedulesEnabled={schedulesEnabled}',
    );
    expect(settingsSource).not.toContain('<SettingsScheduleList masterEnabled=');
  });

  it('uses the reviewed title and exact switch labels in both languages', () => {
    const zhCN = readFrontendSource('i18n/locales/zh-CN.ts');
    const enUS = readFrontendSource('i18n/locales/en-US.ts');

    ['tasks: "对话与任务"', 'conversationView: "对话默认配置"', 'taskView: "任务"', 'enableSubtasks: "启用子任务"', 'enableWorkflows: "启用工作流"', 'enableSchedules: "启用定时任务"']
      .forEach((copy) => expect(zhCN).toContain(copy));
    ['tasks: "Chat & tasks"', 'conversationView: "Default chat configuration"', 'taskView: "Tasks"', 'enableSubtasks: "Enable subtasks"', 'enableWorkflows: "Enable workflows"', 'enableSchedules: "Enable scheduled tasks"']
      .forEach((copy) => expect(enUS).toContain(copy));
    expect(zhCN).toContain('taskDescription: "管理当前账号的定时任务及运行开关；关闭定时任务不会删除已有配置。"');
    expect(enUS).toContain('taskDescription: "Manage scheduled tasks and their runtime control for the current account. Turning scheduled tasks off does not delete existing configuration."');
  });

  it('uses a compact scheduled-task control band with a mobile fallback', () => {
    const settingsStyles = readFrontendSource('modules/settings/index.scss');

    expect(settingsStyles).toMatch(
      /\.settings-task-controls\s*\{[^}]*display: grid;/s,
    );
    expect(settingsStyles).not.toContain('.settings-task-controls.is-conversation');
    expect(settingsStyles).toContain(
      '.settings-task-controls.is-schedules { grid-template-columns: minmax(0, 1fr); }',
    );
    expect(settingsStyles).toContain(
      '.settings-task-control + .settings-task-control { border-left: 1px solid #e8eef6; }',
    );
    expect(settingsStyles).toContain('.settings-task-controls { grid-template-columns: 1fr; }');
    expect(settingsStyles).toContain(
      '.settings-task-control + .settings-task-control { border-top: 1px solid #e8eef6; border-left: 0; }',
    );
  });

  it('nests default configuration under Chat & tasks and keeps the legacy route compatible', () => {
    const settingsSource = readFrontendSource('modules/settings/index.tsx');
    const tasksStart = settingsSource.indexOf('} else if (section === "tasks")');
    const knowledgeStart = settingsSource.indexOf('} else if (section === "knowledge")');

    expect(settingsSource).not.toContain('{ id: "defaults"');
    expect(settingsSource).toContain('candidate === "defaults"');
    expect(settingsSource).toContain('const taskView = candidate !== "defaults" && searchParams.get("view") === "tasks" ? "tasks" : "conversation";');
    expect(tasksStart).toBeGreaterThan(-1);
    expect(knowledgeStart).toBeGreaterThan(tasksStart);
    expect(settingsSource.slice(tasksStart, knowledgeStart)).toContain('<TaskEntryDefaults');
    expect(settingsSource.slice(tasksStart, knowledgeStart)).toContain('key: "conversation"');
    expect(settingsSource.slice(tasksStart, knowledgeStart)).toContain('key: "tasks"');
  });
});
