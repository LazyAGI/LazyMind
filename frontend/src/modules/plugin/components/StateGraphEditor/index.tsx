import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, message, Tooltip } from 'antd';
import ReactMarkdown from 'react-markdown';
import { isDeveloperModeActive } from '@/utils/developerMode';
import {
  CheckCircleOutlined,
  LoadingOutlined,
  PlusOutlined,
  AppstoreOutlined,
  SettingOutlined,
  FileOutlined,
} from '@ant-design/icons';
import type { GraphModel } from './core/model';
import { createEmptyModel } from './core/model';
import { parseYaml } from './core/parser';
import { serializeModel } from './core/serializer';
import { validateStateGraph } from './core/validator';
import type { ValidationError } from './core/validator';
import { parsePluginYaml } from './core/pluginParser';
import { serializePluginModel } from './core/pluginSerializer';
import type { PluginModel } from './core/pluginModel';
import { createEmptyPluginModel } from './core/pluginModel';
import type { ScenarioData } from './ScenarioEditor';
import { parseScenario, serializeScenario } from './ScenarioEditor';
import GraphCanvas from './GraphCanvas';
import type { CanvasHandle } from './GraphCanvas';
import ArtifactPanel from './ArtifactPanel';
import YamlEditor from './YamlEditor';
import ValidationPanel from './ValidationPanel';
import UiEditorPanel from './UiEditorPanel';
import PluginInfoModal from './PluginInfoModal';
import './index.scss';

// content tab: which "view" is active
type ContentTab = 'statemachine' | 'ui' | 'scenario';
// view mode: preview or code
type ViewMode = 'preview' | 'code';
type SaveStatus = 'idle' | 'pending' | 'saving' | 'saved' | 'error';

// code file derived from tab
type CodeFile = 'plugin.yaml' | 'state.yml' | 'scenario.md' | string;

// Map content tab to its default code file
function codeFileForTab(tab: ContentTab): CodeFile {
  if (tab === 'statemachine') return 'state.yml';
  if (tab === 'ui') return 'plugin.yaml';
  return 'scenario.md';
}

const AUTO_SAVE_DELAY_MS = 1500;

export interface SavePayload {
  stateYaml: string;
  pluginYaml: string;
  scenarioContent: string;
  scriptsContent: string;
  // Layout-only field: JSON-serialized GraphModel.layout (node positions/widths).
  // Stored in a separate DB column with last-write-wins; no version check.
  stateLayoutContent: string;
}

interface Props {
  initialStateYaml?: string;
  initialPluginYaml?: string;
  initialScenarioContent?: string;
  initialScriptsContent?: string;
  /** Plugin name shown in breadcrumb area (managed by parent) */
  pluginName?: React.ReactNode;
  /** Called automatically when any file changes (auto-save). */
  onSave?: (payload: SavePayload) => Promise<void>;
  onClose?: () => void;
  /** When false, the empty-canvas hint is suppressed (user already has experience). */
  showEmptyHint?: boolean;
}

function parseScriptFiles(raw: string): Record<string, string> {
  try {
    const parsed = JSON.parse(raw || '{}');
    if (typeof parsed === 'object' && parsed !== null) return parsed as Record<string, string>;
  } catch {}
  return {};
}

/**
 * Build the initial GraphModel from state.yml + plugin.yaml.
 * Slots are always sourced from plugin.yaml (the single persistent store for slot definitions).
 * state.yml never contains slots; plugin.yaml is authoritative.
 */
function initGraphModel(stateYaml: string | undefined, pluginYaml: string | undefined): GraphModel {
  const base = stateYaml ? (parseYaml(stateYaml) ?? createEmptyModel()) : createEmptyModel();
  const slots: Record<string, import('./core/model').SlotDef> = {};
  if (pluginYaml) {
    const pm = parsePluginYaml(pluginYaml);
    if (pm) {
      for (const s of pm.slots) {
        slots[s.id] = {
          id: s.id, type: s.type, label: s.label,
          cardinality: s.cardinality, ordered: s.ordered,
          allow_manual_add: s.allow_manual_add, summary_max_chars: s.summary_max_chars,
        };
      }
    }
  }
  return { ...base, slots };
}

export default function StateGraphEditor({
  initialStateYaml,
  initialPluginYaml,
  initialScenarioContent,
  initialScriptsContent,
  pluginName,
  onSave,
  onClose,
  showEmptyHint = true,
}: Props) {
  const [contentTab, setContentTab] = useState<ContentTab>('statemachine');
  const [viewMode, setViewMode] = useState<ViewMode>('preview');
  // In code mode, the active file is tracked independently of contentTab
  const [activeCodeFile, setActiveCodeFile] = useState<CodeFile>('state.yml');
  const [saveStatus, setSaveStatus] = useState<SaveStatus>('idle');
  const [showArtifacts, setShowArtifacts] = useState(true);
  const [pluginInfoOpen, setPluginInfoOpen] = useState(false);
  // Active UI tab — lifted from UiEditorPanel so TabBar removal doesn't lose state
  const [uiActiveTabId, setUiActiveTabId] = useState<string | undefined>(undefined);

  // Prevent macOS browser back/forward navigation when horizontally swiping
  // inside the editor. CSS overscroll-behavior-x:none on the canvas container
  // handles this; no JS wheel interception needed (it would break ReactFlow pan).
  const editorRootRef = useRef<HTMLDivElement>(null);

  // plugin.yaml model — must be initialized before GraphModel so we can back-fill slots.
  const [pluginModel, setPluginModel] = useState<PluginModel>(() =>
    initialPluginYaml ? (parsePluginYaml(initialPluginYaml) ?? createEmptyPluginModel()) : createEmptyPluginModel(),
  );

  // state.yml model — back-fill slots from plugin.yaml when state.yml has none (post-migration drafts).
  const modelRef = useRef<GraphModel>(initGraphModel(initialStateYaml, initialPluginYaml));
  const [model, setModelState] = useState<GraphModel>(modelRef.current);
  const [errors, setErrors] = useState<ValidationError[]>(() => validateStateGraph(modelRef.current));

  // scenario data
  const [scenarioData, setScenarioData] = useState<ScenarioData>(() =>
    parseScenario(initialScenarioContent ?? '', modelRef.current.nodes),
  );

  // scripts content (JSON string: { "path": "content" })
  const [scriptsContent, setScriptsContent] = useState(initialScriptsContent ?? '{}');

  // Undo history
  const historyRef = useRef<GraphModel[]>([]);
  const historyIndexRef = useRef<number>(-1);

  const canvasRef = useRef<CanvasHandle>(null);

  // Auto-save
  const autoSaveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const onSaveRef = useRef(onSave);
  useEffect(() => { onSaveRef.current = onSave; }, [onSave]);

  const buildPayload = useCallback((m: GraphModel, pm: PluginModel, sd: ScenarioData, sc: string): SavePayload => ({
    stateYaml: serializeModel(m, false),
    pluginYaml: serializePluginModel(pm, m),
    scenarioContent: serializeScenario(m.nodes, sd),
    scriptsContent: sc,
    stateLayoutContent: JSON.stringify(m.layout),
  }), []);

  const doSave = useCallback(async (m: GraphModel, pm: PluginModel, sd: ScenarioData, sc: string) => {
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
    } catch {
      setSaveStatus('error');
      message.error('保存失败，请重试');
    }
  }, [buildPayload]);

  const triggerAutoSave = useCallback((m: GraphModel, pm: PluginModel, sd: ScenarioData, sc: string) => {
    if (!onSaveRef.current) return;
    if (autoSaveTimerRef.current) clearTimeout(autoSaveTimerRef.current);
    setSaveStatus('pending');
    autoSaveTimerRef.current = setTimeout(() => void doSave(m, pm, sd, sc), AUTO_SAVE_DELAY_MS);
  }, [doSave]);

  const pluginModelRef = useRef(pluginModel);
  pluginModelRef.current = pluginModel;
  const scenarioDataRef = useRef(scenarioData);
  scenarioDataRef.current = scenarioData;
  const scriptsContentRef = useRef(scriptsContent);
  scriptsContentRef.current = scriptsContent;

  // Keyboard shortcuts
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 'z' && !e.shiftKey) {
        e.preventDefault();
        if (historyIndexRef.current < 0) return;
        const prev = historyRef.current[historyIndexRef.current];
        historyIndexRef.current -= 1;
        modelRef.current = prev;
        setModelState(prev);
        setErrors(validateStateGraph(prev));
        triggerAutoSave(prev, pluginModelRef.current, scenarioDataRef.current, scriptsContentRef.current);
      }
      if ((e.ctrlKey || e.metaKey) && e.key === 's') {
        e.preventDefault();
        void doSave(modelRef.current, pluginModelRef.current, scenarioDataRef.current, scriptsContentRef.current);
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [triggerAutoSave, doSave]);

  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const updateModel = useCallback((nextModel: GraphModel) => {
    const prev = modelRef.current;
    historyRef.current = historyRef.current.slice(0, historyIndexRef.current + 1);
    historyRef.current.push(prev);
    historyIndexRef.current = historyRef.current.length - 1;
    modelRef.current = nextModel;
    setModelState(nextModel);
    setErrors(validateStateGraph(nextModel));

    // Keep pluginModel.slots in sync with graphModel.slots.
    // ArtifactPanel writes new slots only into GraphModel; syncing back here
    // ensures handlePluginModelChange never overwrites them with stale data.
    if (nextModel.slots !== prev.slots) {
      const syncedSlots: import('./core/pluginModel').PluginSlotDef[] = Object.values(nextModel.slots).map((s) => ({
        id: s.id, type: s.type as import('./core/pluginModel').PluginSlotDef['type'],
        label: s.label, cardinality: s.cardinality, ordered: s.ordered,
        allow_manual_add: s.allow_manual_add, summary_max_chars: s.summary_max_chars,
      }));
      const syncedPm = { ...pluginModelRef.current, slots: syncedSlots };
      pluginModelRef.current = syncedPm;
      setPluginModel(syncedPm);
    }

    triggerAutoSave(nextModel, pluginModelRef.current, scenarioDataRef.current, scriptsContentRef.current);
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
          // parseYaml always returns slots:{} — slot definitions live in plugin.yaml.
          // Preserve the current slots so editing state.yml never wipes them.
          slots: modelRef.current.slots,
        };
        modelRef.current = mergedModel;
        setModelState(mergedModel);
        setErrors(validateStateGraph(mergedModel));
        triggerAutoSave(mergedModel, pluginModelRef.current, scenarioDataRef.current, scriptsContentRef.current);
      } else {
        setErrors([{ code: 'V10_YAML_SYNTAX', message: 'YAML 语法错误，请检查格式' }]);
      }
    }, 500);
  }, [triggerAutoSave]);

  const handlePluginModelChange = useCallback((pm: PluginModel) => {
    setPluginModel(pm);
    pluginModelRef.current = pm;
    // Sync slots into both modelRef and React model state so ArtifactPanel always
    // displays the latest slot definitions. Previously only the ref was updated,
    // leaving the React state stale and causing ArtifactPanel to show old slots.
    const slots: Record<string, import('./core/model').SlotDef> = {};
    for (const s of pm.slots) {
      slots[s.id] = {
        id: s.id, type: s.type, label: s.label,
        cardinality: s.cardinality, ordered: s.ordered,
        allow_manual_add: s.allow_manual_add, summary_max_chars: s.summary_max_chars,
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
    triggerAutoSave(modelRef.current, pluginModelRef.current, sd, scriptsContentRef.current);
  }, [triggerAutoSave]);

  // Code-editor change handlers for plugin.yaml and scenario.md
  // plugin.yaml: parse → validate → sync both pluginModel and graphModel.slots
  const handlePluginYamlCodeChange = useCallback((text: string) => {
    const pm = parsePluginYaml(text);
    if (pm) {
      // Sync slots into GraphModel so serializePluginModel always has the correct source
      const slots: Record<string, import('./core/model').SlotDef> = {};
      for (const s of pm.slots) {
        slots[s.id] = {
          id: s.id, type: s.type, label: s.label,
          cardinality: s.cardinality, ordered: s.ordered,
          allow_manual_add: s.allow_manual_add, summary_max_chars: s.summary_max_chars,
        };
      }
      const updatedModel: GraphModel = { ...modelRef.current, slots };
      modelRef.current = updatedModel;
      setModelState(updatedModel);

      setPluginModel(pm);
      pluginModelRef.current = pm;
      triggerAutoSave(updatedModel, pm, scenarioDataRef.current, scriptsContentRef.current);
      setErrors((prev) => prev.filter((e) => e.code !== 'V10_PLUGIN_YAML_SYNTAX'));
    } else {
      setErrors((prev) => {
        const alreadyHas = prev.some((e) => e.code === 'V10_PLUGIN_YAML_SYNTAX');
        if (alreadyHas) return prev;
        return [...prev, { code: 'V10_PLUGIN_YAML_SYNTAX', message: 'plugin.yaml 语法错误，请检查格式' }];
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
    triggerAutoSave(modelRef.current, pluginModelRef.current, scenarioDataRef.current, sc);
  }, [triggerAutoSave]);

  const handlePluginInfoSave = useCallback(async (pm: PluginModel, sd: ScenarioData) => {
    handlePluginModelChange(pm);
    handleScenarioChange(sd);
    await doSave(modelRef.current, pm, sd, scriptsContentRef.current);
  }, [handlePluginModelChange, handleScenarioChange, doSave]);

  const handleAddNode = useCallback(() => { canvasRef.current?.addNode(); }, []);
  const handleSelectNode = useCallback(() => {
    setViewMode('preview');
    setContentTab('statemachine');
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
    } else if (activeCodeFile === 'plugin.yaml') {
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
  const stateYamlForCode = serializeModel(model, false);
  // scenario.md text
  const scenarioMdForCode = serializeScenario(model.nodes, scenarioData);
  // plugin.yaml text
  const pluginYamlForCode = serializePluginModel(pluginModel, model);

  // All files available in the file tree (code mode)
  const coreFiles: CodeFile[] = ['state.yml', 'plugin.yaml', 'scenario.md'];
  const scriptFilePaths = Object.keys(scriptFiles);
  const devMode = isDeveloperModeActive();

  const getCodeFileContent = (file: CodeFile): string => {
    if (file === 'plugin.yaml') return pluginYamlForCode;
    if (file === 'state.yml') return stateYamlForCode;
    if (file === 'scenario.md') return scenarioMdForCode;
    if (file === 'layout.json') return JSON.stringify(model.layout, null, 2);
    return scriptFiles[file] ?? '';
  };


  return (
    <div ref={editorRootRef} className="state-graph-editor" aria-label="插件编辑器">
      {/* ── Row 1: back/breadcrumb left, save status + plugin config right ── */}
      <div className="sge-topbar">
        <div className="sge-topbar-left">
          {onClose && (
            <button className="sge-back-btn" onClick={onClose} aria-label="返回">
              ←
            </button>
          )}
          {pluginName && <span className="sge-plugin-name">{pluginName}</span>}
        </div>
        <div className="sge-topbar-right">
          {onSave && (
            <span className="sge-autosave-status">
              {saveStatus === 'pending' && <span className="sge-autosave-pending">待保存…</span>}
              {saveStatus === 'saving' && <span className="sge-autosave-saving"><LoadingOutlined /> 保存中…</span>}
              {saveStatus === 'saved' && <span className="sge-autosave-saved"><CheckCircleOutlined /> 已保存</span>}
              {saveStatus === 'error' && <span className="sge-autosave-error">保存失败</span>}
            </span>
          )}
          <Button size="small" icon={<SettingOutlined />} onClick={() => setPluginInfoOpen(true)}>
            插件配置
          </Button>
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
                className={`sge-seg-btn${contentTab === tab ? ' sge-seg-btn--active' : ''}${viewMode === 'code' ? ' sge-seg-btn--disabled' : ''}`}
                onClick={() => { if (viewMode !== 'code') setContentTab(tab); }}
                disabled={viewMode === 'code'}
                aria-disabled={viewMode === 'code'}
              >
                {tab === 'statemachine' ? '状态机' : tab === 'ui' ? 'UI' : '说明文档'}
              </button>
            ))}
          </div>
          <span className="sge-tab-divider" />
          {/* Capsule group 2: view mode */}
          <div className="sge-segmented">
            <button
              className={`sge-seg-btn${viewMode === 'preview' ? ' sge-seg-btn--active' : ''}`}
              onClick={handleExitCode}
            >
              预览
            </button>
            <button
              className={`sge-seg-btn${viewMode === 'code' ? ' sge-seg-btn--active' : ''}`}
              onClick={handleEnterCode}
            >
              代码
            </button>
          </div>
        </div>
        <div className="sge-toolbar2-right">
          {contentTab === 'statemachine' && viewMode === 'preview' && (
            <>
              <Button
                size="small"
                icon={<AppstoreOutlined />}
                onClick={() => setShowArtifacts((v) => !v)}
                type={showArtifacts ? 'primary' : 'default'}
              >
                素材{slotCount > 0 && <span className="sge-artifact-count">{slotCount}</span>}
              </Button>
              <Button size="small" icon={<PlusOutlined />} onClick={handleAddNode}>
                添加步骤
              </Button>
            </>
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
                errors={errors}
                onModelChange={updateModel}
                pluginModel={pluginModel}
                scenarioData={scenarioData}
                onScenarioChange={handleScenarioChange}
                canvasRef={canvasRef}
              />
              {model.nodes.length === 0 && showEmptyHint && (
                <div className="sge-empty-state" aria-hidden="true">
                  <div className="sge-empty-state-content">
                    <p className="sge-empty-state-title">用流程图描述你的工作</p>
                    <ol className="sge-empty-state-list">
                      <li>点击「添加步骤」创建一个步骤，每个步骤代表一个执行环节</li>
                      <li>点击「素材」定义步骤间传递的内容，如文字、图片、文件等</li>
                      <li>拖拽步骤上的连接点来连接各步骤，表示执行顺序</li>
                    </ol>
                    <p className="sge-empty-state-hint">也可以双击画布空白处快速添加步骤 · 添加第一个步骤后提示消失</p>
                  </div>
                </div>
              )}
              {showArtifacts && (
                <ArtifactPanel
                  model={model}
                  onClose={() => setShowArtifacts(false)}
                  onModelChange={updateModelFromUpdater}
                />
              )}
            </div>
            <ValidationPanel errors={errors} onSelectNode={handleSelectNode} />
          </div>
        )}

        {viewMode === 'preview' && contentTab === 'ui' && (
          <div className="sge-ui-editor-panel">
            <UiEditorPanel
              graphModel={model}
              pluginModel={pluginModel}
              onGraphModelChange={updateModelFromUpdater}
              onPluginModelChange={handlePluginModelChange}
              activeTabId={uiActiveTabId}
              onActiveTabChange={setUiActiveTabId}
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
                <span className="sge-code-sidebar-label">核心文件</span>
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
                  <span className="sge-code-sidebar-label">脚本文件</span>
                  {scriptFilePaths.map((path) => (
                    <div
                      key={path}
                      className={`sge-code-file-item${activeCodeFile === path ? ' sge-code-file-item--active' : ''}`}
                      onClick={() => setActiveCodeFile(path)}
                    >
                      <FileOutlined className="sge-code-file-icon" />
                      <Tooltip title={path}>
                        <span className="sge-code-file-name">{path.replace('scripts/', '')}</span>
                      </Tooltip>
                    </div>
                  ))}
                </div>
              )}
              {devMode && (
                <div className="sge-code-sidebar-section">
                  <span className="sge-code-sidebar-label sge-code-sidebar-label--dev">调试</span>
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
                key={activeCodeFile === 'layout.json' ? `layout.json-${JSON.stringify(model.layout)}` : activeCodeFile}
                value={getCodeFileContent(activeCodeFile)}
                onChange={(text) => {
                  if (activeCodeFile === 'layout.json') return; // read-only debug view
                  if (activeCodeFile === 'state.yml') {
                    handleYamlChange(text);
                  } else if (activeCodeFile === 'plugin.yaml') {
                    handlePluginYamlCodeChange(text);
                  } else if (activeCodeFile === 'scenario.md') {
                    handleScenarioMdCodeChange(text);
                  } else {
                    handleScriptsChange(activeCodeFile, text);
                  }
                }}
                errors={activeCodeFile === 'state.yml' ? errors : []}
                readOnly={activeCodeFile === 'layout.json'}
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
      </div>

      <PluginInfoModal
        open={pluginInfoOpen}
        onCancel={() => setPluginInfoOpen(false)}
        pluginModel={pluginModel}
        scenarioData={scenarioData}
        onSave={handlePluginInfoSave}
      />
    </div>
  );
}
