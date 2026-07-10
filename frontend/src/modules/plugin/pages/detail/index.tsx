import { useState, useEffect, useCallback, useRef } from 'react';
import { useParams, useNavigate, useOutletContext } from 'react-router-dom';
import { Alert, Breadcrumb, Button, Modal, Input, Spin, Select, Space, Tag, message } from 'antd';
import { SyncOutlined, CheckCircleOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { getPluginDraft, listPluginDrafts, updatePluginDraftContent, aiGeneratePluginDraft, repairPluginDraft, publishPluginDraft, listPluginVersions, getPluginVersion, editPluginVersion } from '../../pluginDraftApi';
import type { PluginDraftRecord } from '../../pluginDraftApi';
import type { PluginVersionSummary, PluginVersionContent } from '../../pluginDraftApi';
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




export default function PluginDetailPage() {
  const { pluginId } = useParams<{ pluginId: string }>();
  const navigate = useNavigate();
  const { t } = useTranslation();
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  useOutletContext<{ isMenuCollapsed: boolean; toggleMenu: () => void }>();

  const getPhaseMessage = (phase: GeneratePhase): string => {
    const map: Record<GeneratePhase, string> = {
      brief: t('selfEvolutionRun.pluginDetailPhaseBrief'),
      skeleton: t('selfEvolutionRun.pluginDetailPhaseSkeleton'),
      scenario_scripts: t('selfEvolutionRun.pluginDetailPhaseScenarioScripts'),
      repairing: t('selfEvolutionRun.pluginDetailPhaseRepairing'),
      done: '',
      failed: '',
      idle: '',
    };
    return map[phase] ?? '';
  };

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
  const [publishing, setPublishing] = useState(false);
  const [versions, setVersions] = useState<PluginVersionSummary[]>([]);
  const [selectedRevision, setSelectedRevision] = useState<string>('draft');
  const [versionContent, setVersionContent] = useState<PluginVersionContent | null>(null);
  const [switchingVersion, setSwitchingVersion] = useState(false);
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
      message.error(t('selfEvolutionRun.pluginDetailLoadFailed'));
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
              message.error(t('selfEvolutionRun.pluginDetailRepairValidationFailed'));
            } else {
              message.success(t('selfEvolutionRun.pluginDetailRepairSuccess'));
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
      message.error(t('selfEvolutionRun.pluginDetailRegenerateFailed'));
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
      message.error(t('selfEvolutionRun.pluginDetailRepairRequestFailed'));
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
        // 409 Conflict: two sub-cases.
        // 1. Version conflict: AI write bumped the version — refresh draft and rethrow.
        // 2. Plugin id duplicate: another draft by this user already uses the same plugin id.
        const status = (err as { response?: { status?: number; data?: { message?: string; data?: PluginDraftRecord } } })?.response?.status;
        if (status === 409) {
          const body = (err as { response: { data: { message?: string; data?: PluginDraftRecord } } }).response?.data;
          if (body?.message && body.message.includes('plugin id already exists')) {
            message.error(t('selfEvolutionRun.pluginDetailPluginIdDuplicate'));
            throw err;
          }
          // Version conflict: update local version and let editor retry.
          const latest = body?.data;
          if (latest) setDraft(latest);
          message.warning(t('selfEvolutionRun.pluginDetailAiUpdatedRetrying'));
        }
        throw err;
      }
      setDraft(updated);
    },
    [pluginId],
  );

  const handlePublish = useCallback(async () => {
    if (!draft) return;
    setPublishing(true);
    try {
      const result = await publishPluginDraft(draft.id);
      message.success(`Plugin 已发布为版本 ${result.revision_no}，默认关闭`);
      setVersions(await listPluginVersions(result.plugin_ref));
      setDraft(await getPluginDraft(draft.id));
    } catch (error) {
      message.error(error instanceof Error ? error.message : 'Plugin 发布失败');
    } finally {
      setPublishing(false);
    }
  }, [draft]);

  useEffect(() => {
    if (!draft?.published_plugin_ref) { setVersions([]); return; }
    void listPluginVersions(draft.published_plugin_ref).then(setVersions).catch(() => setVersions([]));
  }, [draft?.published_plugin_ref]);

  const handleVersionChange = useCallback(async (value: string) => {
    if (value === 'draft') { setSelectedRevision('draft'); setVersionContent(null); return; }
    if (!draft?.published_plugin_ref) return;
    const loadVersion = async () => {
      setSelectedRevision(value); setSwitchingVersion(true);
      try { setVersionContent(await getPluginVersion(draft.published_plugin_ref, value)); }
      catch { message.error('历史版本加载失败'); setSelectedRevision('draft'); }
      finally { setSwitchingVersion(false); }
    };
    if (draft.draft_dirty) {
      Modal.confirm({ title: '当前草稿有未发布的修改', content: '历史版本将以只读方式打开。若随后点击“编辑此版本”，当前草稿会被清空并替换为该历史版本。', okText: '继续查看', cancelText: '取消', onOk: loadVersion });
      return;
    }
    await loadVersion();
  }, [draft?.published_plugin_ref, draft?.draft_dirty]);

  const handleEditHistoricalVersion = useCallback(async () => {
    if (!draft?.published_plugin_ref || selectedRevision === 'draft') return;
    Modal.confirm({ title: '用此版本替换当前草稿？', content: '当前草稿内容会被选定历史版本覆盖，此操作不会修改已发布版本。', okText: '替换并编辑', onOk: async () => {
      const next = await editPluginVersion(draft.published_plugin_ref, selectedRevision); setDraft(next); setVersionContent(null); setSelectedRevision('draft'); message.success('草稿已替换为选定版本');
    }});
  }, [draft?.published_plugin_ref, selectedRevision]);

  if (loading) {
    return (
      <div className="plugin-editor-overlay">
        <div className="plugin-editor-mask" />
        <div className="plugin-editor-panel">
          <div className="plugin-detail-loading"><Spin tip={t('selfEvolutionRun.pluginDetailLoading')} /></div>
        </div>
      </div>
    );
  }

  if (!draft) {
    return (
      <div className="plugin-editor-overlay">
        <div className="plugin-editor-mask" />
        <div className="plugin-editor-panel">
          <div className="plugin-detail-error"><p>{t('selfEvolutionRun.pluginDetailNotFound')}</p></div>
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
  const viewingHistory = selectedRevision !== 'draft' && versionContent !== null;

  // Determine which YAML content to use
  // state_layout_content stores x-layout JSON separately; merge it into stateYaml
  // so the editor initializes with correct node positions.
  const rawStateYaml = viewingHistory ? versionContent.state_yaml_content : (draft.state_yaml_content || draft.content || undefined);
  let stateYaml = rawStateYaml;
  if (!viewingHistory && rawStateYaml && draft.state_layout_content) {
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
  let pluginYaml = (viewingHistory ? versionContent.plugin_yaml_content : draft.plugin_yaml_content) || undefined;
  if (!pluginYaml && draft.name) {
    pluginYaml = `name: "${draft.name.replace(/"/g, '\\"')}"\n`;
  }
  // Extract plugin id from yaml for breadcrumb; fall back to draft.name.
  const breadcrumbLabel = (() => {
    const m = pluginYaml?.match(/^id:\s*["']?([^"'\n]+)["']?\s*$/m);
    return m?.[1]?.trim() || draft.name;
  })();

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
          message={getPhaseMessage('scenario_scripts')}
          description={t('selfEvolutionRun.pluginDetailPhase3Banner')}
        />
      )}

      {isFailed && !dismissedBanners.has('failed') && !repairModalOpen && (
        <Alert
          className="plugin-detail-banner"
          type="error"
          showIcon
          closable
          onClose={() => dismissBanner('failed')}
          message={t('selfEvolutionRun.pluginDetailFailedBanner')}
          description={draft.generate_error || undefined}
          action={
            <Button size="small" loading={isRegenerating} disabled={isRepairing} onClick={handleRegenerate}>
              {t('selfEvolutionRun.pluginDetailRegenerate')}
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
          message={t('selfEvolutionRun.pluginDetailGenerateWarningBanner')}
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
          message={draft.generate_warning.startsWith('[修复失败]') ? t('selfEvolutionRun.pluginDetailRepairFailedBanner') : t('selfEvolutionRun.pluginDetailPartialContentBanner')}
          description={draft.generate_warning}
        />
      )}

      {/* AI generation progress Modal — shown during Phase 0/1/2/3, not closable */}
      <Modal
        open={isStillGenerating && !isRepairing}
        closable={false}
        maskClosable={false}
        footer={null}
        width={480}
        centered
        className="plugin-generate-progress-modal"
      >
        <div className="plugin-generate-progress-body">
          <Spin size="large" />
          <p className="plugin-generate-progress-title">{getPhaseMessage(phase)}</p>
          <div className="plugin-generate-phase-steps">
            <div className={`phase-step ${phase === 'brief' ? 'active' : phase === 'skeleton' || phase === 'scenario_scripts' || phase === 'done' ? 'done' : ''}`}>
              {phase === 'brief' ? <SyncOutlined spin /> : <CheckCircleOutlined />}
              {' '}{t('selfEvolutionRun.pluginDetailGeneratePhase0')}
            </div>
            <div className={`phase-step ${phase === 'skeleton' ? 'active' : phase === 'scenario_scripts' || phase === 'done' ? 'done' : ''}`}>
              {phase === 'skeleton' ? <SyncOutlined spin /> : phase === 'scenario_scripts' || phase === 'done' ? <CheckCircleOutlined /> : null}
              {' '}{t('selfEvolutionRun.pluginDetailGeneratePhase1')}
            </div>
            <div className={`phase-step ${phase === 'scenario_scripts' ? 'active' : phase === 'done' ? 'done' : ''}`}>
              {phase === 'scenario_scripts' ? <SyncOutlined spin /> : phase === 'done' ? <CheckCircleOutlined /> : null}
              {' '}{t('selfEvolutionRun.pluginDetailGeneratePhase2')}
            </div>
            <div className={`phase-step ${phase === 'scenario_scripts' ? 'active' : phase === 'done' ? 'done' : ''}`}>
              {phase === 'scenario_scripts' ? <SyncOutlined spin /> : phase === 'done' ? <CheckCircleOutlined /> : null}
              {' '}{t('selfEvolutionRun.pluginDetailGeneratePhase3')}
            </div>
          </div>
          <p className="plugin-generate-progress-hint">{t('selfEvolutionRun.pluginDetailGenerateHint')}</p>
        </div>
      </Modal>

      {/* Editor area — always rendered so it's ready when generation completes */}
      <div className="plugin-detail-editor">
          {editorReady && isPhase3Running && (
            <div className="plugin-detail-phase-steps plugin-detail-phase-steps--inline">
              <div className="phase-step phase-step--done">
                <CheckCircleOutlined /> {t('selfEvolutionRun.pluginDetailPhaseLabelSkeleton')}
              </div>
              <div className="phase-step phase-step--done">
                <CheckCircleOutlined /> {t('selfEvolutionRun.pluginDetailPhaseLabelStatemachine')}
              </div>
              <div className="phase-step active">
                <SyncOutlined spin />
                {' '}{t('selfEvolutionRun.pluginDetailPhaseLabelDocs')}
              </div>
            </div>
          )}
          <StateGraphEditor
            key={`${draft.version}:${selectedRevision}`}
            initialStateYaml={stateYaml}
            initialPluginYaml={pluginYaml}
            initialScenarioContent={(viewingHistory ? versionContent.scenario_content : draft.scenario_content) || undefined}
            initialScriptsContent={(viewingHistory ? versionContent.scripts_content : draft.scripts_content) || undefined}
            onRepair={handleOpenRepair}
            readonly={viewingHistory || isRepairing || repairModalOpen}
            defaultShowArtifacts={showArtifactsRef.current}
            onArtifactsChange={(show) => { showArtifactsRef.current = show; }}
            designBriefContent={draft.design_brief_content || undefined}
            pluginName={
              <Space size={8}>
                <Breadcrumb items={[
                  { title: t('selfEvolutionRun.pluginDetailMyPlugins'), href: '/memory-management/plugins' },
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
                        title={t('selfEvolutionRun.pluginDetailEditNameTitle')}
                      >
                        {breadcrumbLabel}
                      </button>
                    ),
                  },
                ]} />
                <span>/</span>
                <Select
                  variant="borderless"
                  loading={switchingVersion}
                  value={selectedRevision}
                  style={{ minWidth: 110 }}
                  onChange={(value) => void handleVersionChange(value)}
                  options={[
                    { value: 'draft', label: '草稿' },
                    ...versions.map((item) => ({ value: item.revision_id, label: `v${item.revision_no}${item.current ? '（线上）' : ''}` })),
                  ]}
                />
              </Space>
            }
            topbarExtra={draft.published ? <Tag color="success" icon={<CheckCircleOutlined />}>线上：v{draft.current_revision_no}</Tag> : <Tag>未发布</Tag>}
            topbarActions={viewingHistory ? (
              <Button onClick={() => void handleEditHistoricalVersion()}>编辑此版本</Button>
            ) : editorReady ? (
              <Button type="primary" loading={publishing} disabled={(draft.published && !draft.draft_dirty) || isRepairing || isStillGenerating} title={draft.published && !draft.draft_dirty ? '草稿相对于基础版本没有变更' : undefined} onClick={handlePublish}>发布插件</Button>
            ) : null}
            onSave={handleSave}
            onClose={() => navigate('/memory-management/plugins')}
            showEmptyHint={showEmptyHint}
          />
        </div>
      {/* AI Repair Modal */}
      <Modal
        open={repairModalOpen}
        title={`${t('selfEvolutionRun.pluginDetailRepairModalTitle')} — ${repairTarget === 'scenario' ? t('selfEvolutionRun.pluginDetailRepairTargetScenario') : repairTarget === 'ui' ? t('selfEvolutionRun.pluginDetailRepairTargetUi') : t('selfEvolutionRun.pluginDetailRepairTargetStatemachine')}`}
        onCancel={() => {
          if (repairSubmitting || isRepairing) return;
          setRepairModalOpen(false);
          setRepairHint('');
          setRepairValidationErrors([]);
        }}
        closable={!repairSubmitting && !isRepairing}
        maskClosable={false}
        footer={repairSubmitting || isRepairing ? null : (
          <Button type="primary" onClick={handleRepair}>{t('selfEvolutionRun.pluginDetailRepairSubmit')}</Button>
        )}
      >
        {(repairSubmitting || isRepairing) ? (
          <div style={{ textAlign: 'center', padding: '32px 0' }}>
            <SyncOutlined spin style={{ fontSize: 36, color: '#1677ff' }} />
            <p style={{ marginTop: 16, fontSize: 15, fontWeight: 500 }}>{t('selfEvolutionRun.pluginDetailRepairInProgress')}</p>
            <p style={{ marginTop: 4, color: '#8c8c8c', fontSize: 13 }}>
              {repairTarget === 'scenario'
                ? t('selfEvolutionRun.pluginDetailRepairProgressScenario')
                : repairTarget === 'ui'
                  ? t('selfEvolutionRun.pluginDetailRepairProgressUi')
                  : t('selfEvolutionRun.pluginDetailRepairProgressStatemachine')}
            </p>
          </div>
        ) : (
          <>
            {repairValidationErrors.length > 0 && (
              <>
                <p style={{ marginBottom: 6 }}>{t('selfEvolutionRun.pluginDetailRepairValidationBasis')}</p>
                <ul style={{ margin: '0 0 12px 0', paddingLeft: 18, fontSize: 13, color: 'var(--color-text-secondary, #888)' }}>
                  {repairValidationErrors.map((e, i) => (
                    <li key={i}>{e.message}</li>
                  ))}
                </ul>
              </>
            )}
            <p style={{ marginBottom: 8 }}>{t('selfEvolutionRun.pluginDetailRepairHintLabel')}</p>
            <Input.TextArea
              placeholder={repairTarget === 'scenario' ? t('selfEvolutionRun.pluginDetailRepairScenarioPlaceholder') : t('selfEvolutionRun.pluginDetailRepairStatePlaceholder')}
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
