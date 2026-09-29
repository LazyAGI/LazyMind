import { useCallback, useEffect, useMemo, useState } from "react";
import type { ChangeEvent, ReactNode, RefObject } from "react";
import { Alert, Empty, Input, Pagination, Skeleton, Switch, message } from "antd";
import { ApartmentOutlined, AppstoreOutlined } from "@ant-design/icons";
import { useTranslation } from "react-i18next";
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

async function loadAllSkills(): Promise<SkillAssetRecord[]> {
  const records = new Map<string, SkillAssetRecord>();
  for (let page = 1; ; page += 1) {
    const response = await listSkillAssetsPage({ page, pageSize: 100 });
    const previousSize = records.size;
    response.records.forEach((record) => records.set(record.id, record));
    if (records.size >= (response.total ?? records.size)) return [...records.values()];
    if (records.size === previousSize) throw new Error("Incomplete skill pagination");
  }
}

interface UserSkillWorkflowSettingsProps {
  activeView?: ResourceTab;
  onViewChange?: (view: ResourceTab) => void;
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
  const { t } = useTranslation();
  const status = !controlEnabled
    ? t("settingsPage.skills.masterOff", { label: controlLabel })
    : enabled
      ? t("settingsPage.enabled")
      : t("settingsPage.disabled");
  return (
    <tr className={controlEnabled ? "" : "is-master-paused"}>
      <td><div className="settings-resource-name">
      <span className="settings-skill-resource-icon" aria-hidden="true">{icon}</span>
      <div className="settings-skill-resource-copy">
        <h2>{title}</h2>
        <p>{description || t("settingsPage.skills.noDescription")}</p>
        {error ? <span className="settings-skill-resource-error" role="alert">{error}</span> : null}
      </div>
      </div></td>
      <td className="settings-resource-category">{meta}</td>
      <td>
      <Switch
        className="settings-ref-switch"
        checked={enabled}
        loading={loading}
        disabled={!controlEnabled || loading}
        onChange={onChange}
        aria-label={t("settingsPage.skills.toggleAria", {
          action: enabled ? t("settingsPage.confirm.disableState") : t("settingsPage.confirm.enableState"),
          title,
        })}
        aria-describedby={`${id}-resource-state`}
      />
      <span id={`${id}-resource-state`} className="settings-screenreader-status">
        {controlEnabled ? status : t("settingsPage.skills.resourceSuspended", { status })}
      </span>
      </td>
    </tr>
  );
}

export default function UserSkillWorkflowSettings({
  activeView,
  onViewChange,
  skillsEnabled,
  workflowsEnabled,
  groupSaving,
  controlsDisabled,
  onGroupChange,
  headingRef,
  onChanged,
}: UserSkillWorkflowSettingsProps) {
  const { t } = useTranslation();
  const [localTab, setLocalTab] = useState<ResourceTab>("skills");
  const activeTab = activeView ?? localTab;
  const setActiveTab = onViewChange ?? setLocalTab;
  const [pageByTab, setPageByTab] = useState<Record<ResourceTab, number>>({ skills: 1, workflows: 1 });
  const [pageSize, setPageSize] = useState(12);
  const [keyword, setKeyword] = useState("");
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
        loadAllSkills(),
        listUserWorkflowSettings(),
      ]);
      setSkills(skillResponse);
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
    if (!controlEnabled) return t("settingsPage.skills.allDisabled", { total });
    if (total > 0 && enabledCount === total) return t("settingsPage.skills.allEnabled", { enabled: enabledCount, total });
    if (enabledCount > 0) return t("settingsPage.skills.partialEnabled", { enabled: enabledCount, total });
    return t("settingsPage.skills.allDisabled", { total });
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
      message.success(enabled ? t("settingsPage.skills.skillEnabled") : t("settingsPage.skills.skillDisabled"));
    } catch {
      setSkills((current) => current.map((item) => item.id === skill.id ? { ...item, isEnabled: skill.isEnabled } : item));
      setRowErrors((current) => ({ ...current, [key]: t("settingsPage.skills.saveFailed") }));
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
      message.success(enabled ? t("settingsPage.skills.workflowEnabled") : t("settingsPage.skills.workflowDisabled"));
    } catch {
      setWorkflows((current) => current.map((item) => item.workflow_ref === workflow.workflow_ref ? { ...item, enabled: workflow.enabled } : item));
      setRowErrors((current) => ({ ...current, [key]: t("settingsPage.skills.saveFailed") }));
    } finally {
      finishSaving(key);
    }
  };

  const activeResources = (activeTab === "skills" ? skills : workflows).filter((item) =>
    `${item.name} ${item.description ?? ""}`.toLocaleLowerCase().includes(keyword.trim().toLocaleLowerCase()),
  );
  const activeGroupEnabled = activeTab === "skills" ? skillsEnabled : workflowsEnabled;
  const activeGroupSaving = groupSaving === activeTab;
  const activeGroupTotal = activeTab === "skills" ? skills.length : workflows.length;
  const activeGroupEnabledCount = activeTab === "skills" ? enabledSkillCount : enabledWorkflowCount;
  const activeGroupLabel = activeTab === "skills" ? t("settingsPage.skills.mySkills") : t("settingsPage.skills.myWorkflows");
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
            meta={skill.category || t("settingsPage.skills.personalSkill")}
            enabled={skill.isEnabled}
            controlEnabled={skillsEnabled && !controlsDisabled}
            controlLabel={t("settingsPage.skills.mySkills")}
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
            meta={t("settingsPage.skills.workflowMeta", { revision: workflow.revision_no })}
            enabled={workflow.enabled}
            controlEnabled={workflowsEnabled && !controlsDisabled}
            controlLabel={t("settingsPage.skills.myWorkflows")}
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
          <h1 ref={headingRef} tabIndex={-1}>{t("settingsPage.skills.title")}</h1>
          <p>{t("settingsPage.skills.description")}</p>
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
              onChange={(enabled: boolean) => onGroupChange(activeTab, enabled, activeGroupEnabledCount)}
              aria-label={t("settingsPage.skills.bulkEnableAria", { label: activeGroupLabel })}
            />
          </div>
        </div>
      </header>

      {activeTab === "skills" && !skillsEnabled ? (
        <div className="settings-skill-master-notice" role="status" aria-live="polite">
          {t("settingsPage.skills.masterOffSkillNotice")}
        </div>
      ) : activeTab === "workflows" && !workflowsEnabled ? (
        <div className="settings-skill-master-notice" role="status" aria-live="polite">
          {t("settingsPage.skills.masterOffWorkflowNotice")}
        </div>
      ) : null}

      <nav className="settings-skill-resource-tabs" aria-label={t("settingsPage.skills.tabsAria")}>
        <button type="button" className={activeTab === "skills" ? "is-active" : ""} onClick={() => setActiveTab("skills")}>
          {t("settingsPage.skills.mySkills")} <span>{skills.length}</span>
        </button>
        <button type="button" className={activeTab === "workflows" ? "is-active" : ""} onClick={() => setActiveTab("workflows")}>
          {t("settingsPage.skills.myWorkflows")} <span>{workflows.length}</span>
        </button>
      </nav>

      <div className="settings-resource-toolbar">
        <Input.Search
          allowClear
          value={keyword}
          placeholder={t("settingsPage.skills.searchResources")}
          aria-label={t("settingsPage.skills.searchResources")}
          onChange={(event: ChangeEvent<HTMLInputElement>) => {
            setKeyword(event.target.value);
            setPageByTab({ skills: 1, workflows: 1 });
          }}
        />
      </div>

      {loading ? (
        <div className="settings-skill-resource-loading" aria-label={t("settingsPage.skills.loadingAria")}><Skeleton active paragraph={{ rows: 5 }} /></div>
      ) : loadError ? (
        <Alert
          type="error"
          showIcon
          message={t("settingsPage.skills.loadFailed")}
          description={t("settingsPage.skills.loadFailedDesc")}
          action={<button type="button" className="settings-skill-retry" onClick={() => void loadResources()}>{t("settingsPage.retry")}</button>}
        />
      ) : rows.length ? (
        <>
          <div className="settings-resource-table-wrap">
            <table className="settings-resource-table">
              <thead><tr>
                <th>{t("settingsPage.skills.resourceName")}</th>
                <th>{t("settingsPage.skills.resourceInfo")}</th>
                <th>{t("settingsPage.skills.resourceEnabled")}</th>
              </tr></thead>
              <tbody>{rows}</tbody>
            </table>
          </div>
          <footer className="settings-skill-resource-pagination">
            <Pagination
              current={activePage}
              pageSize={pageSize}
              total={activeResources.length}
              pageSizeOptions={[12, 20, 50, 100]}
              showSizeChanger
              showTotal={(total: number) => t("settingsPage.skills.totalItems", { total })}
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
          <Empty
            image={Empty.PRESENTED_IMAGE_SIMPLE}
            description={activeTab === "skills" ? t("settingsPage.skills.emptySkills") : t("settingsPage.skills.emptyWorkflows")}
          />
        </div>
      )}
    </section>
  );
}
