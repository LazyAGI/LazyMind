import { useState, useEffect, useCallback, useRef } from 'react';
import { useParams, useNavigate, useOutletContext } from 'react-router-dom';
import { Alert, Breadcrumb, Button, Modal, Input, Spin, Select, Space, Tag, message } from 'antd';
import { SyncOutlined, CheckCircleOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { localizeErrorCode } from '@/components/request';
import { getWorkflowDraft, listWorkflowDrafts, updateWorkflowDraftContent, aiGenerateWorkflowDraft, repairWorkflowDraft, publishWorkflowDraft, listWorkflowVersions, getWorkflowVersion, editWorkflowVersion, getWorkflowGenerationAnalysis, confirmWorkflowWorkflow, previewWorkflowRepair, getWorkflowRepairRun, validateWorkflowDraft } from '../../workflowDraftApi';
import type { WorkflowDraftRecord } from '../../workflowDraftApi';
import type { WorkflowVersionSummary, WorkflowVersionContent, WorkflowGenerationAnalysis, RepairPreview } from '../../workflowDraftApi';
import StateGraphEditor from '../../components/StateGraphEditor';
import type { SavePayload, RepairTarget } from '../../components/StateGraphEditor';
import type { ValidationError } from '../../components/StateGraphEditor/core/validator';
import './index.scss';

const POLL_INTERVAL_MS = 3000;

// generate_status values that indicate AI generation is still in progress.
const GENERATING_STATUSES = new Set(['analyzing', 'generating', 'brief_done', 'skeleton_done', 'state_done', 'repairing']);

// generate_status values where enough content is available to render the editor.
// state_done means workflow.yaml + state.yml are ready even though Phase 3 is still running.
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

function asSaveConflictError(error?: unknown): Error & { isSaveConflict: true } {
  const conflict = error instanceof Error ? error : new Error('workflow draft version conflict');
  return Object.assign(conflict, { isSaveConflict: true as const });
}

export default function WorkflowDetailPage() {
  const { workflowId } = useParams<{ workflowId: string }>();
  const navigate = useNavigate();
  const { t } = useTranslation();
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  useOutletContext<{ isMenuCollapsed: boolean; toggleMenu: () => void }>();

  const getPhaseMessage = (phase: GeneratePhase): string => {
    const map: Record<GeneratePhase, string> = {
      brief: t('selfEvolutionRun.workflowDetailPhaseBrief'),
      skeleton: t('selfEvolutionRun.workflowDetailPhaseSkeleton'),
      scenario_scripts: t('selfEvolutionRun.workflowDetailPhaseScenarioScripts'),
      repairing: t('selfEvolutionRun.workflowDetailPhaseRepairing'),
      done: '',
      failed: '',
      idle: '',
    };
    return map[phase] ?? '';
  };

  // Workflow editor opens as a Drawer over the content area; no need to collapse the sidebar.

  const [draft, setDraft] = useState<WorkflowDraftRecord | null>(null);
  const draftRef = useRef<WorkflowDraftRecord | null>(null);
  // Keep ref in sync for use in handleSave (avoids stale closure over version).
  useEffect(() => { draftRef.current = draft; }, [draft]);
  // Auto-saves must be applied in order: each successful write advances the
  // optimistic-lock version used by the next write.
  const saveQueueRef = useRef<Promise<void>>(Promise.resolve());
  // Once another editor/background job wins the optimistic lock, keep the
  // local canvas intact but stop sending writes until the user reloads.
  const saveConflictRef = useRef(false);
  // Persist artifacts panel open/close state across version remounts.
  // Default false — user explicitly opens the panel by clicking the 素材 button.
  const showArtifactsRef = useRef(false);
  const [loading, setLoading] = useState(true);
  const [isRegenerating, setIsRegenerating] = useState(false);
  const [repairModalOpen, setRepairModalOpen] = useState(false);
  // True while the :ai-repair API call is in-flight (keeps Modal open with a spinner).
  const [repairSubmitting, setRepairSubmitting] = useState(false);
  const [publishing, setPublishing] = useState(false);
  const [hasAuthoritativeErrors, setHasAuthoritativeErrors] = useState(false);
  const [versions, setVersions] = useState<WorkflowVersionSummary[]>([]);
  const [selectedRevision, setSelectedRevision] = useState<string>('draft');
  const [versionContent, setVersionContent] = useState<WorkflowVersionContent | null>(null);
  const [switchingVersion, setSwitchingVersion] = useState(false);
  const [repairHint, setRepairHint] = useState('');
  const [repairTarget, setRepairTarget] = useState<RepairTarget>('statemachine');
  const [repairValidationErrors, setRepairValidationErrors] = useState<ValidationError[]>([]);
  const [generationAnalysis, setGenerationAnalysis] = useState<WorkflowGenerationAnalysis | null>(null);
  const [confirmingCandidate, setConfirmingCandidate] = useState('');
  const [repairPreview, setRepairPreview] = useState<RepairPreview | null>(null);
  const [repairFailureDetails, setRepairFailureDetails] = useState<string[]>([]);
  const repairPreviewRequestRef = useRef(0);
  const prevStatusRef = useRef<string>('');
  // Per-banner dismissed state. Each banner has a unique key; dismissed keys are stored
  // as a JSON array in localStorage so they survive page refresh.
  // Keys: 'phase3' | 'failed' | 'generate_error' | 'generate_warning:<content_hash>'
  // The generate_warning key includes a hash of the content so that new warnings
  // (after a regenerate or repair) auto-reappear even if a previous warning was dismissed.
  const [dismissedBanners, setDismissedBanners] = useState<Set<string>>(() => {
    if (!workflowId) return new Set();
    try {
      const raw = localStorage.getItem(`workflow_banners_dismissed:${workflowId}`);
      return raw ? new Set(JSON.parse(raw) as string[]) : new Set();
    } catch {
      return new Set();
    }
  });

  const dismissBanner = useCallback((key: string) => {
    setDismissedBanners((prev) => {
      const next = new Set(prev);
      next.add(key);
      if (workflowId) {
        try {
          localStorage.setItem(`workflow_banners_dismissed:${workflowId}`, JSON.stringify([...next]));
        } catch { /* ignore */ }
      }
      return next;
    });
  }, [workflowId]);

  // Derive a short stable key for content-based banners so that new content clears
  // the dismissed state automatically. We use a simple djb2 hash — no crypto needed.
  const contentKey = useCallback((content: string): string => {
    let h = 5381;
    for (let i = 0; i < content.length; i++) h = ((h << 5) + h) ^ content.charCodeAt(i);
    return (h >>> 0).toString(36);
  }, []);
  const [editingName, setEditingName] = useState(false);
  const [nameValue, setNameValue] = useState('');
  // true = show empty-canvas hint; false = user already has experience (≥1 non-empty workflow)
  const [showEmptyHint, setShowEmptyHint] = useState(true);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const loadDraft = useCallback(async () => {
    if (!workflowId) return;
    setLoading(true);
    try {
      const data = await getWorkflowDraft(workflowId);
      setDraft(data);
      setNameValue(data.name);
    } catch {
      // API errors are reported by the shared request interceptor.
    } finally {
      setLoading(false);
    }
  }, [workflowId]);

  // Check whether the user already has at least one non-empty workflow (excluding the current one).
  // A workflow is considered non-empty when it has state_yaml_content / content, or generate_status is done/state_done.
  useEffect(() => {
    if (!workflowId) return;
    listWorkflowDrafts({ pageSize: 50 })
      .then(({ records }) => {
        const hasExperience = records.some(
          (r) =>
            r.id !== workflowId &&
            (r.state_yaml_content || r.content || r.workflow_yaml_content ||
              r.generate_status === 'done' || r.generate_status === 'state_done'),
        );
        if (hasExperience) setShowEmptyHint(false);
      })
      .catch(() => {});
  }, [workflowId]);

  const startPolling = useCallback(() => {
    if (pollRef.current) clearInterval(pollRef.current);
    pollRef.current = setInterval(async () => {
      if (!workflowId) return;
      try {
        const data = await getWorkflowDraft(
          workflowId,
          { silentError: true } as never,
        );
        setDraft(data);
        if (!GENERATING_STATUSES.has(data.generate_status)) {
          if (pollRef.current) clearInterval(pollRef.current);
          pollRef.current = null;
          const wasRepairing = prevStatusRef.current === 'repairing';
          if (wasRepairing) {
            let repairFailed = data.generate_warning?.startsWith('[修复失败]') ?? false;
            let repairDetails: string[] = [];
            if (data.last_repair_run_id) {
              try {
                const run = await getWorkflowRepairRun(workflowId, data.last_repair_run_id);
                repairDetails = Array.isArray(run.diagnostics_after)
                  ? run.diagnostics_after
                    .filter((item) => item.severity === 'error')
                    .map((item) => `${item.path}: ${localizeErrorCode(item.code, localizeErrorCode('2000509'))}`)
                  : [];
                repairFailed = run.status !== 'succeeded' || repairDetails.length > 0;
              } catch {
                // Keep the warning-prefix fallback when repair-run lookup is unavailable.
              }
            }
            // Close the repair Modal now that the job finished.
            setRepairModalOpen(false);
            setRepairHint('');
            setRepairValidationErrors([]);
            setRepairSubmitting(false);
            if (repairFailed) {
              setRepairFailureDetails([...new Set(repairDetails)]);
              // Clear only the generate_warning banner so it reappears with the new failure message.
              if (workflowId) {
                const warningKey = `generate_warning:${contentKey(data.generate_warning ?? '')}`;
                setDismissedBanners((prev) => {
                  const next = new Set([...prev].filter((k) => !k.startsWith('generate_warning:')));
                  try {
                    localStorage.setItem(`workflow_banners_dismissed:${workflowId}`, JSON.stringify([...next]));
                  } catch { /* ignore */ }
                  return next;
                });
                void warningKey; // used only for type-check
              }
            } else {
              setRepairFailureDetails([]);
              message.success(t('selfEvolutionRun.workflowDetailRepairSuccess'));
            }
          }
        }
        prevStatusRef.current = data.generate_status;
      } catch {
        // ignore polling errors
      }
    }, POLL_INTERVAL_MS);
  }, [workflowId]);

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
    if (!workflowId || !draft) return;
    setIsRegenerating(true);
    try {
      const updated = await aiGenerateWorkflowDraft(workflowId, {
        description: draft.content || draft.name,
      });
      setDraft(updated);
      // Clear all dismissed banners so the new generation result is fully visible.
      setDismissedBanners(new Set());
      if (workflowId) {
        try { localStorage.removeItem(`workflow_banners_dismissed:${workflowId}`); } catch { /* ignore */ }
      }
      startPolling();
    } catch {
      // API errors are reported by the shared request interceptor.
    } finally {
      setIsRegenerating(false);
    }
  }, [workflowId, draft, startPolling]);

  const handleRepair = useCallback(async () => {
    if (!workflowId) return;
    const hintSnapshot = repairHint.trim();
    const errorsSnapshot = repairValidationErrors;
    const targetSnapshot = repairTarget;
    try {
      let fullHint = hintSnapshot;
      if (errorsSnapshot.length > 0) {
        const errText = JSON.stringify(errorsSnapshot.map((e) => ({
          code: e.code,
          path: e.path,
          node_id: e.nodeId,
          edge_id: e.edgeKey,
          material_id: e.materialId,
          message: e.message,
          details: e.details,
        })), null, 2);
        fullHint = fullHint
          ? `${fullHint}\n\nAuthoritative validation diagnostics to fix:\n${errText}`
          : `Authoritative validation diagnostics to fix:\n${errText}`;
      }
      setRepairSubmitting(true);
      // Mark prevStatusRef as repairing BEFORE the API call so the polling
      // callback can correctly detect wasRepairing=true even on the first tick.
      prevStatusRef.current = 'repairing';
      // API returns immediately with generate_status=repairing.
      // Keep Modal open — it will show a loading UI until polling finishes.
      const updated = await repairWorkflowDraft(workflowId, {
        repair_hint: fullHint,
        target: targetSnapshot,
        mode: draftRef.current?.source_analysis_id ? 'source_aware' : 'workflow_local',
        draft_version: draftRef.current?.version || 0,
        source_analysis_id: draftRef.current?.source_analysis_id || undefined,
      });
      setDraft(updated);
      startPolling();
    } catch {
      setRepairSubmitting(false);
      // Reset prevStatusRef since we never entered repairing state.
      prevStatusRef.current = '';
      try {
        const latest = await getWorkflowDraft(workflowId);
        setDraft(latest);
      } catch { /* ignore */ }
    }
    // repairSubmitting stays true until polling ends (handled in startPolling callback)
  }, [workflowId, repairHint, repairValidationErrors, repairTarget, startPolling]);

  useEffect(() => {
    if (!workflowId || !draft?.source_analysis_id) return;
    getWorkflowGenerationAnalysis(workflowId).then(setGenerationAnalysis).catch(() => setGenerationAnalysis(null));
  }, [workflowId, draft?.source_analysis_id, draft?.generate_status]);

  const handleConfirmCandidate = useCallback(async (candidateId: string) => {
    if (!workflowId || !draft || !generationAnalysis) return;
    setConfirmingCandidate(candidateId);
    try {
      await confirmWorkflowWorkflow(workflowId,{analysis_id:generationAnalysis.analysis_id,candidate_id:candidateId,source_skill_revision_id:generationAnalysis.source_skill_revision_id,draft_version:draft.version});
      setDraft(await getWorkflowDraft(workflowId)); startPolling();
    } catch {}
    finally { setConfirmingCandidate(''); }
  },[workflowId,draft,generationAnalysis,startPolling]);

  useEffect(() => {
    if (!workflowId || !repairModalOpen) return;
    const requestId = ++repairPreviewRequestRef.current;
    setRepairPreview(null);
    previewWorkflowRepair(workflowId, {
      target: repairTarget,
      mode: draft?.source_analysis_id ? 'source_aware' : 'workflow_local',
    }).then((preview) => {
      if (repairPreviewRequestRef.current === requestId) setRepairPreview(preview);
    }).catch(() => {
      if (repairPreviewRequestRef.current === requestId) setRepairPreview(null);
    });
    return () => {
      if (repairPreviewRequestRef.current === requestId) repairPreviewRequestRef.current += 1;
    };
  }, [workflowId, repairModalOpen, repairTarget, draft?.source_analysis_id]);

  const handleOpenRepair = useCallback((target: RepairTarget, validationErrors?: ValidationError[]) => {
    setRepairPreview(null);
    setRepairTarget(target);
    setRepairValidationErrors(validationErrors ?? []);
    setRepairModalOpen(true);
  }, []);

  const handleSave = useCallback(
    (payload: SavePayload) => {
      if (!workflowId) return Promise.resolve();
      const save = saveQueueRef.current.catch(() => undefined).then(async () => {
        if (saveConflictRef.current) {
          throw asSaveConflictError();
        }
        const currentVersion = draftRef.current?.version ?? 0;
        let updated: WorkflowDraftRecord;
        try {
          updated = await updateWorkflowDraftContent(workflowId, {
            state_yaml_content: payload.stateYaml,
            state_layout_content: payload.stateLayoutContent,
            workflow_yaml_content: payload.workflowYaml,
            scenario_content: payload.scenarioContent,
            scripts_content: payload.scriptsContent,
            version: currentVersion,
          });
        } catch (err: unknown) {
          // A stale version means another window or a background job changed
          // the draft. Never adopt its version and retry the whole local
          // snapshot: that would silently overwrite the other writer.
          const response = (err as { response?: { status?: number; data?: { message?: string } } })?.response;
          if (response?.status === 409) {
            if (response.data?.message?.includes('workflow id already exists')) {
              throw err;
            }
            if (response.data?.message === 'conflict') {
              saveConflictRef.current = true;
              throw asSaveConflictError(err);
            }
          }
          throw err;
        }
        // Update the ref synchronously so the next queued save uses this
        // response's version even before React commits setDraft.
        draftRef.current = updated;
        setDraft(updated);
      });
      saveQueueRef.current = save;
      return save;
    },
    [workflowId, t],
  );

  const handleValidate = useCallback(async (): Promise<ValidationError[]> => {
    if (!workflowId) return [];
    const result = await validateWorkflowDraft(workflowId);
    const diagnostics = result.diagnostics.map((item) => ({
      code: item.code,
      message: item.message,
      severity: item.severity,
      path: item.path,
      nodeId: item.node_id,
      edgeKey: item.edge_id,
      materialId: item.material_id,
      details: item.details as Record<string, unknown> | undefined,
    }));
    setHasAuthoritativeErrors(result.diagnostics.some((item) => item.severity === 'error'));
    return diagnostics;
  }, [workflowId]);

  const handlePublish = useCallback(async () => {
    if (!draft) return;
    setPublishing(true);
    try {
      const result = await publishWorkflowDraft(draft.id);
      message.success(`Workflow 已发布为版本 ${result.revision_no}，默认关闭`);
      setVersions(await listWorkflowVersions(result.workflow_ref));
      setDraft(await getWorkflowDraft(draft.id));
    } catch {
      // API errors are reported by the shared request interceptor.
    } finally {
      setPublishing(false);
    }
  }, [draft]);

  useEffect(() => {
    if (!draft?.published_workflow_ref) { setVersions([]); return; }
    void listWorkflowVersions(draft.published_workflow_ref).then(setVersions).catch(() => setVersions([]));
  }, [draft?.published_workflow_ref]);

  const handleVersionChange = useCallback(async (value: string) => {
    if (value === 'draft') { setSelectedRevision('draft'); setVersionContent(null); return; }
    if (!draft?.published_workflow_ref) return;
    const loadVersion = async () => {
      setSelectedRevision(value); setSwitchingVersion(true);
      try { setVersionContent(await getWorkflowVersion(draft.published_workflow_ref, value)); }
      catch { setSelectedRevision('draft'); }
      finally { setSwitchingVersion(false); }
    };
    if (draft.draft_dirty) {
      Modal.confirm({ title: '当前草稿有未发布的修改', content: '历史版本将以只读方式打开。若随后点击“编辑此版本”，当前草稿会被清空并替换为该历史版本。', okText: '继续查看', cancelText: '取消', onOk: loadVersion });
      return;
    }
    await loadVersion();
  }, [draft?.published_workflow_ref, draft?.draft_dirty]);

  const handleEditHistoricalVersion = useCallback(async () => {
    if (!draft?.published_workflow_ref || selectedRevision === 'draft') return;
    Modal.confirm({ title: '用此版本替换当前草稿？', content: '当前草稿内容会被选定历史版本覆盖，此操作不会修改已发布版本。', okText: '替换并编辑', onOk: async () => {
      const next = await editWorkflowVersion(draft.published_workflow_ref, selectedRevision); setDraft(next); setVersionContent(null); setSelectedRevision('draft'); message.success('草稿已替换为选定版本');
    }});
  }, [draft?.published_workflow_ref, selectedRevision]);

  if (loading) {
    return (
      <div className="workflow-editor-overlay">
        <div className="workflow-editor-mask" />
        <div className="workflow-editor-panel">
          <div className="workflow-detail-loading"><Spin tip={t('selfEvolutionRun.workflowDetailLoading')} /></div>
        </div>
      </div>
    );
  }

  if (!draft) {
    return (
      <div className="workflow-editor-overlay">
        <div className="workflow-editor-mask" />
        <div className="workflow-editor-panel">
          <div className="workflow-detail-error"><p>{t('selfEvolutionRun.workflowDetailNotFound')}</p></div>
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
      const layoutObj = JSON.parse(draft.state_layout_content) as Record<string, unknown>;
      if (Object.keys(layoutObj).length > 0) {
        // Prepend x-layout block to state YAML so the parser picks it up.
        // Support both 'w' (legacy) and 'width' (current NodeLayout field name).
        const layoutYaml = `x-layout:\n${Object.entries(layoutObj)
          .map(([id, value]) => `  ${JSON.stringify(id)}: ${JSON.stringify(value)}`)
          .join('\n')}\n`;
        stateYaml = layoutYaml + rawStateYaml;
      }
    } catch {
      // ignore malformed layout JSON
    }
  }
  let workflowYaml = (viewingHistory ? versionContent.workflow_yaml_content : draft.workflow_yaml_content) || undefined;
  if (!workflowYaml && draft.name) {
    workflowYaml = `name: "${draft.name.replace(/"/g, '\\"')}"\n`;
  }
  // Extract workflow id from yaml for breadcrumb; fall back to draft.name.
  const breadcrumbLabel = (() => {
    const m = workflowYaml?.match(/^id:\s*["']?([^"'\n]+)["']?\s*$/m);
    return m?.[1]?.trim() || draft.name;
  })();

  return (
    <div className="workflow-editor-overlay">
      <div className="workflow-editor-mask" />
      <div className="workflow-editor-panel">
    <div className="workflow-detail-page">
      {draft.generate_status === 'needs_confirmation' && generationAnalysis && (
        <Alert className="workflow-detail-banner" type="warning" showIcon message={t('selfEvolutionRun.workflowWorkflowChoose')} description={<Space direction="vertical"><span>{generationAnalysis.message}</span>{Object.entries(generationAnalysis.scripts).filter(([,report])=>report.classification==='unsupported').map(([path,report])=><span key={path}>{t('selfEvolutionRun.workflowUnsafeScriptIgnored',{path,reason:report.reason || t('selfEvolutionRun.workflowUnsafeScriptReason')})}</span>)}{generationAnalysis.candidates.map(candidate => <Button key={candidate.id} loading={confirmingCandidate===candidate.id} onClick={()=>handleConfirmCandidate(candidate.id)}>{candidate.name || candidate.goal || candidate.id}</Button>)}</Space>} />
      )}
      {draft.generate_status === 'rejected' && (
        <Alert className="workflow-detail-banner" type="error" showIcon message={t('selfEvolutionRun.workflowWorkflowRejected')} description={localizeErrorCode('2000509')} />
      )}
      {/* Generation progress banner — shown while Phase 3 is still running (editor already ready) */}
      {isPhase3Running && !repairModalOpen && (
        <Alert
          className="workflow-detail-banner"
          type="info"
          icon={<SyncOutlined spin />}
          showIcon
          message={getPhaseMessage('scenario_scripts')}
          description={t('selfEvolutionRun.workflowDetailPhase3Banner')}
        />
      )}

      {isFailed && !dismissedBanners.has('failed') && !repairModalOpen && (
        <Alert
          className="workflow-detail-banner"
          type="error"
          showIcon
          closable
          onClose={() => dismissBanner('failed')}
          message={t('selfEvolutionRun.workflowDetailFailedBanner')}
          description={localizeErrorCode('2000509')}
          action={
            <Button size="small" loading={isRegenerating} disabled={isRepairing} onClick={handleRegenerate}>
              {t('selfEvolutionRun.workflowDetailRegenerate')}
            </Button>
          }
        />
      )}

      {!isFailed && draft.generate_status === 'done' && draft.generate_error && !dismissedBanners.has('generate_error') && !repairModalOpen && (
        <Alert
          className="workflow-detail-banner"
          type="warning"
          showIcon
          closable
          onClose={() => dismissBanner('generate_error')}
          message={t('selfEvolutionRun.workflowDetailGenerateWarningBanner')}
          description={localizeErrorCode('2000509')}
        />
      )}

      {draft.generate_status === 'done' && draft.generate_warning && !dismissedBanners.has(`generate_warning:${contentKey(draft.generate_warning)}`) && !repairModalOpen && (
        <Alert
          className="workflow-detail-banner"
          type={draft.generate_warning.startsWith('[修复失败]') ? 'error' : 'warning'}
          showIcon
          closable
          onClose={() => dismissBanner(`generate_warning:${contentKey(draft.generate_warning)}`)}
          message={draft.generate_warning.startsWith('[修复失败]') ? t('selfEvolutionRun.workflowDetailRepairFailedBanner') : t('selfEvolutionRun.workflowDetailPartialContentBanner')}
          description={repairFailureDetails.length > 0
            ? <ul>{repairFailureDetails.map((detail) => <li key={detail}>{detail}</li>)}</ul>
            : localizeErrorCode('2000509')}
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
        className="workflow-generate-progress-modal"
      >
        <div className="workflow-generate-progress-body">
          <Spin size="large" />
          <p className="workflow-generate-progress-title">{getPhaseMessage(phase)}</p>
          <div className="workflow-generate-phase-steps">
            <div className={`phase-step ${phase === 'brief' ? 'active' : phase === 'skeleton' || phase === 'scenario_scripts' || phase === 'done' ? 'done' : ''}`}>
              {phase === 'brief' ? <SyncOutlined spin /> : <CheckCircleOutlined />}
              {' '}{t('selfEvolutionRun.workflowDetailGeneratePhase0')}
            </div>
            <div className={`phase-step ${phase === 'skeleton' ? 'active' : phase === 'scenario_scripts' || phase === 'done' ? 'done' : ''}`}>
              {phase === 'skeleton' ? <SyncOutlined spin /> : phase === 'scenario_scripts' || phase === 'done' ? <CheckCircleOutlined /> : null}
              {' '}{t('selfEvolutionRun.workflowDetailGeneratePhase1')}
            </div>
            <div className={`phase-step ${phase === 'scenario_scripts' ? 'active' : phase === 'done' ? 'done' : ''}`}>
              {phase === 'scenario_scripts' ? <SyncOutlined spin /> : phase === 'done' ? <CheckCircleOutlined /> : null}
              {' '}{t('selfEvolutionRun.workflowDetailGeneratePhase2')}
            </div>
            <div className={`phase-step ${phase === 'scenario_scripts' ? 'active' : phase === 'done' ? 'done' : ''}`}>
              {phase === 'scenario_scripts' ? <SyncOutlined spin /> : phase === 'done' ? <CheckCircleOutlined /> : null}
              {' '}{t('selfEvolutionRun.workflowDetailGeneratePhase3')}
            </div>
          </div>
          <p className="workflow-generate-progress-hint">{t('selfEvolutionRun.workflowDetailGenerateHint')}</p>
        </div>
      </Modal>

      {/* Editor area — always rendered so it's ready when generation completes */}
      <div className="workflow-detail-editor">
          {editorReady && isPhase3Running && (
            <div className="workflow-detail-phase-steps workflow-detail-phase-steps--inline">
              <div className="phase-step phase-step--done">
                <CheckCircleOutlined /> {t('selfEvolutionRun.workflowDetailPhaseLabelSkeleton')}
              </div>
              <div className="phase-step phase-step--done">
                <CheckCircleOutlined /> {t('selfEvolutionRun.workflowDetailPhaseLabelStatemachine')}
              </div>
              <div className="phase-step active">
                <SyncOutlined spin />
                {' '}{t('selfEvolutionRun.workflowDetailPhaseLabelDocs')}
              </div>
            </div>
          )}
          <StateGraphEditor
            key={`${draft.generate_status}:${selectedRevision}`}
            initialStateYaml={stateYaml}
            initialWorkflowYaml={workflowYaml}
            initialScenarioContent={(viewingHistory ? versionContent.scenario_content : draft.scenario_content) || undefined}
            initialScriptsContent={(viewingHistory ? versionContent.scripts_content : draft.scripts_content) || undefined}
            onRepair={handleOpenRepair}
            readonly={viewingHistory || isRepairing || repairModalOpen}
            defaultShowArtifacts={showArtifactsRef.current}
            onArtifactsChange={(show) => { showArtifactsRef.current = show; }}
            designBriefContent={draft.design_brief_content || undefined}
            skillConversionReport={generationAnalysis && draft.generate_status !== 'needs_confirmation' ? {
              coverage: generationAnalysis.coverage,
              toolMappings: generationAnalysis.tool_mappings,
              scripts: generationAnalysis.scripts,
            } : undefined}
            workflowName={
              <Space size={8}>
                <Breadcrumb items={[
                  { title: t('selfEvolutionRun.workflowDetailMyWorkflows'), href: '/memory-management/workflows' },
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
                        className="workflow-detail-name"
                        onClick={() => setEditingName(true)}
                        title={t('selfEvolutionRun.workflowDetailEditNameTitle')}
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
              <Button type="primary" loading={publishing} disabled={hasAuthoritativeErrors || (draft.published && !draft.draft_dirty) || isRepairing || isStillGenerating} title={hasAuthoritativeErrors ? '请先修复 Go 校验返回的错误' : draft.published && !draft.draft_dirty ? '草稿相对于基础版本没有变更' : undefined} onClick={handlePublish}>发布插件</Button>
            ) : null}
            onSave={handleSave}
            onValidate={handleValidate}
            onClose={() => navigate('/memory-management/workflows')}
            showEmptyHint={showEmptyHint}
          />
        </div>
      {/* AI Repair Modal */}
      <Modal
        open={repairModalOpen}
        title={t('selfEvolutionRun.workflowDetailRepairModalTitle')}
        onCancel={() => {
          if (repairSubmitting || isRepairing) return;
          setRepairModalOpen(false);
          setRepairPreview(null);
          setRepairHint('');
          setRepairValidationErrors([]);
        }}
        closable={!repairSubmitting && !isRepairing}
        maskClosable={false}
        footer={repairSubmitting || isRepairing ? null : (
          <Button type="primary" onClick={handleRepair}>{t('selfEvolutionRun.workflowDetailRepairSubmit')}</Button>
        )}
      >
        {(repairSubmitting || isRepairing) ? (
          <div style={{ textAlign: 'center', padding: '32px 0' }}>
            <SyncOutlined spin style={{ fontSize: 36, color: '#1677ff' }} />
            <p style={{ marginTop: 16, fontSize: 15, fontWeight: 500 }}>{t('selfEvolutionRun.workflowDetailRepairInProgress')}</p>
            <p style={{ marginTop: 4, color: '#8c8c8c', fontSize: 13 }}>
              {repairTarget === 'scenario'
                ? t('selfEvolutionRun.workflowDetailRepairProgressScenario')
                : repairTarget === 'ui'
                  ? t('selfEvolutionRun.workflowDetailRepairProgressUi')
                  : t('selfEvolutionRun.workflowDetailRepairProgressStatemachine')}
            </p>
          </div>
        ) : (
          <>
            <Select value={repairTarget} onChange={(value)=>setRepairTarget(value)} style={{width:'100%',marginBottom:12}} options={[{value:'statemachine',label:t('selfEvolutionRun.workflowDetailRepairTargetStatemachine')},{value:'ui',label:t('selfEvolutionRun.workflowDetailRepairTargetUi')},{value:'scenario',label:t('selfEvolutionRun.workflowDetailRepairTargetScenario')},{value:'scripts',label:t('selfEvolutionRun.workflowDetailRepairTargetScripts')},{value:'full',label:t('selfEvolutionRun.workflowDetailRepairTargetFull')}]} />
            {(repairTarget === 'statemachine' || repairTarget === 'full') && repairValidationErrors.length > 0 && (
              <>
                <p style={{ marginBottom: 6 }}>{t('selfEvolutionRun.workflowDetailRepairValidationBasis')}</p>
                <ul style={{ margin: '0 0 12px 0', paddingLeft: 18, fontSize: 13, color: 'var(--color-text-secondary, #888)' }}>
                  {repairValidationErrors.map((e, i) => (
                    <li key={i}>{t(`selfEvolutionRun.validationErrors.${e.code}`, {
                      defaultValue: e.message,
                      node: e.nodeId ?? '',
                      edge: e.edgeKey ?? '',
                      material: e.materialId ?? '',
                      producer: String(e.details?.producer_step_id ?? e.details?.previous_producer ?? ''),
                    })}</li>
                  ))}
                </ul>
              </>
            )}
            {repairPreview && <Alert type="info" showIcon message={t('selfEvolutionRun.workflowRepairPreview')} description={<><div>{(repairPreview.planned_files ?? []).join(', ')}</div>{(repairPreview.diagnostics ?? []).map(item=><div key={`${item.code}:${item.path}`}>{(item.severity || 'error').toUpperCase()} {item.path}: {localizeErrorCode(item.code, localizeErrorCode('2000509'))}</div>)}</>} />}
            <p style={{ marginBottom: 8 }}>{t('selfEvolutionRun.workflowDetailRepairHintLabel')}</p>
            <Input.TextArea
              placeholder={repairTarget === 'scenario' ? t('selfEvolutionRun.workflowDetailRepairScenarioPlaceholder') : t('selfEvolutionRun.workflowDetailRepairStatePlaceholder')}
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
