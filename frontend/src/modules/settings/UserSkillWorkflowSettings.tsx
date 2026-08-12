import { useCallback, useEffect, useMemo, useState } from "react";
import type { ReactNode, RefObject } from "react";
import { Alert, Empty, Pagination, Skeleton, Switch, Tag, message } from "antd";
import { ApartmentOutlined, AppstoreOutlined } from "@ant-design/icons";
import {
  listSkillAssetsPage,
  patchSkillAsset,
  type SkillAssetRecord,
} from "@/modules/memory/skillApi";
import {
  listUserWorkflowSettings,
  setUserWorkflowEnabled,
  type UserWorkflowSetting,
} from "@/modules/workflow/workflowDraftApi";

export type ResourceTab = "skills" | "workflows";

interface UserSkillWorkflowSettingsProps {
  skillsEnabled: boolean;
  workflowsEnabled: boolean;
  groupSaving: ResourceTab | null;
  controlsDisabled: boolean;
  onGroupChange: (group: ResourceTab, enabled: boolean, enabledCount: number) => void;
  headingRef: RefObject<HTMLHeadingElement>;
  onChanged?: () => void | Promise<void>;
}

interface ResourceRowProps {
  id: string;
  title: string;
  description: string;
  meta: string;
  enabled: boolean;
  controlEnabled: boolean;
  controlLabel: string;
  loading: boolean;
  error?: string;
  icon: ReactNode;
  onChange: (enabled: boolean) => void;
}

function ResourceRow({
  id,
  title,
  description,
  meta,
  enabled,
  controlEnabled,
  controlLabel,
  loading,
  error,
  icon,
  onChange,
}: ResourceRowProps) {
  const status = !controlEnabled ? `${controlLabel}开关已关闭` : enabled ? "已启用" : "已停用";
  const statusClass = !controlEnabled
    ? " is-suspended"
    : enabled
      ? " is-enabled"
      : "";

  return (
    <div className={`settings-skill-resource-row${enabled ? "" : " is-disabled"}${controlEnabled ? "" : " is-master-paused"}`}>
      <span className="settings-skill-resource-icon" aria-hidden="true">{icon}</span>
      <div className="settings-skill-resource-copy">
        <h2>{title}</h2>
        <p>{description || "暂无描述"}</p>
        <span className="settings-skill-resource-meta">{meta}</span>
        {error ? <span className="settings-skill-resource-error" role="alert">{error}</span> : null}
      </div>
      <Tag className={`settings-skill-resource-status${statusClass}`}>
        {status}
      </Tag>
      <Switch
        className="settings-ref-switch"
        checked={enabled}
        loading={loading}
        disabled={!controlEnabled || loading}
        onChange={onChange}
        aria-label={`${enabled ? "停用" : "启用"}${title}`}
        aria-describedby={`${id}-resource-state`}
      />
      <span id={`${id}-resource-state`} className="settings-screenreader-status">
        {controlEnabled ? status : `${status}，当前资源已停用`}
      </span>
    </div>
  );
}

export default function UserSkillWorkflowSettings({
  skillsEnabled,
  workflowsEnabled,
  groupSaving,
  controlsDisabled,
  onGroupChange,
  headingRef,
  onChanged,
}: UserSkillWorkflowSettingsProps) {
  const [activeTab, setActiveTab] = useState<ResourceTab>("skills");
  const [pageByTab, setPageByTab] = useState<Record<ResourceTab, number>>({ skills: 1, workflows: 1 });
  const [pageSize, setPageSize] = useState(6);
  const [skills, setSkills] = useState<SkillAssetRecord[]>([]);
  const [workflows, setWorkflows] = useState<UserWorkflowSetting[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(false);
  const [saving, setSaving] = useState<Set<string>>(new Set());
  const [rowErrors, setRowErrors] = useState<Record<string, string>>({});

  const loadResources = useCallback(async () => {
    setLoading(true);
    setLoadError(false);
    try {
      const [skillResponse, workflowResponse] = await Promise.all([
        listSkillAssetsPage({ page: 1, pageSize: 200 }),
        listUserWorkflowSettings(),
      ]);
      setSkills(skillResponse.records);
      setWorkflows(workflowResponse);
    } catch {
      setLoadError(true);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadResources();
  }, [loadResources, skillsEnabled, workflowsEnabled]);

  const enabledSkillCount = useMemo(() => skills.filter((item) => item.isEnabled).length, [skills]);
  const enabledWorkflowCount = useMemo(() => workflows.filter((item) => item.enabled).length, [workflows]);
  const groupStatus = (controlEnabled: boolean, enabledCount: number, total: number) => {
    if (!controlEnabled) return `0 / ${total} 全部停用`;
    if (total > 0 && enabledCount === total) return `${enabledCount} / ${total} 全部启用`;
    if (enabledCount > 0) return `${enabledCount} / ${total} 部分启用`;
    return `0 / ${total} 全部停用`;
  };

  const beginSaving = (key: string) => {
    setSaving((current) => new Set(current).add(key));
    setRowErrors((current) => {
      const next = { ...current };
      delete next[key];
      return next;
    });
  };

  const finishSaving = (key: string) => {
    setSaving((current) => {
      const next = new Set(current);
      next.delete(key);
      return next;
    });
  };

  const toggleSkill = async (skill: SkillAssetRecord, enabled: boolean) => {
    const key = `skill:${skill.id}`;
    beginSaving(key);
    setSkills((current) => current.map((item) => item.id === skill.id ? { ...item, isEnabled: enabled } : item));
    try {
      await patchSkillAsset(skill.id, { is_enabled: enabled });
      await onChanged?.();
      message.success(enabled ? "技能已启用" : "技能已停用");
    } catch {
      setSkills((current) => current.map((item) => item.id === skill.id ? { ...item, isEnabled: skill.isEnabled } : item));
      setRowErrors((current) => ({ ...current, [key]: "保存失败，已恢复原状态，请重试。" }));
    } finally {
      finishSaving(key);
    }
  };

  const toggleWorkflow = async (workflow: UserWorkflowSetting, enabled: boolean) => {
    const key = `workflow:${workflow.workflow_ref}`;
    beginSaving(key);
    setWorkflows((current) => current.map((item) => item.workflow_ref === workflow.workflow_ref ? { ...item, enabled } : item));
    try {
      await setUserWorkflowEnabled(workflow.workflow_ref, enabled);
      await onChanged?.();
      message.success(enabled ? "工作流已启用" : "工作流已停用");
    } catch {
      setWorkflows((current) => current.map((item) => item.workflow_ref === workflow.workflow_ref ? { ...item, enabled: workflow.enabled } : item));
      setRowErrors((current) => ({ ...current, [key]: "保存失败，已恢复原状态，请重试。" }));
    } finally {
      finishSaving(key);
    }
  };

  const activeResources = activeTab === "skills" ? skills : workflows;
  const activeGroupEnabled = activeTab === "skills" ? skillsEnabled : workflowsEnabled;
  const activeGroupSaving = groupSaving === activeTab;
  const activeGroupTotal = activeTab === "skills" ? skills.length : workflows.length;
  const activeGroupEnabledCount = activeTab === "skills" ? enabledSkillCount : enabledWorkflowCount;
  const activeGroupLabel = activeTab === "skills" ? "我的技能" : "我的工作流";
  const activePage = pageByTab[activeTab];
  const pageStart = (activePage - 1) * pageSize;
  const visibleResources = activeResources.slice(pageStart, pageStart + pageSize);

  useEffect(() => {
    const lastPage = Math.max(1, Math.ceil(activeResources.length / pageSize));
    if (activePage > lastPage) {
      setPageByTab((current) => ({ ...current, [activeTab]: lastPage }));
    }
  }, [activePage, activeResources.length, activeTab, pageSize]);

  const rows = activeTab === "skills"
    ? (visibleResources as SkillAssetRecord[]).map((skill) => {
        const key = `skill:${skill.id}`;
        return (
          <ResourceRow
            key={key}
            id={key}
            title={skill.name}
            description={skill.description}
            meta={skill.category || "个人技能"}
            enabled={skill.isEnabled}
            controlEnabled={skillsEnabled}
            controlLabel="我的技能"
            loading={saving.has(key)}
            error={rowErrors[key]}
            icon={<AppstoreOutlined />}
            onChange={(enabled) => void toggleSkill(skill, enabled)}
          />
        );
      })
    : (visibleResources as UserWorkflowSetting[]).map((workflow) => {
        const key = `workflow:${workflow.workflow_ref}`;
        return (
          <ResourceRow
            key={key}
            id={key}
            title={workflow.name}
            description={workflow.description || workflow.when_to_use}
            meta={`我的工作流 · v${workflow.revision_no}`}
            enabled={workflow.enabled}
            controlEnabled={workflowsEnabled}
            controlLabel="我的工作流"
            loading={saving.has(key)}
            error={rowErrors[key]}
            icon={<ApartmentOutlined />}
            onChange={(enabled) => void toggleWorkflow(workflow, enabled)}
          />
        );
      });

  return (
    <section className="settings-skill-resources">
      <header className="settings-skill-resources-head">
        <span className="settings-skill-resources-title-icon" aria-hidden="true"><AppstoreOutlined /></span>
        <div>
          <h1 ref={headingRef} tabIndex={-1}>技能与插件</h1>
          <p>管理我的技能和我的工作流运行状态；这里只支持启用或停用。</p>
        </div>
        <div className="settings-skill-group-controls">
          <div className="settings-skill-master">
            <div>
              <strong>{activeGroupLabel}</strong>
              <span>{groupStatus(activeGroupEnabled, activeGroupEnabledCount, activeGroupTotal)}</span>
            </div>
            <Switch
              className="settings-ref-switch"
              checked={activeGroupEnabled}
              loading={activeGroupSaving}
              disabled={controlsDisabled || activeGroupTotal === 0}
              onChange={(enabled) => onGroupChange(activeTab, enabled, activeGroupEnabledCount)}
              aria-label={`批量启用${activeGroupLabel}`}
            />
          </div>
        </div>
      </header>

      {activeTab === "skills" && !skillsEnabled ? (
        <div className="settings-skill-master-notice" role="status" aria-live="polite">
          我的技能开关已关闭，所有技能均已停用；重新开启会统一启用全部技能。
        </div>
      ) : activeTab === "workflows" && !workflowsEnabled ? (
        <div className="settings-skill-master-notice" role="status" aria-live="polite">
          我的工作流开关已关闭，所有工作流均已停用；重新开启会统一启用全部工作流。
        </div>
      ) : null}

      <nav className="settings-skill-resource-tabs" aria-label="技能与工作流">
        <button type="button" className={activeTab === "skills" ? "is-active" : ""} onClick={() => setActiveTab("skills")}>
          我的技能 <span>{skills.length}</span>
        </button>
        <button type="button" className={activeTab === "workflows" ? "is-active" : ""} onClick={() => setActiveTab("workflows")}>
          我的工作流 <span>{workflows.length}</span>
        </button>
      </nav>

      {loading ? (
        <div className="settings-skill-resource-loading" aria-label="正在加载技能与工作流"><Skeleton active paragraph={{ rows: 5 }} /></div>
      ) : loadError ? (
        <Alert
          type="error"
          showIcon
          message="无法加载技能与工作流"
          description="请检查网络后重试。"
          action={<button type="button" className="settings-skill-retry" onClick={() => void loadResources()}>重试</button>}
        />
      ) : rows.length ? (
        <>
          <div className="settings-skill-resource-list">{rows}</div>
          <footer className="settings-skill-resource-pagination">
            <Pagination
              current={activePage}
              pageSize={pageSize}
              total={activeResources.length}
              pageSizeOptions={[6, 12, 20, 50]}
              showSizeChanger
              showTotal={(total: number) => `共 ${total} 条`}
              onChange={(page: number) => setPageByTab((current) => ({ ...current, [activeTab]: page }))}
              onShowSizeChange={(_page: number, nextPageSize: number) => {
                setPageSize(nextPageSize);
                setPageByTab({ skills: 1, workflows: 1 });
              }}
            />
          </footer>
        </>
      ) : (
        <div className="settings-skill-resource-empty">
          <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={activeTab === "skills" ? "暂无可管理的个人技能" : "暂无可管理的个人工作流"} />
        </div>
      )}
    </section>
  );
}
