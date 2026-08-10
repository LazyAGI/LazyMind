import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { Button, Tooltip } from 'antd';
import ReactMarkdown from 'react-markdown';
import { useTranslation } from 'react-i18next';
import { isDeveloperModeActive } from '@/utils/developerMode';
import {
  CheckCircleOutlined,
  LoadingOutlined,
  PlusOutlined,
  AppstoreOutlined,
  SettingOutlined,
  FileOutlined,
  FolderOutlined,
  ToolOutlined,
} from '@ant-design/icons';
import type { GraphModel } from './core/model';
import { createEmptyModel, expressionMaterials, VIRTUAL_START } from './core/model';
import { parseYaml } from './core/parser';
import { serializeModel } from './core/serializer';
import { serializeLayout } from './core/layout';
import { validateStateGraph } from './core/validator';
import type { ValidationError } from './core/validator';
import { parseWorkflowYaml } from './core/workflowParser';
import { serializeWorkflowModel } from './core/workflowSerializer';
import type { WorkflowModel } from './core/workflowModel';
import { createEmptyWorkflowModel } from './core/workflowModel';
import type { ScenarioData } from './ScenarioEditor';
import { parseScenario, serializeScenario } from './ScenarioEditor';
import GraphCanvas from './GraphCanvas';
import type { CanvasHandle } from './GraphCanvas';
import ArtifactPanel from './ArtifactPanel';
import YamlEditor from './YamlEditor';
import ValidationPanel from './ValidationPanel';
import UiEditorPanel from './UiEditorPanel';
import WorkflowInfoModal from './WorkflowInfoModal';
import './index.scss';

// content tab: which "view" is active
type ContentTab = 'statemachine' | 'ui' | 'scenario';
// view mode: preview, code, or brief (AI design brief)
type ViewMode = 'preview' | 'code' | 'brief';
type SaveStatus = 'idle' | 'pending' | 'saving' | 'saved' | 'error';

export interface SkillConversionReport {
  coverage: unknown;
  toolMappings: unknown;
  scripts: unknown;
}

// code file derived from tab
type CodeFile = 'workflow.yaml' | 'state.yml' | 'scenario.md' | string;
type ScriptTreeNode = { name: string; path: string; children: ScriptTreeNode[]; filePath?: string };

// Map content tab to its default code file
function codeFileForTab(tab: ContentTab): CodeFile {
  if (tab === 'statemachine') return 'state.yml';
  if (tab === 'ui') return 'workflow.yaml';
  return 'scenario.md';
}

const AUTO_SAVE_DELAY_MS = 1500;

export type RepairTarget = 'statemachine' | 'ui' | 'scenario' | 'scripts' | 'full';

export interface SavePayload {
  stateYaml: string;
  workflowYaml: string;
  scenarioContent: string;
  scriptsContent: string;
  // Layout-only field: JSON-serialized GraphModel.layout (node positions/widths).
  // Stored in a separate DB column with last-write-wins; no version check.
  stateLayoutContent: string;
}

interface Props {
  initialStateYaml?: string;
  initialWorkflowYaml?: string;
  initialScenarioContent?: string;
  initialScriptsContent?: string;
  /** Workflow name shown in breadcrumb area (managed by parent) */
  workflowName?: React.ReactNode;
  topbarExtra?: React.ReactNode;
  topbarActions?: React.ReactNode;
  /** Called automatically when any file changes (auto-save). */
  onSave?: (payload: SavePayload) => Promise<void>;
  /** Runs Go's authoritative editor-profile validation after a successful save. */
  onValidate?: () => Promise<ValidationError[]>;
  onClose?: () => void;
  /** When false, the empty-canvas hint is suppressed (user already has experience). */
  showEmptyHint?: boolean;
  /** When true, all editing is disabled. onSave is ignored and all inputs become read-only. */
  readonly?: boolean;
  /**
   * Initial visibility of the artifacts panel. Defaults to true.
   * Pass false to keep the panel collapsed on remount (e.g. user closed it before a repair).
   */
  defaultShowArtifacts?: boolean;
  /** Called when the artifacts panel is opened or closed. Parent can persist this. */
  onArtifactsChange?: (show: boolean) => void;
  /**
   * When provided, an "AI 修复" button appears in the toolbar of each content tab.
   * `target` indicates which part the user wants to repair.
   * `validationErrors` carries the current graph validation errors (only for 'statemachine' target).
   */
  onRepair?: (target: RepairTarget, validationErrors?: ValidationError[]) => void;
  /** Design draft used as reference while converting a Skill into a Workflow. */
  designBriefContent?: string;
  /** Analysis produced by Skill-to-Workflow conversion. */
  skillConversionReport?: SkillConversionReport;
}

function parseScriptFiles(raw: string): Record<string, string> {
  try {
    const parsed = JSON.parse(raw || '{}');
    if (typeof parsed === 'string') return parseScriptFiles(parsed);
    if (typeof parsed === 'object' && parsed !== null) {
      const files: Record<string, string> = {};
      Object.entries(parsed as Record<string, unknown>).forEach(([path, content]) => {
        if (typeof content === 'string') files[path] = content;
      });
      return files;
    }
  } catch {}
  return {};
}

function scriptDisplayPath(path: string): string[] {
  return path.replace(/^scripts\//, '').split('/').filter(Boolean);
}

function buildScriptTree(paths: string[]): ScriptTreeNode[] {
  const root: ScriptTreeNode[] = [];
  const ensureDir = (nodes: ScriptTreeNode[], name: string, path: string): ScriptTreeNode => {
    let node = nodes.find((item) => item.name === name && !item.filePath);
    if (!node) {
      node = { name, path, children: [] };
      nodes.push(node);
    }
    return node;
  };
  [...paths].sort((a, b) => a.localeCompare(b)).forEach((filePath) => {
    const parts = scriptDisplayPath(filePath);
    let nodes = root;
    let prefix = '';
    parts.forEach((part, index) => {
      prefix = prefix ? `${prefix}/${part}` : part;
      if (index === parts.length - 1) {
        nodes.push({ name: part, path: prefix, children: [], filePath });
      } else {
        const dir = ensureDir(nodes, part, prefix);
        nodes = dir.children;
      }
    });
  });
  const sortNodes = (nodes: ScriptTreeNode[]) => {
    nodes.sort((a, b) => {
      if (Boolean(a.filePath) !== Boolean(b.filePath)) return a.filePath ? 1 : -1;
      return a.name.localeCompare(b.name);
    });
    nodes.forEach((node) => sortNodes(node.children));
  };
  sortNodes(root);
  return root;
}

function validationTargetNode(error: ValidationError, model: GraphModel): string | null {
  if (error.nodeId && (error.nodeId === VIRTUAL_START || model.nodes.some((node) => node.id === error.nodeId))) {
    return error.nodeId;
  }
  if (error.edgeKey) {
    const source = error.edgeKey.split('->')[0];
    if (source === VIRTUAL_START || model.nodes.some((node) => node.id === source)) return source;
  }
  if (error.materialId) {
    const related = model.nodes.find((node) => {
      const materials = [
        ...node.inputs.flatMap((input) => [input.material, ...(input.alternatives ?? [])]),
        ...node.outputs.map((output) => output.material),
        ...expressionMaterials(node.skipIf),
      ];
      return materials.includes(error.materialId!);
    });
    return related?.id ?? null;
  }
  return null;
}

/**
 * Build the initial GraphModel from state.yml + workflow.yaml.
 * Slots are always sourced from workflow.yaml (the single persistent store for slot definitions).
 * state.yml never contains slots; workflow.yaml is authoritative.
 */
function initGraphModel(stateYaml: string | undefined, workflowYaml: string | undefined): GraphModel {
  const base = stateYaml ? (parseYaml(stateYaml) ?? createEmptyModel()) : createEmptyModel();
  const slots: Record<string, import('./core/model').SlotDef> = {};
  if (workflowYaml) {
    const pm = parseWorkflowYaml(workflowYaml);
    if (pm) {
      for (const s of pm.slots) {
        slots[s.id] = {
          id: s.id, type: s.type, label: s.label,
          cardinality: s.cardinality, ordered: s.ordered,
          allow_manual_add: s.allow_manual_add, summary_max_chars: s.summary_max_chars,
          external: s.external,
        };
      }
    }
  }
  return { ...base, slots };
}

export default function StateGraphEditor({
  initialStateYaml,
  initialWorkflowYaml,
  initialScenarioContent,
  initialScriptsContent,
  workflowName,
  topbarExtra,
  topbarActions,
  onSave,
  onValidate,
  onClose,
  showEmptyHint = true,
  readonly = false,
  defaultShowArtifacts = false,
  onRepair,
  onArtifactsChange,
  designBriefContent,
  skillConversionReport,
}: Props) {
  const { t } = useTranslation();
  const [contentTab, setContentTab] = useState<ContentTab>('statemachine');
  const [viewMode, setViewMode] = useState<ViewMode>('preview');
  // In code mode, the active file is tracked independently of contentTab
  const [activeCodeFile, setActiveCodeFile] = useState<CodeFile>('state.yml');
  const [saveStatus, setSaveStatus] = useState<SaveStatus>('idle');
  const [showArtifacts, setShowArtifacts] = useState(defaultShowArtifacts);
  const [artifactAddRequest, setArtifactAddRequest] = useState(0);
  const toggleArtifacts = useCallback(() => {
    setShowArtifacts((v) => {
      const next = !v;
      onArtifactsChange?.(next);
      return next;
    });
  }, [onArtifactsChange]);
  const closeArtifacts = useCallback(() => {
    setShowArtifacts(false);
    onArtifactsChange?.(false);
  }, [onArtifactsChange]);
  const [workflowInfoOpen, setWorkflowInfoOpen] = useState(false);
  // Active UI tab — lifted from UiEditorPanel so TabBar removal doesn't lose state
  const [uiActiveTabId, setUiActiveTabId] = useState<string | undefined>(undefined);

  // Prevent macOS browser back/forward navigation when horizontally swiping
  // inside the editor. CSS overscroll-behavior-x:none on the canvas container
  // handles this; no JS wheel interception needed (it would break ReactFlow pan).
  const editorRootRef = useRef<HTMLDivElement>(null);

  // workflow.yaml model — must be initialized before GraphModel so we can back-fill slots.
  const [workflowModel, setWorkflowModel] = useState<WorkflowModel>(() => {
    const parsedWorkflow = initialWorkflowYaml
      ? (parseWorkflowYaml(initialWorkflowYaml) ?? createEmptyWorkflowModel())
      : createEmptyWorkflowModel();
    // state.yml is authoritative for the editable step list. Older/generated
    // drafts can have a populated state machine but no workflow.yaml `steps`
    // block; do not let the next auto-save serialize that omission back to disk.
    const graph = initialStateYaml ? parseYaml(initialStateYaml) : null;
    return graph
      ? { ...parsedWorkflow, steps: graph.nodes.map((node) => ({ id: node.id, label: node.label })) }
      : parsedWorkflow;
  });

  // state.yml model — back-fill slots from workflow.yaml when state.yml has none (post-migration drafts).
  const modelRef = useRef<GraphModel>(initGraphModel(initialStateYaml, initialWorkflowYaml));
  const [model, setModelState] = useState<GraphModel>(modelRef.current);
  const [errors, setErrors] = useState<ValidationError[]>(() => validateStateGraph(modelRef.current));
  const [authoritativeErrors, setAuthoritativeErrors] = useState<ValidationError[]>([]);
  const displayErrors = useMemo(() => [
    ...errors.filter((local) => !authoritativeErrors.some((server) => server.code === local.code)),
    ...authoritativeErrors,
  ], [errors, authoritativeErrors]);

  // scenario data
  const [scenarioData, setScenarioData] = useState<ScenarioData>(() =>
    parseScenario(initialScenarioContent ?? '', modelRef.current.nodes),
  );

  // scripts content (JSON string: { "path": "content" })
  const [scriptsContent, setScriptsContent] = useState(initialScriptsContent ?? '{}');
  const lastInitialScriptsContentRef = useRef(initialScriptsContent ?? '{}');

  // Undo history
  const historyRef = useRef<GraphModel[]>([]);
  const historyIndexRef = useRef<number>(-1);

  const canvasRef = useRef<CanvasHandle>(null);

  // Auto-save
  const autoSaveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const onSaveRef = useRef(onSave);
  useEffect(() => { onSaveRef.current = onSave; }, [onSave]);
  const onValidateRef = useRef(onValidate);
  useEffect(() => { onValidateRef.current = onValidate; }, [onValidate]);
  const validationRequestRef = useRef(0);

  const runAuthoritativeValidation = useCallback(async () => {
    const validate = onValidateRef.current;
    if (!validate) return;
    const requestId = ++validationRequestRef.current;
    try {
      const nextErrors = await validate();
      if (validationRequestRef.current === requestId) {
        setAuthoritativeErrors(nextErrors);
      }
    } catch {
      // A validation transport failure must not turn a successful save into a
      // save failure or erase the last known diagnostics.
    }
  }, []);

  // The Go validator is authoritative. Load its diagnostics as soon as an
  // editable draft opens instead of waiting for the first user modification.
  useEffect(() => {
    if (!readonly) void runAuthoritativeValidation();
    return () => {
      validationRequestRef.current += 1;
    };
  }, [readonly, runAuthoritativeValidation]);

  const buildPayload = useCallback((m: GraphModel, pm: WorkflowModel, sd: ScenarioData, sc: string): SavePayload => ({
    stateYaml: serializeModel(m, false),
    workflowYaml: serializeWorkflowModel(pm, m),
    scenarioContent: serializeScenario(m.nodes, sd),
    scriptsContent: sc,
    stateLayoutContent: serializeLayout(m),
  }), []);

  const doSave = useCallback(async (m: GraphModel, pm: WorkflowModel, sd: ScenarioData, sc: string) => {
    const fn = onSaveRef.current;
    if (!fn) return;
    if (autoSaveTimerRef.current) {
      clearTimeout(autoSaveTimerRef.current);
      autoSaveTimerRef.current = null;
    }
    setSaveStatus('saving');
    try {
      await fn(buildPayload(m, pm, sd, sc));
      setSaveStatus('saved');
      await runAuthoritativeValidation();
    } catch (error: unknown) {
      setSaveStatus('error');
    }
  }, [buildPayload, runAuthoritativeValidation]);

  const triggerAutoSave = useCallback((m: GraphModel, pm: WorkflowModel, sd: ScenarioData, sc: string) => {
    if (!onSaveRef.current) return;
    if (autoSaveTimerRef.current) clearTimeout(autoSaveTimerRef.current);
    setSaveStatus('pending');
    autoSaveTimerRef.current = setTimeout(() => void doSave(m, pm, sd, sc), AUTO_SAVE_DELAY_MS);
  }, [doSave]);

  const workflowModelRef = useRef(workflowModel);
  workflowModelRef.current = workflowModel;
  const scenarioDataRef = useRef(scenarioData);
  scenarioDataRef.current = scenarioData;
  const scriptsContentRef = useRef(scriptsContent);
  scriptsContentRef.current = scriptsContent;
  useEffect(() => {
    const next = initialScriptsContent ?? '{}';
    if (next === lastInitialScriptsContentRef.current) return;
    lastInitialScriptsContentRef.current = next;
    setScriptsContent(next);
    scriptsContentRef.current = next;
  }, [initialScriptsContent]);

  // Keyboard shortcuts
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 'z' && !e.shiftKey) {
        if (readonly) return;
        e.preventDefault();
        if (historyIndexRef.current < 0) return;
        const prev = historyRef.current[historyIndexRef.current];
        historyIndexRef.current -= 1;
        modelRef.current = prev;
        setModelState(prev);
        setErrors(validateStateGraph(prev));
        triggerAutoSave(prev, workflowModelRef.current, scenarioDataRef.current, scriptsContentRef.current);
      }
      if ((e.ctrlKey || e.metaKey) && e.key === 's') {
        if (readonly) return;
        e.preventDefault();
        void doSave(modelRef.current, workflowModelRef.current, scenarioDataRef.current, scriptsContentRef.current);
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [triggerAutoSave, doSave, readonly]);

  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const updateModel = useCallback((nextModel: GraphModel) => {
    const prev = modelRef.current;
    historyRef.current = historyRef.current.slice(0, historyIndexRef.current + 1);
    historyRef.current.push(prev);
    historyIndexRef.current = historyRef.current.length - 1;
    modelRef.current = nextModel;
    setModelState(nextModel);
    setErrors(validateStateGraph(nextModel));

    // Keep workflowModel step metadata and slots in sync with GraphModel.
    // A node rename must update workflow.yaml in the same auto-save; otherwise
    // the server correctly reports that the renamed state step is undeclared.
    // ArtifactPanel writes new slots only into GraphModel; syncing back here
    // ensures handleWorkflowModelChange never overwrites them with stale data.
    const stepsChanged = nextModel.nodes !== prev.nodes;
    const slotsChanged = nextModel.slots !== prev.slots;
    if (stepsChanged || slotsChanged) {
      const syncedSlots: import('./core/workflowModel').WorkflowSlotDef[] = Object.values(nextModel.slots).map((s) => ({
        id: s.id, type: s.type as import('./core/workflowModel').WorkflowSlotDef['type'],
        label: s.label, cardinality: s.cardinality, ordered: s.ordered,
        allow_manual_add: s.allow_manual_add, summary_max_chars: s.summary_max_chars,
        external: s.external,
      }));
      const syncedPm = {
        ...workflowModelRef.current,
        steps: nextModel.nodes.map((node) => ({ id: node.id, label: node.label })),
        slots: syncedSlots,
      };
      workflowModelRef.current = syncedPm;
      setWorkflowModel(syncedPm);
    }

    triggerAutoSave(nextModel, workflowModelRef.current, scenarioDataRef.current, scriptsContentRef.current);
  }, [triggerAutoSave]);

  // Functional-updater variant: applies an updater to the LATEST modelRef.current,
  // not to the stale React state. Used by ArtifactPanel so that slot changes never
  // overwrite layout.width values that were written since the last render.
  const updateModelFromUpdater = useCallback(
    (updater: (prev: GraphModel) => GraphModel) => {
      updateModel(updater(modelRef.current));
    },
    [updateModel],
  );

  const handleYamlChange = useCallback((text: string) => {
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => {
      const parsed = parseYaml(text);
      if (parsed) {
        const mergedModel: GraphModel = {
          ...parsed,
          layout: { ...parsed.layout, ...modelRef.current.layout },
          edgeLayout: { ...parsed.edgeLayout, ...modelRef.current.edgeLayout },
          // parseYaml always returns slots:{} — slot definitions live in workflow.yaml.
          // Preserve the current slots so editing state.yml never wipes them.
          slots: modelRef.current.slots,
        };
        modelRef.current = mergedModel;
        setModelState(mergedModel);
        setErrors(validateStateGraph(mergedModel));
        triggerAutoSave(mergedModel, workflowModelRef.current, scenarioDataRef.current, scriptsContentRef.current);
      } else {
        setErrors([{ code: 'V10_YAML_SYNTAX', message: t('selfEvolutionRun.sgeYamlSyntaxError') }]);
      }
    }, 500);
  }, [triggerAutoSave, t]);

  const handleWorkflowModelChange = useCallback((pm: WorkflowModel) => {
    setWorkflowModel(pm);
    workflowModelRef.current = pm;
    // Sync slots into both modelRef and React model state so ArtifactPanel always
    // displays the latest slot definitions. Previously only the ref was updated,
    // leaving the React state stale and causing ArtifactPanel to show old slots.
    const slots: Record<string, import('./core/model').SlotDef> = {};
    for (const s of pm.slots) {
      slots[s.id] = {
        id: s.id, type: s.type, label: s.label,
        cardinality: s.cardinality, ordered: s.ordered,
        allow_manual_add: s.allow_manual_add, summary_max_chars: s.summary_max_chars,
        external: s.external,
      };
    }
    const updatedModel = { ...modelRef.current, slots };
    modelRef.current = updatedModel;
    setModelState(updatedModel);
    triggerAutoSave(updatedModel, pm, scenarioDataRef.current, scriptsContentRef.current);
  }, [triggerAutoSave]);

  const handleScenarioChange = useCallback((sd: ScenarioData) => {
    setScenarioData(sd);
    scenarioDataRef.current = sd;
    triggerAutoSave(modelRef.current, workflowModelRef.current, sd, scriptsContentRef.current);
  }, [triggerAutoSave]);

  // Code-editor change handlers for workflow.yaml and scenario.md
  // workflow.yaml: parse → validate → sync both workflowModel and graphModel.slots
  const handleWorkflowYamlCodeChange = useCallback((text: string) => {
    const pm = parseWorkflowYaml(text);
    if (pm) {
      // Sync slots into GraphModel so serializeWorkflowModel always has the correct source
      const slots: Record<string, import('./core/model').SlotDef> = {};
      for (const s of pm.slots) {
        slots[s.id] = {
          id: s.id, type: s.type, label: s.label,
          cardinality: s.cardinality, ordered: s.ordered,
          allow_manual_add: s.allow_manual_add, summary_max_chars: s.summary_max_chars,
          external: s.external,
        };
      }
      const updatedModel: GraphModel = { ...modelRef.current, slots };
      modelRef.current = updatedModel;
      setModelState(updatedModel);

      setWorkflowModel(pm);
      workflowModelRef.current = pm;
      triggerAutoSave(updatedModel, pm, scenarioDataRef.current, scriptsContentRef.current);
      setErrors((prev) => prev.filter((e) => e.code !== 'V10_WORKFLOW_YAML_SYNTAX'));
    } else {
      setErrors((prev) => {
        const alreadyHas = prev.some((e) => e.code === 'V10_WORKFLOW_YAML_SYNTAX');
        if (alreadyHas) return prev;
        return [...prev, { code: 'V10_WORKFLOW_YAML_SYNTAX', message: t('selfEvolutionRun.sgeWorkflowYamlSyntaxError') }];
      });
    }
  }, [triggerAutoSave]);

  const handleScenarioMdCodeChange = useCallback((text: string) => {
    const sd = parseScenario(text, modelRef.current.nodes);
    handleScenarioChange(sd);
  }, [handleScenarioChange]);

  const handleScriptsChange = useCallback((path: string, content: string) => {
    const files = parseScriptFiles(scriptsContentRef.current);
    files[path] = content;
    const sc = JSON.stringify(files, null, 2);
    setScriptsContent(sc);
    scriptsContentRef.current = sc;
    triggerAutoSave(modelRef.current, workflowModelRef.current, scenarioDataRef.current, sc);
  }, [triggerAutoSave]);

  const handleWorkflowInfoSave = useCallback(async (pm: WorkflowModel, sd: ScenarioData) => {
    handleWorkflowModelChange(pm);
    handleScenarioChange(sd);
    await doSave(modelRef.current, pm, sd, scriptsContentRef.current);
  }, [handleWorkflowModelChange, handleScenarioChange, doSave]);

  const handleAddNode = useCallback(() => { canvasRef.current?.addNode(); }, []);
  const handleSelectNode = useCallback((nodeId: string) => {
    setViewMode('preview');
    setContentTab('statemachine');
    requestAnimationFrame(() => canvasRef.current?.focusNode(nodeId));
  }, []);

  // Switch to code mode, initialize activeCodeFile from current tab
  const handleEnterCode = useCallback(() => {
    setActiveCodeFile(codeFileForTab(contentTab));
    setViewMode('code');
  }, [contentTab]);

  // Exit code mode: derive contentTab from the file that was being edited
  const handleExitCode = useCallback(() => {
    if (activeCodeFile === 'state.yml') {
      setContentTab('statemachine');
    } else if (activeCodeFile === 'workflow.yaml') {
      setContentTab('ui');
    } else if (activeCodeFile === 'scenario.md') {
      setContentTab('scenario');
    } else {
      // script file — default to statemachine
      setContentTab('statemachine');
    }
    setViewMode('preview');
  }, [activeCodeFile]);

  const slotCount = Object.keys(model.slots).length;
  const scriptFiles = parseScriptFiles(scriptsContent);

  // Derive yaml text for code view of state.yml (x-layout is internal, not shown to users)
  const stateYamlForCode = readonly
    ? (initialStateYaml ?? serializeModel(model, false))
    : serializeModel(model, false);
  // scenario.md text
  const scenarioMdForCode = readonly
    ? (initialScenarioContent ?? serializeScenario(model.nodes, scenarioData))
    : serializeScenario(model.nodes, scenarioData);
  // workflow.yaml text
  const workflowYamlForCode = serializeWorkflowModel(workflowModel, model);

  // All files available in the file tree (code mode)
  const coreFiles: CodeFile[] = ['state.yml', 'workflow.yaml', 'scenario.md'];
  const scriptFilePaths = Object.keys(scriptFiles);
  const scriptTree = buildScriptTree(scriptFilePaths);
  const devMode = isDeveloperModeActive();

  const getCodeFileContent = (file: CodeFile): string => {
    if (file === 'workflow.yaml') return workflowYamlForCode;
    if (file === 'state.yml') return stateYamlForCode;
    if (file === 'scenario.md') return scenarioMdForCode;
    if (file === 'layout.json') return JSON.stringify(JSON.parse(serializeLayout(model)), null, 2);
    return scriptFiles[file] ?? '';
  };

  const renderScriptTree = (nodes: ScriptTreeNode[], depth = 0): ReactNode => nodes.map((node) => {
    if (!node.filePath) {
      return (
        <div key={`dir:${node.path}`}>
          <div className="sge-code-folder-item" style={{ paddingLeft: 12 + depth * 14 }}>
            <FolderOutlined className="sge-code-file-icon" />
            <span className="sge-code-file-name">{node.name}</span>
          </div>
          {renderScriptTree(node.children, depth + 1)}
        </div>
      );
    }
    return (
      <div
        key={node.filePath}
        className={`sge-code-file-item${activeCodeFile === node.filePath ? ' sge-code-file-item--active' : ''}`}
        style={{ paddingLeft: 12 + depth * 14 }}
        onClick={() => setActiveCodeFile(node.filePath!)}
      >
        <FileOutlined className="sge-code-file-icon" />
        <Tooltip title={node.filePath}>
          <span className="sge-code-file-name">{node.name}</span>
        </Tooltip>
      </div>
    );
  });


  return (
    <div ref={editorRootRef} className="state-graph-editor" aria-label={t('selfEvolutionRun.sgeEditorAriaLabel')}>
      {/* ── Row 1: back/breadcrumb left, save status + workflow config right ── */}
      <div className="sge-topbar">
        <div className="sge-topbar-left">
          {onClose && (
            <button className="sge-back-btn" onClick={onClose} aria-label={t('selfEvolutionRun.sgeBackAriaLabel')}>
              ←
            </button>
          )}
          {workflowName && <span className="sge-workflow-name">{workflowName}</span>}
        </div>
        <div className="sge-topbar-right">
          {!readonly && onSave && (
            <span className="sge-autosave-status">
              {saveStatus === 'pending' && <span className="sge-autosave-pending">{t('selfEvolutionRun.sgeSavePending')}</span>}
              {saveStatus === 'saving' && <span className="sge-autosave-saving"><LoadingOutlined /> {t('selfEvolutionRun.sgeSaving')}</span>}
              {saveStatus === 'saved' && <span className="sge-autosave-saved"><CheckCircleOutlined /> {t('selfEvolutionRun.sgeSaved')}</span>}
              {saveStatus === 'error' && <span className="sge-autosave-error">{t('selfEvolutionRun.sgeSaveError')}</span>}
            </span>
          )}
          {readonly && <span className="sge-readonly-badge">{t('selfEvolutionRun.sgeReadonlyBadge')}</span>}
          {topbarExtra}
          <Button size="small" icon={<SettingOutlined />} onClick={() => setWorkflowInfoOpen(true)}>
            {t('selfEvolutionRun.sgeWorkflowConfigBtn')}
          </Button>
          {topbarActions}
        </div>
      </div>

      {/* ── Row 2: content tabs + view switcher left, action buttons right ── */}
      <div className="sge-toolbar2">
        <div className="sge-toolbar2-left">
          {/* Capsule group 1: content tabs — disabled in code mode */}
          <div className="sge-segmented">
            {(['statemachine', 'ui', 'scenario'] as ContentTab[]).map((tab) => (
              <button
                key={tab}
                className={`sge-seg-btn${contentTab === tab ? ' sge-seg-btn--active' : ''}${(viewMode === 'code' || viewMode === 'brief') ? ' sge-seg-btn--disabled' : ''}`}
                onClick={() => { if (viewMode !== 'code' && viewMode !== 'brief') setContentTab(tab); }}
                disabled={viewMode === 'code' || viewMode === 'brief'}
                aria-disabled={viewMode === 'code' || viewMode === 'brief'}
              >
                {tab === 'statemachine' ? t('selfEvolutionRun.sgeTabStatemachine') : tab === 'ui' ? t('selfEvolutionRun.sgeTabUi') : t('selfEvolutionRun.sgeTabScenario')}
              </button>
            ))}
          </div>
          <Tooltip
            title={t('selfEvolutionRun.sgeTabHelpTooltip')}
            placement="bottom"
          >
            <span className="sge-tab-help-icon">?</span>
          </Tooltip>
          <span className="sge-tab-divider" />
          {/* Capsule group 2: view mode */}
          <div className="sge-segmented">
            <button
              className={`sge-seg-btn${viewMode === 'preview' ? ' sge-seg-btn--active' : ''}`}
              onClick={handleExitCode}
            >
              {t('selfEvolutionRun.sgeViewPreview')}
            </button>
            <button
              className={`sge-seg-btn${viewMode === 'code' ? ' sge-seg-btn--active' : ''}`}
              onClick={handleEnterCode}
            >
              {t('selfEvolutionRun.sgeViewCode')}
            </button>
            {(designBriefContent || skillConversionReport) && (
              <button
                className={`sge-seg-btn${viewMode === 'brief' ? ' sge-seg-btn--active' : ''}`}
                onClick={() => setViewMode('brief')}
              >
                {t('selfEvolutionRun.sgeViewBrief')}
              </button>
            )}
          </div>
        </div>
        <div className="sge-toolbar2-right">
          {!readonly && contentTab === 'statemachine' && viewMode === 'preview' && (
            <>
              {onRepair && (
                <Button size="small" icon={<ToolOutlined />} onClick={() => onRepair('statemachine', displayErrors)}>
                  {t('selfEvolutionRun.sgeAiRepairBtn')}
                </Button>
              )}
              <Button
                size="small"
                icon={<AppstoreOutlined />}
                onClick={() => toggleArtifacts()}
                type={showArtifacts ? 'primary' : 'default'}
              >
                {t('selfEvolutionRun.sgeArtifactsBtn')}{slotCount > 0 && <span className="sge-artifact-count">{slotCount}</span>}
              </Button>
              <Button size="small" icon={<PlusOutlined />} onClick={handleAddNode}>
                {t('selfEvolutionRun.sgeAddStepBtn')}
              </Button>
            </>
          )}
          {readonly && contentTab === 'statemachine' && viewMode === 'preview' && (
            <Button
              size="small"
              icon={<AppstoreOutlined />}
              onClick={() => toggleArtifacts()}
              type={showArtifacts ? 'primary' : 'default'}
            >
              {t('selfEvolutionRun.sgeArtifactsBtn')}{slotCount > 0 && <span className="sge-artifact-count">{slotCount}</span>}
            </Button>
          )}
          {!readonly && contentTab === 'ui' && viewMode === 'preview' && onRepair && (
            <Button size="small" icon={<ToolOutlined />} onClick={() => onRepair('ui')}>
              {t('selfEvolutionRun.sgeAiRepairBtn')}
            </Button>
          )}
          {!readonly && contentTab === 'scenario' && viewMode === 'preview' && onRepair && (
            <Button size="small" icon={<ToolOutlined />} onClick={() => onRepair('scenario')}>
              {t('selfEvolutionRun.sgeAiRepairBtn')}
            </Button>
          )}
        </div>
      </div>

      {/* ── Content area ── */}
      <div className="sge-body">
        {viewMode === 'preview' && contentTab === 'statemachine' && (
          <div className="sge-statemachine-panel">
            <div className="sge-content">
              <GraphCanvas
                model={model}
                errors={displayErrors}
                onModelChange={readonly ? () => {} : updateModel}
                workflowModel={workflowModel}
                scenarioData={scenarioData}
                onScenarioChange={readonly ? undefined : handleScenarioChange}
                canvasRef={canvasRef}
                readonly={readonly}
                onCreateArtifact={() => { setShowArtifacts(true); setArtifactAddRequest((value) => value + 1); }}
              />
              {model.nodes.length === 0 && showEmptyHint && (
                <div className="sge-empty-state" aria-hidden="true">
                  <div className="sge-empty-state-content">
                    <p className="sge-empty-state-title">{t('selfEvolutionRun.sgeEmptyStateTitle')}</p>
                    <ol className="sge-empty-state-list">
                      <li>{t('selfEvolutionRun.sgeEmptyStateStep1')}</li>
                      <li>{t('selfEvolutionRun.sgeEmptyStateStep2')}</li>
                      <li>{t('selfEvolutionRun.sgeEmptyStateStep3')}</li>
                    </ol>
                    <p className="sge-empty-state-hint">{t('selfEvolutionRun.sgeEmptyStateHint')}</p>
                  </div>
                </div>
              )}
              {showArtifacts && (
                <ArtifactPanel
                  model={model}
                  onClose={() => closeArtifacts()}
                  onModelChange={readonly ? () => {} : updateModelFromUpdater}
                  readonly={readonly}
                  startAddingToken={artifactAddRequest}
                />
              )}
            </div>
            <ValidationPanel
              errors={displayErrors}
              getTargetNodeId={(error) => validationTargetNode(error, model)}
              onSelectNode={handleSelectNode}
            />
          </div>
        )}

        {viewMode === 'preview' && contentTab === 'ui' && (
          <div className="sge-ui-editor-panel">
            <UiEditorPanel
              graphModel={model}
              workflowModel={workflowModel}
              onGraphModelChange={readonly ? () => {} : updateModelFromUpdater}
              onWorkflowModelChange={readonly ? () => {} : handleWorkflowModelChange}
              activeTabId={uiActiveTabId}
              onActiveTabChange={setUiActiveTabId}
              readonly={readonly}
            />
          </div>
        )}

        {viewMode === 'preview' && contentTab === 'scenario' && (
          <div className="sge-scenario-preview">
            <ReactMarkdown>{scenarioMdForCode}</ReactMarkdown>
          </div>
        )}

        {viewMode === 'code' && (
          <div className="sge-code-mode">
            {/* Left: file tree */}
            <div className="sge-code-sidebar">
              <div className="sge-code-sidebar-section">
                <span className="sge-code-sidebar-label">{t('selfEvolutionRun.sgeCodeSidebarCore')}</span>
                {coreFiles.map((file) => (
                  <div
                    key={file}
                    className={`sge-code-file-item${activeCodeFile === file ? ' sge-code-file-item--active' : ''}`}
                    onClick={() => setActiveCodeFile(file)}
                  >
                    <FileOutlined className="sge-code-file-icon" />
                    <span className="sge-code-file-name">{file}</span>
                  </div>
                ))}
              </div>
              {scriptFilePaths.length > 0 && (
                <div className="sge-code-sidebar-section">
                  <span className="sge-code-sidebar-label">{t('selfEvolutionRun.sgeCodeSidebarScript')}</span>
                  {renderScriptTree(scriptTree)}
                </div>
              )}
              {devMode && (
                <div className="sge-code-sidebar-section">
                  <span className="sge-code-sidebar-label sge-code-sidebar-label--dev">{t('selfEvolutionRun.sgeCodeSidebarDebug')}</span>
                  <div
                    className={`sge-code-file-item${activeCodeFile === 'layout.json' ? ' sge-code-file-item--active' : ''}`}
                    onClick={() => setActiveCodeFile('layout.json')}
                  >
                    <FileOutlined className="sge-code-file-icon" />
                    <span className="sge-code-file-name">layout.json</span>
                  </div>
                </div>
              )}
            </div>
            {/* Right: editor */}
            <div className="sge-code-editor">
              <YamlEditor
                key={activeCodeFile === 'layout.json' ? `layout.json-${serializeLayout(model)}` : activeCodeFile}
                value={getCodeFileContent(activeCodeFile)}
                onChange={(text) => {
                  if (readonly) return;
                  if (activeCodeFile === 'layout.json') return;
                  if (activeCodeFile === 'state.yml') {
                    handleYamlChange(text);
                  } else if (activeCodeFile === 'workflow.yaml') {
                    handleWorkflowYamlCodeChange(text);
                  } else if (activeCodeFile === 'scenario.md') {
                    handleScenarioMdCodeChange(text);
                  } else {
                    handleScriptsChange(activeCodeFile, text);
                  }
                }}
                errors={activeCodeFile === 'state.yml' ? displayErrors : []}
                readOnly={readonly || activeCodeFile === 'layout.json'}
                language={
                  activeCodeFile.endsWith('.md')
                    ? 'markdown'
                    : activeCodeFile.endsWith('.py')
                    ? 'python'
                    : 'yaml'
                }
              />
            </div>
          </div>
        )}

        {viewMode === 'brief' && (designBriefContent || skillConversionReport) && (
          <div className="sge-brief-preview">
            {designBriefContent && (
              <section className="sge-log-section">
                <h2>{t('selfEvolutionRun.sgeConversionDraftTitle')}</h2>
                <p className="sge-log-description">{t('selfEvolutionRun.sgeConversionDraftDescription')}</p>
                <pre className="sge-brief-content">{designBriefContent}</pre>
              </section>
            )}
            {skillConversionReport && (
              <section className="sge-log-section">
                <h2>{t('selfEvolutionRun.sgeConversionReportTitle')}</h2>
                <p className="sge-log-description">{t('selfEvolutionRun.sgeConversionReportDescription')}</p>
                <div className="sge-log-report-grid">
                  <div>
                    <h3>{t('selfEvolutionRun.workflowCoverageReport')}</h3>
                    <pre className="sge-brief-content">{JSON.stringify(skillConversionReport.coverage, null, 2)}</pre>
                  </div>
                  <div>
                    <h3>{t('selfEvolutionRun.workflowToolMappingReport')}</h3>
                    <pre className="sge-brief-content">{JSON.stringify(skillConversionReport.toolMappings, null, 2)}</pre>
                  </div>
                  <div>
                    <h3>{t('selfEvolutionRun.workflowScriptReport')}</h3>
                    <pre className="sge-brief-content">{JSON.stringify(skillConversionReport.scripts, null, 2)}</pre>
                  </div>
                </div>
              </section>
            )}
          </div>
        )}
      </div>

      <WorkflowInfoModal
        open={workflowInfoOpen}
        onCancel={() => setWorkflowInfoOpen(false)}
        workflowModel={workflowModel}
        scenarioData={scenarioData}
        onSave={readonly ? undefined : handleWorkflowInfoSave}
        readonly={readonly}
      />
    </div>
  );
}
