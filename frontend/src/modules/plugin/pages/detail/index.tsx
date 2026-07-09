import { useState, useEffect, useCallback, useRef } from 'react';
import { useParams, useNavigate, useOutletContext } from 'react-router-dom';
import { Alert, Breadcrumb, Button, Modal, Input, Spin, message } from 'antd';
import { SyncOutlined, CheckCircleOutlined } from '@ant-design/icons';
import { getPluginDraft, listPluginDrafts, updatePluginDraftContent, aiGeneratePluginDraft, repairPluginDraft } from '../../pluginDraftApi';
import type { PluginDraftRecord } from '../../pluginDraftApi';
import StateGraphEditor from '../../components/StateGraphEditor';
import type { SavePayload, RepairTarget } from '../../components/StateGraphEditor';
import type { ValidationError } from '../../components/StateGraphEditor/core/validator';
import './index.scss';

const POLL_INTERVAL_MS = 3000;

// generate_status values that indicate AI generation is still in progress.
const GENERATING_STATUSES = new Set(['generating', 'brief_done', 'skeleton_done', 'state_done', 'repairing']);

// generate_status values where enough content is available to render the editor.
// state_done means plugin.yaml + state.yml are ready even though Phase 3 is still running.
const EDITOR_READY_STATUSES = new Set(['state_done', 'done']);

type GeneratePhase = 'brief' | 'skeleton' | 'scenario_scripts' | 'repairing' | 'done' | 'failed' | 'idle';

function resolvePhase(status: string): GeneratePhase {
  switch (status) {
    case 'generating':
    case 'brief_done':
      return 'brief';
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
  brief: 'AI 正在分析需求、生成设计草稿…',
  skeleton: 'AI 正在生成插件骨架（slots / steps）…',
  scenario_scripts: 'AI 正在生成 scenario.md 与脚本文件，编辑器可以提前使用…',
  repairing: 'AI 修复中，请稍后…',
  done: '',
  failed: '',
  idle: '',
};

export default function PluginDetailPage() {
  const { pluginId } = useParams<{ pluginId: string }>();
  const navigate = useNavigate();
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  useOutletContext<{ isMenuCollapsed: boolean; toggleMenu: () => void }>();

  // Plugin editor opens as a Drawer over the content area; no need to collapse the sidebar.

  const [draft, setDraft] = useState<PluginDraftRecord | null>(null);
  const draftRef = useRef<PluginDraftRecord | null>(null);
  // Keep ref in sync for use in handleSave (avoids stale closure over version).
  useEffect(() => { draftRef.current = draft; }, [draft]);
  // Persist artifacts panel open/close state across version remounts.
  // Default false — user explicitly opens the panel by clicking the 素材 button.
  const showArtifactsRef = useRef(false);
  const [loading, setLoading] = useState(true);
  const [isRegenerating, setIsRegenerating] = useState(false);
  const [repairModalOpen, setRepairModalOpen] = useState(false);
  // True while the :ai-repair API call is in-flight (keeps Modal open with a spinner).
  const [repairSubmitting, setRepairSubmitting] = useState(false);
  const [repairHint, setRepairHint] = useState('');
  const [repairTarget, setRepairTarget] = useState<RepairTarget>('statemachine');
  const [repairValidationErrors, setRepairValidationErrors] = useState<ValidationError[]>([]);
  const prevStatusRef = useRef<string>('');
  // Per-banner dismissed state. Each banner has a unique key; dismissed keys are stored
  // as a JSON array in localStorage so they survive page refresh.
  // Keys: 'phase3' | 'failed' | 'generate_error' | 'generate_warning:<content_hash>'
  // The generate_warning key includes a hash of the content so that new warnings
  // (after a regenerate or repair) auto-reappear even if a previous warning was dismissed.
  const [dismissedBanners, setDismissedBanners] = useState<Set<string>>(() => {
    if (!pluginId) return new Set();
    try {
      const raw = localStorage.getItem(`plugin_banners_dismissed:${pluginId}`);
      return raw ? new Set(JSON.parse(raw) as string[]) : new Set();
    } catch {
      return new Set();
    }
  });

  const dismissBanner = useCallback((key: string) => {
    setDismissedBanners((prev) => {
      const next = new Set(prev);
      next.add(key);
      if (pluginId) {
        try {
          localStorage.setItem(`plugin_banners_dismissed:${pluginId}`, JSON.stringify([...next]));
        } catch { /* ignore */ }
      }
      return next;
    });
  }, [pluginId]);

  // Derive a short stable key for content-based banners so that new content clears
  // the dismissed state automatically. We use a simple djb2 hash — no crypto needed.
  const contentKey = useCallback((content: string): string => {
    let h = 5381;
    for (let i = 0; i < content.length; i++) h = ((h << 5) + h) ^ content.charCodeAt(i);
    return (h >>> 0).toString(36);
  }, []);
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
          const wasRepairing = prevStatusRef.current === 'repairing';
          if (wasRepairing) {
            const repairFailed = data.generate_warning?.startsWith('[修复失败]');
            // Close the repair Modal now that the job finished.
            setRepairModalOpen(false);
            setRepairHint('');
            setRepairValidationErrors([]);
            setRepairSubmitting(false);
            if (repairFailed) {
              // Clear only the generate_warning banner so it reappears with the new failure message.
              if (pluginId) {
                const warningKey = `generate_warning:${contentKey(data.generate_warning ?? '')}`;
                setDismissedBanners((prev) => {
                  const next = new Set([...prev].filter((k) => !k.startsWith('generate_warning:')));
                  try {
                    localStorage.setItem(`plugin_banners_dismissed:${pluginId}`, JSON.stringify([...next]));
                  } catch { /* ignore */ }
                  return next;
                });
                void warningKey; // used only for type-check
              }
              message.error('AI 修复未通过校验，请查看错误提示后重试');
            } else {
              message.success('AI 修复完成');
            }
          }
        }
        prevStatusRef.current = data.generate_status;
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
      // Clear all dismissed banners so the new generation result is fully visible.
      setDismissedBanners(new Set());
      if (pluginId) {
        try { localStorage.removeItem(`plugin_banners_dismissed:${pluginId}`); } catch { /* ignore */ }
      }
      startPolling();
    } catch {
      message.error('重新生成失败，请稍后重试');
    } finally {
      setIsRegenerating(false);
    }
  }, [pluginId, draft, startPolling]);

  const handleRepair = useCallback(async () => {
    if (!pluginId) return;
    const hintSnapshot = repairHint.trim();
    const errorsSnapshot = repairValidationErrors;
    const targetSnapshot = repairTarget;
    try {
      let fullHint = hintSnapshot;
      if (errorsSnapshot.length > 0) {
        const errText = errorsSnapshot.map((e) => e.message).join('\n');
        fullHint = fullHint
          ? `${fullHint}\n\n校验错误（需一并修复）：\n${errText}`
          : `校验错误（需修复）：\n${errText}`;
      }
      setRepairSubmitting(true);
      // Mark prevStatusRef as repairing BEFORE the API call so the polling
      // callback can correctly detect wasRepairing=true even on the first tick.
      prevStatusRef.current = 'repairing';
      // API returns immediately with generate_status=repairing.
      // Keep Modal open — it will show a loading UI until polling finishes.
      const updated = await repairPluginDraft(pluginId, {
        repair_hint: fullHint,
        target: targetSnapshot,
      });
      setDraft(updated);
      startPolling();
    } catch {
      message.error('修复请求失败，请稍后重试');
      setRepairSubmitting(false);
      // Reset prevStatusRef since we never entered repairing state.
      prevStatusRef.current = '';
      try {
        const latest = await getPluginDraft(pluginId);
        setDraft(latest);
      } catch { /* ignore */ }
    }
    // repairSubmitting stays true until polling ends (handled in startPolling callback)
  }, [pluginId, repairHint, repairValidationErrors, repairTarget, startPolling]);

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
      <div className="plugin-editor-overlay">
        <div className="plugin-editor-mask" />
        <div className="plugin-editor-panel">
          <div className="plugin-detail-loading"><Spin tip="加载中..." /></div>
        </div>
      </div>
    );
  }

  if (!draft) {
    return (
      <div className="plugin-editor-overlay">
        <div className="plugin-editor-mask" />
        <div className="plugin-editor-panel">
          <div className="plugin-detail-error"><p>插件草稿不存在</p></div>
        </div>
      </div>
    );
  }

  const phase = resolvePhase(draft.generate_status);
  const isRepairing = draft.generate_status === 'repairing';
  const isStillGenerating = GENERATING_STATUSES.has(draft.generate_status);
  const editorReady = EDITOR_READY_STATUSES.has(draft.generate_status) || draft.generate_status === 'done';
  const isFailed = draft.generate_status === 'failed';
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
    <div className="plugin-editor-overlay">
      <div className="plugin-editor-mask" />
      <div className="plugin-editor-panel">
    <div className="plugin-detail-page">
      {/* Generation progress banner — shown while Phase 3 is still running (editor already ready) */}
      {isPhase3Running && !repairModalOpen && (
        <Alert
          className="plugin-detail-banner"
          type="info"
          icon={<SyncOutlined spin />}
          showIcon
          message={PHASE_MESSAGES.scenario_scripts}
          description="插件骨架和状态机已就绪，你可以提前预览和编辑，scenario.md 与脚本文件稍后自动填入。"
        />
      )}

      {isFailed && !dismissedBanners.has('failed') && !repairModalOpen && (
        <Alert
          className="plugin-detail-banner"
          type="error"
          showIcon
          closable
          onClose={() => dismissBanner('failed')}
          message="生成失败，你可以手动编辑或重新生成"
          description={draft.generate_error || undefined}
          action={
            <Button size="small" loading={isRegenerating} disabled={isRepairing} onClick={handleRegenerate}>
              重新生成
            </Button>
          }
        />
      )}

      {!isFailed && draft.generate_status === 'done' && draft.generate_error && !dismissedBanners.has('generate_error') && !repairModalOpen && (
        <Alert
          className="plugin-detail-banner"
          type="warning"
          showIcon
          closable
          onClose={() => dismissBanner('generate_error')}
          message="生成完成（部分阶段有警告）"
          description={draft.generate_error}
        />
      )}

      {draft.generate_status === 'done' && draft.generate_warning && !dismissedBanners.has(`generate_warning:${contentKey(draft.generate_warning)}`) && !repairModalOpen && (
        <Alert
          className="plugin-detail-banner"
          type={draft.generate_warning.startsWith('[修复失败]') ? 'error' : 'warning'}
          showIcon
          closable
          onClose={() => dismissBanner(`generate_warning:${contentKey(draft.generate_warning)}`)}
          message={draft.generate_warning.startsWith('[修复失败]') ? 'AI 修复失败，以下原因导致校验仍未通过' : 'AI 生成了部分内容，以下字段可能需要补充或由 AI 修复'}
          description={draft.generate_warning}
        />
      )}

      {/* AI generation progress Modal — shown during Phase 0/1/2, not closable */}
      <Modal
        open={isStillGenerating && !isRepairing && !editorReady}
        closable={false}
        maskClosable={false}
        footer={null}
        width={480}
        centered
        className="plugin-generate-progress-modal"
      >
        <div className="plugin-generate-progress-body">
          <Spin size="large" />
          <p className="plugin-generate-progress-title">{PHASE_MESSAGES[phase] || 'AI 正在生成插件内容…'}</p>
          <div className="plugin-generate-phase-steps">
            <div className={`phase-step ${phase === 'brief' ? 'active' : phase === 'skeleton' || phase === 'scenario_scripts' || phase === 'done' ? 'done' : ''}`}>
              {phase === 'brief' ? <SyncOutlined spin /> : <CheckCircleOutlined />}
              {' 阶段 0：分析需求 & 生成设计草稿'}
            </div>
            <div className={`phase-step ${phase === 'skeleton' ? 'active' : phase === 'scenario_scripts' || phase === 'done' ? 'done' : ''}`}>
              {phase === 'skeleton' ? <SyncOutlined spin /> : phase === 'scenario_scripts' || phase === 'done' ? <CheckCircleOutlined /> : null}
              {' 阶段 1：生成插件骨架'}
            </div>
            <div className="phase-step">{'阶段 2：生成状态机'}</div>
            <div className="phase-step">{'阶段 3：生成文档 & 脚本'}</div>
          </div>
          <p className="plugin-generate-progress-hint">生成过程通常需要 30–90 秒，请耐心等待…</p>
        </div>
      </Modal>

      {/* Editor area — always rendered so it's ready when generation completes */}
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
            defaultShowArtifacts={showArtifactsRef.current}
            onArtifactsChange={(show) => { showArtifactsRef.current = show; }}
            designBriefContent={draft.design_brief_content || undefined}
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
      {/* AI Repair Modal */}
      <Modal
        open={repairModalOpen}
        title={`AI 修复 — ${repairTarget === 'scenario' ? '说明文档' : repairTarget === 'ui' ? 'UI 配置' : '流程图'}`}
        onCancel={() => {
          if (repairSubmitting || isRepairing) return;
          setRepairModalOpen(false);
          setRepairHint('');
          setRepairValidationErrors([]);
        }}
        closable={!repairSubmitting && !isRepairing}
        maskClosable={false}
        footer={repairSubmitting || isRepairing ? null : (
          <Button type="primary" onClick={handleRepair}>开始修复</Button>
        )}
      >
        {(repairSubmitting || isRepairing) ? (
          <div style={{ textAlign: 'center', padding: '32px 0' }}>
            <SyncOutlined spin style={{ fontSize: 36, color: '#1677ff' }} />
            <p style={{ marginTop: 16, fontSize: 15, fontWeight: 500 }}>AI 修复中，请稍后…</p>
            <p style={{ marginTop: 4, color: '#8c8c8c', fontSize: 13 }}>
              正在分析并修复 slot 引用和状态机结构，通常需要 10–30 秒。
            </p>
          </div>
        ) : (
          <>
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
          </>
        )}
      </Modal>
    </div>
    </div>
    </div>
  );
}
