import { useState, useEffect, useCallback, useRef } from 'react';
import { useParams, useNavigate, useOutletContext } from 'react-router-dom';
import { Alert, Breadcrumb, Button, Modal, Input, Skeleton, Spin, message } from 'antd';
import { SyncOutlined, CheckCircleOutlined, ToolOutlined } from '@ant-design/icons';
import { getPluginDraft, listPluginDrafts, updatePluginDraftContent, aiGeneratePluginDraft, repairPluginDraft } from '../../pluginDraftApi';
import type { PluginDraftRecord } from '../../pluginDraftApi';
import StateGraphEditor from '../../components/StateGraphEditor';
import type { SavePayload, RepairTarget } from '../../components/StateGraphEditor';
import type { ValidationError } from '../../components/StateGraphEditor/core/validator';
import './index.scss';

const POLL_INTERVAL_MS = 3000;

// generate_status values that indicate AI generation is still in progress.
const GENERATING_STATUSES = new Set(['generating', 'skeleton_done', 'state_done', 'repairing']);

// generate_status values where enough content is available to render the editor.
// state_done means plugin.yaml + state.yml are ready even though Phase 3 is still running.
const EDITOR_READY_STATUSES = new Set(['state_done', 'done']);

type GeneratePhase = 'skeleton' | 'scenario_scripts' | 'repairing' | 'done' | 'failed' | 'idle';

function resolvePhase(status: string): GeneratePhase {
  switch (status) {
    case 'generating':
    case 'skeleton_done':
      return 'skeleton';
    case 'state_done':
      return 'scenario_scripts';
    case 'repairing':
      return 'repairing';
    case 'done':
      return 'done';
    case 'failed':
      return 'failed';
    default:
      return 'idle';
  }
}

const PHASE_MESSAGES: Record<GeneratePhase, string> = {
  skeleton: 'AI 正在分析需求、生成插件骨架（slots / steps）…',
  scenario_scripts: 'AI 正在生成 scenario.md 与脚本文件，编辑器可以提前使用…',
  repairing: 'AI 修复中，请稍后…',
  done: '',
  failed: '',
  idle: '',
};

export default function PluginDetailPage() {
  const { pluginId } = useParams<{ pluginId: string }>();
  const navigate = useNavigate();
  const { isMenuCollapsed, toggleMenu } = useOutletContext<{ isMenuCollapsed: boolean; toggleMenu: () => void }>();

  // Collapse the chat sidebar when entering the plugin editor
  useEffect(() => {
    if (!isMenuCollapsed) toggleMenu();
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const [draft, setDraft] = useState<PluginDraftRecord | null>(null);
  const draftRef = useRef<PluginDraftRecord | null>(null);
  // Keep ref in sync for use in handleSave (avoids stale closure over version).
  useEffect(() => { draftRef.current = draft; }, [draft]);
  const [loading, setLoading] = useState(true);
  const [isRegenerating, setIsRegenerating] = useState(false);
  const [repairModalOpen, setRepairModalOpen] = useState(false);
  const [repairHint, setRepairHint] = useState('');
  const [repairTarget, setRepairTarget] = useState<RepairTarget>('statemachine');
  const [repairValidationErrors, setRepairValidationErrors] = useState<ValidationError[]>([]);
  const [isRepairing, setIsRepairing] = useState(false);
  // Track whether the user has dismissed the generate banner for this specific draft.
  // Persisted in localStorage so it survives page refresh.
  const [bannerDismissed, setBannerDismissed] = useState(() => {
    if (!pluginId) return false;
    return localStorage.getItem(`plugin_banner_dismissed:${pluginId}`) === '1';
  });
  const dismissBanner = useCallback(() => {
    if (pluginId) localStorage.setItem(`plugin_banner_dismissed:${pluginId}`, '1');
    setBannerDismissed(true);
  }, [pluginId]);
  const [editingName, setEditingName] = useState(false);
  const [nameValue, setNameValue] = useState('');
  // true = show empty-canvas hint; false = user already has experience (≥1 non-empty plugin)
  const [showEmptyHint, setShowEmptyHint] = useState(true);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const loadDraft = useCallback(async () => {
    if (!pluginId) return;
    setLoading(true);
    try {
      const data = await getPluginDraft(pluginId);
      setDraft(data);
      setNameValue(data.name);
    } catch {
      message.error('加载插件草稿失败');
    } finally {
      setLoading(false);
    }
  }, [pluginId]);

  // Check whether the user already has at least one non-empty plugin (excluding the current one).
  // A plugin is considered non-empty when it has state_yaml_content / content, or generate_status is done/state_done.
  useEffect(() => {
    if (!pluginId) return;
    listPluginDrafts({ pageSize: 50 })
      .then(({ records }) => {
        const hasExperience = records.some(
          (r) =>
            r.id !== pluginId &&
            (r.state_yaml_content || r.content || r.plugin_yaml_content ||
              r.generate_status === 'done' || r.generate_status === 'state_done'),
        );
        if (hasExperience) setShowEmptyHint(false);
      })
      .catch(() => {});
  }, [pluginId]);

  const startPolling = useCallback(() => {
    if (pollRef.current) clearInterval(pollRef.current);
    pollRef.current = setInterval(async () => {
      if (!pluginId) return;
      try {
        const data = await getPluginDraft(pluginId);
        setDraft(data);
        if (!GENERATING_STATUSES.has(data.generate_status)) {
          if (pollRef.current) clearInterval(pollRef.current);
          pollRef.current = null;
          // Auto-dismiss banner when generation just completed (transition to done/failed).
          // The user will see it briefly during the current session; next visit it's gone.
          if (data.generate_status === 'done' || data.generate_status === 'failed') {
            localStorage.setItem(`plugin_banner_dismissed:${pluginId}`, '1');
            setBannerDismissed(true);
          }
        }
      } catch {
        // ignore polling errors
      }
    }, POLL_INTERVAL_MS);
  }, [pluginId]);

  useEffect(() => {
    void loadDraft();
    return () => { if (pollRef.current) clearInterval(pollRef.current); };
  }, [loadDraft]);

  useEffect(() => {
    if (draft && GENERATING_STATUSES.has(draft.generate_status)) {
      startPolling();
    } else {
      if (pollRef.current) clearInterval(pollRef.current);
    }
  }, [draft?.generate_status, startPolling]);

  const handleRegenerate = useCallback(async () => {
    if (!pluginId || !draft) return;
    setIsRegenerating(true);
    try {
      const updated = await aiGeneratePluginDraft(pluginId, {
        description: draft.content || draft.name,
      });
      setDraft(updated);
      // Reset dismissed so the new generation result is visible once it completes.
      setBannerDismissed(false);
      localStorage.removeItem(`plugin_banner_dismissed:${pluginId}`);
      startPolling();
    } catch {
      message.error('重新生成失败，请稍后重试');
    } finally {
      setIsRegenerating(false);
    }
  }, [pluginId, draft, startPolling]);

  const handleRepair = useCallback(async () => {
    if (!pluginId) return;
    // Snapshot hint/errors before clearing state.
    const hintSnapshot = repairHint.trim();
    const errorsSnapshot = repairValidationErrors;
    const targetSnapshot = repairTarget;
    // Immediately close modal and start polling so repairing UI shows.
    setRepairModalOpen(false);
    setRepairHint('');
    setRepairValidationErrors([]);
    setIsRepairing(true);
    startPolling();
    try {
      let fullHint = hintSnapshot;
      if (errorsSnapshot.length > 0) {
        const errText = errorsSnapshot.map((e) => e.message).join('\n');
        fullHint = fullHint
          ? `${fullHint}\n\n校验错误（需一并修复）：\n${errText}`
          : `校验错误（需修复）：\n${errText}`;
      }
      const updated = await repairPluginDraft(pluginId, {
        repair_hint: fullHint,
        target: targetSnapshot,
      });
      setDraft(updated);
      message.success('AI 修复完成');
    } catch {
      message.error('修复失败，请稍后重试');
      // Refresh to get restored generate_status from backend defer.
      try {
        const latest = await getPluginDraft(pluginId);
        setDraft(latest);
      } catch { /* ignore */ }
    } finally {
      setIsRepairing(false);
    }
  }, [pluginId, repairHint, repairTarget, repairValidationErrors, startPolling]);

  const handleOpenRepair = useCallback((target: RepairTarget, validationErrors?: ValidationError[]) => {
    setRepairTarget(target);
    setRepairValidationErrors(validationErrors ?? []);
    setRepairModalOpen(true);
  }, []);

  const handleSave = useCallback(
    async (payload: SavePayload) => {
      if (!pluginId) return;
      const currentVersion = draftRef.current?.version ?? 1;
      let updated: PluginDraftRecord;
      try {
        updated = await updatePluginDraftContent(pluginId, {
          state_yaml_content: payload.stateYaml,
          state_layout_content: payload.stateLayoutContent,
          plugin_yaml_content: payload.pluginYaml,
          scenario_content: payload.scenarioContent,
          scripts_content: payload.scriptsContent,
          version: currentVersion,
        });
      } catch (err: unknown) {
        // 409 Conflict: AI write bumped the version. Refresh draft version silently so
        // the next save attempt uses the correct version, then rethrow so the editor
        // shows "保存失败".
        const status = (err as { response?: { status?: number; data?: { data?: PluginDraftRecord } } })?.response?.status;
        if (status === 409) {
          const latest = (err as { response: { data: { data: PluginDraftRecord } } }).response?.data?.data;
          if (latest) setDraft(latest);
          message.warning('内容已被 AI 更新，正在重试保存…');
        }
        throw err;
      }
      setDraft(updated);
    },
    [pluginId],
  );

  if (loading) {
    return (
      <div className="plugin-detail-loading">
        <Spin tip="加载中..." />
      </div>
    );
  }

  if (!draft) {
    return (
      <div className="plugin-detail-error">
        <p>插件草稿不存在</p>
      </div>
    );
  }

  const phase = resolvePhase(draft.generate_status);
  const isStillGenerating = GENERATING_STATUSES.has(draft.generate_status);
  const editorReady = EDITOR_READY_STATUSES.has(draft.generate_status) || draft.generate_status === 'done';
  const isFailed = draft.generate_status === 'failed';
  // Show the editor in a read-only banner state when Phase 3 is still running
  const isPhase3Running = draft.generate_status === 'state_done';

  // Determine which YAML content to use
  // state_layout_content stores x-layout JSON separately; merge it into stateYaml
  // so the editor initializes with correct node positions.
  const rawStateYaml = draft.state_yaml_content || draft.content || undefined;
  let stateYaml = rawStateYaml;
  if (rawStateYaml && draft.state_layout_content) {
    try {
      const layoutObj = JSON.parse(draft.state_layout_content) as Record<string, { x: number; y: number; w?: number; width?: number }>;
      if (Object.keys(layoutObj).length > 0) {
        // Prepend x-layout block to state YAML so the parser picks it up.
        // Support both 'w' (legacy) and 'width' (current NodeLayout field name).
        const layoutYaml = `x-layout:\n${Object.entries(layoutObj)
          .map(([id, pos]) => {
            const w = pos.w ?? pos.width;
            return `  ${id}: { x: ${pos.x}, y: ${pos.y}${w != null ? `, w: ${w}` : ''} }`;
          })
          .join('\n')}\n`;
        stateYaml = layoutYaml + rawStateYaml;
      }
    } catch {
      // ignore malformed layout JSON
    }
  }
  let pluginYaml = draft.plugin_yaml_content || undefined;
  if (!pluginYaml && draft.name) {
    pluginYaml = `name: "${draft.name.replace(/"/g, '\\"')}"\n`;
  }

  return (
    <div className="plugin-detail-page">
      {/* Generation progress banner */}
      {isStillGenerating && !editorReady && (
        <Alert
          className="plugin-detail-banner"
          type="info"
          icon={<SyncOutlined spin />}
          showIcon
          message={PHASE_MESSAGES[phase] || 'AI 正在生成插件内容…'}
        />
      )}

      {isPhase3Running && (
        <Alert
          className="plugin-detail-banner"
          type="info"
          icon={<SyncOutlined spin />}
          showIcon
          message={PHASE_MESSAGES.scenario_scripts}
          description="插件骨架和状态机已就绪，你可以提前预览和编辑，scenario.md 与脚本文件稍后自动填入。"
        />
      )}

      {isFailed && !bannerDismissed && (
        <Alert
          className="plugin-detail-banner"
          type="error"
          showIcon
          closable
          onClose={dismissBanner}
          message="生成失败，你可以手动编辑或重新生成"
          description={draft.generate_error || undefined}
          action={
            <Button size="small" loading={isRegenerating} disabled={isRepairing} onClick={handleRegenerate}>
              重新生成
            </Button>
          }
        />
      )}

      {!isFailed && draft.generate_status === 'done' && draft.generate_error && !bannerDismissed && (
        <Alert
          className="plugin-detail-banner"
          type="warning"
          showIcon
          closable
          onClose={dismissBanner}
          message="生成完成（部分阶段有警告）"
          description={draft.generate_error}
        />
      )}

      {draft.generate_status === 'done' && draft.generate_warning && !bannerDismissed && (
        <Alert
          className="plugin-detail-banner"
          type="warning"
          showIcon
          closable
          onClose={dismissBanner}
          message="AI 生成了部分内容，以下字段可能需要补充或由 AI 修复"
          description={draft.generate_warning}
          action={
            <Button size="small" icon={<ToolOutlined />} disabled={isRepairing} onClick={() => setRepairModalOpen(true)}>
              AI 修复
            </Button>
          }
        />
      )}

      {/* Phase 1+2 still loading — no content to show yet */}
      {isStillGenerating && !editorReady ? (
        <div className="plugin-detail-skeleton">
          {phase === 'repairing' ? (
            <div className="plugin-detail-phase-steps">
              <div className="phase-step active">
                <SyncOutlined spin />
                {' AI 修复中，请稍后…'}
              </div>
            </div>
          ) : (
            <div className="plugin-detail-phase-steps">
              <div className={`phase-step ${phase === 'skeleton' ? 'active' : ''}`}>
                <SyncOutlined spin={phase === 'skeleton'} />
                {' 阶段 1：分析需求 & 生成骨架'}
              </div>
              <div className="phase-step">
                {'阶段 2：生成状态机'}
              </div>
              <div className="phase-step">
                {'阶段 3：生成文档 & 脚本'}
              </div>
            </div>
          )}
          <Skeleton active paragraph={{ rows: 12 }} />
        </div>
      ) : (
        <div className="plugin-detail-editor">
          {editorReady && isPhase3Running && (
            <div className="plugin-detail-phase-steps plugin-detail-phase-steps--inline">
              <div className="phase-step phase-step--done">
                <CheckCircleOutlined /> 骨架
              </div>
              <div className="phase-step phase-step--done">
                <CheckCircleOutlined /> 状态机
              </div>
              <div className="phase-step active">
                <SyncOutlined spin />
                {' 文档 & 脚本'}
              </div>
            </div>
          )}
          <StateGraphEditor
            key={draft.version}
            initialStateYaml={stateYaml}
            initialPluginYaml={pluginYaml}
            initialScenarioContent={draft.scenario_content || undefined}
            initialScriptsContent={draft.scripts_content || undefined}
            onRepair={handleOpenRepair}
            readonly={isRepairing || repairModalOpen}
            pluginName={
              <Breadcrumb
                items={[
                  { title: '我的插件', href: '/memory-management/plugins' },
                  ...(draft.source_skill_id ? [{
                    title: (
                      <a
                        href={`/memory-management/skill-management?skill_id=${draft.source_skill_id}`}
                        target="_blank"
                        rel="noreferrer"
                        style={{ color: 'var(--color-text-secondary, #888)', fontSize: 12 }}
                      >
                        {draft.source_skill_name || '来源技能'}
                      </a>
                    ),
                  }] : []),
                  {
                    title: editingName ? (
                      <Input
                        autoFocus
                        size="small"
                        value={nameValue}
                        style={{ width: 200 }}
                        onChange={(e) => setNameValue(e.target.value)}
                        onBlur={() => setEditingName(false)}
                        onPressEnter={() => setEditingName(false)}
                      />
                    ) : (
                      <button
                        type="button"
                        className="plugin-detail-name"
                        onClick={() => setEditingName(true)}
                        title="点击编辑名称"
                      >
                        {nameValue}
                      </button>
                    ),
                  },
                ]}
              />
            }
            onSave={handleSave}
            onClose={() => navigate('/memory-management/plugins')}
            showEmptyHint={showEmptyHint}
          />
        </div>
      )}
      {/* AI Repair Modal */}
      <Modal
        open={repairModalOpen}
        title={`AI 修复 — ${repairTarget === 'scenario' ? '说明文档' : repairTarget === 'ui' ? 'UI 配置' : '流程图'}`}
        onCancel={() => { setRepairModalOpen(false); setRepairHint(''); setRepairValidationErrors([]); }}
        onOk={handleRepair}
        confirmLoading={isRepairing}
        okText="开始修复"
        cancelButtonProps={{ style: { display: 'none' } }}
      >
        {repairValidationErrors.length > 0 && (
          <>
            <p style={{ marginBottom: 6 }}>以下校验错误将自动作为修复依据：</p>
            <ul style={{ margin: '0 0 12px 0', paddingLeft: 18, fontSize: 13, color: 'var(--color-text-secondary, #888)' }}>
              {repairValidationErrors.map((e, i) => (
                <li key={i}>{e.message}</li>
              ))}
            </ul>
          </>
        )}
        <p style={{ marginBottom: 8 }}>你也可以补充说明（可选）：</p>
        <Input.TextArea
          placeholder={repairTarget === 'scenario' ? '例如：补充每个步骤的说明，让用户理解如何使用这个插件' : '例如：帮我补全 __start__ 的连线，确保流程可以正常启动'}
          value={repairHint}
          onChange={(e) => setRepairHint(e.target.value)}
          rows={3}
          autoSize={{ minRows: 2, maxRows: 5 }}
        />
      </Modal>
    </div>
  );
}
