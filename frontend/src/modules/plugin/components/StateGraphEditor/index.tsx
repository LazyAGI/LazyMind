import { useCallback, useEffect, useRef, useState } from 'react';
import { Button, Segmented, message } from 'antd';
import { CheckCircleOutlined, LoadingOutlined, PlusOutlined, AppstoreOutlined } from '@ant-design/icons';
import type { GraphModel } from './core/model';
import { createEmptyModel } from './core/model';
import { parseYaml } from './core/parser';
import { serializeModel } from './core/serializer';
import { validateStateGraph } from './core/validator';
import type { ValidationError } from './core/validator';
import GraphCanvas from './GraphCanvas';
import type { CanvasHandle } from './GraphCanvas';
import ArtifactPanel from './ArtifactPanel';
import YamlEditor from './YamlEditor';
import ValidationPanel from './ValidationPanel';
import './index.scss';

type ViewMode = 'canvas' | 'yaml';
type SaveStatus = 'idle' | 'pending' | 'saving' | 'saved' | 'error';

const AUTO_SAVE_DELAY_MS = 1500;

interface Props {
  /** Initial YAML content. If omitted, starts with an empty model. */
  initialYaml?: string;
  /** Called automatically when model changes (auto-save). Also called on manual save. */
  onSave?: (yaml: string) => Promise<void>;
  /** Called when user clicks "Close" */
  onClose?: () => void;
}

export default function StateGraphEditor({ initialYaml, onSave, onClose }: Props) {
  const [view, setView] = useState<ViewMode>('canvas');
  const [saveStatus, setSaveStatus] = useState<SaveStatus>('idle');
  const [showArtifacts, setShowArtifacts] = useState(false);

  // GraphModel is the single source of truth in memory
  const modelRef = useRef<GraphModel>(
    initialYaml ? (parseYaml(initialYaml) ?? createEmptyModel()) : createEmptyModel(),
  );
  const [model, setModelState] = useState<GraphModel>(modelRef.current);

  // Undo history
  const historyRef = useRef<GraphModel[]>([]);
  const historyIndexRef = useRef<number>(-1);

  // Ref to canvas for addNode
  const canvasRef = useRef<CanvasHandle>(null);

  // YAML displayed in the editor — always strip x-layout so coordinates never appear in the editor
  const [yamlText, setYamlText] = useState<string>(
    () => serializeModel(modelRef.current, false),
  );

  const [errors, setErrors] = useState<ValidationError[]>(() =>
    validateStateGraph(modelRef.current),
  );

  // Auto-save: debounced timer fires after model changes
  const autoSaveTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const onSaveRef = useRef(onSave);
  useEffect(() => { onSaveRef.current = onSave; }, [onSave]);

  const doSave = useCallback(async (m: GraphModel) => {
    const fn = onSaveRef.current;
    if (!fn) return;
    if (autoSaveTimerRef.current) {
      clearTimeout(autoSaveTimerRef.current);
      autoSaveTimerRef.current = null;
    }
    setSaveStatus('saving');
    try {
      await fn(serializeModel(m, true));
      setSaveStatus('saved');
    } catch {
      setSaveStatus('error');
      message.error('保存失败，请重试');
    }
  }, []);

  const triggerAutoSave = useCallback((m: GraphModel) => {
    if (!onSaveRef.current) return;
    if (autoSaveTimerRef.current) clearTimeout(autoSaveTimerRef.current);
    setSaveStatus('pending');
    autoSaveTimerRef.current = setTimeout(() => void doSave(m), AUTO_SAVE_DELAY_MS);
  }, [doSave]);

  // Undo on Ctrl+Z / Cmd+Z
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
        setYamlText(serializeModel(prev, false));
        triggerAutoSave(prev);
      }
      if ((e.ctrlKey || e.metaKey) && e.key === 's') {
        e.preventDefault();
        void doSave(modelRef.current);
      }
    };
    window.addEventListener('keydown', handleKeyDown);
    return () => window.removeEventListener('keydown', handleKeyDown);
  }, [triggerAutoSave, doSave]);

  // Debounce timer ref for YAML editing
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const updateModel = useCallback((nextModel: GraphModel) => {
    // Push current model to undo history before updating
    const prev = modelRef.current;
    historyRef.current = historyRef.current.slice(0, historyIndexRef.current + 1);
    historyRef.current.push(prev);
    historyIndexRef.current = historyRef.current.length - 1;

    modelRef.current = nextModel;
    setModelState(nextModel);
    setErrors(validateStateGraph(nextModel));
    setYamlText(serializeModel(nextModel, false));
    triggerAutoSave(nextModel);
  }, [triggerAutoSave]);

  // Handle YAML text change from Monaco
  const handleYamlChange = useCallback(
    (text: string) => {
      setYamlText(text);
      if (debounceRef.current) clearTimeout(debounceRef.current);
      debounceRef.current = setTimeout(() => {
        const parsed = parseYaml(text);
        if (parsed) {
          // Preserve current layout when user edits YAML
          const mergedModel: GraphModel = {
            ...parsed,
            layout: { ...parsed.layout, ...modelRef.current.layout },
          };
          modelRef.current = mergedModel;
          setModelState(mergedModel);
          setErrors(validateStateGraph(mergedModel));
          triggerAutoSave(mergedModel);
        }
        // If parse fails, keep the last valid model but mark a syntax error
        else {
          setErrors([
            {
              code: 'V10_YAML_SYNTAX',
              message: 'YAML 语法错误，请检查格式',
            },
          ]);
        }
      }, 500);
    },
    [triggerAutoSave],
  );

  // Add a new step node from toolbar — delegates to canvas for viewport-aware placement
  const handleAddNode = useCallback(() => {
    canvasRef.current?.addNode();
  }, []);

  const handleSelectNode = useCallback((_nodeId: string) => {
    setView('canvas');
  }, []);

  return (
    <div className="state-graph-editor" aria-label="状态机编辑器">
      {/* Toolbar */}
      <div className="sge-toolbar">
        <div className="sge-toolbar-left">
          <Segmented
            value={view}
            options={[
              { label: '画布', value: 'canvas' },
              { label: 'YAML', value: 'yaml' },
            ]}
            onChange={(v) => setView(v as ViewMode)}
          />
          {view === 'canvas' && (
            <>
              <Button
                size="small"
                icon={<AppstoreOutlined />}
                onClick={() => setShowArtifacts((v) => !v)}
                type={showArtifacts ? 'primary' : 'default'}
              >
                素材
                {Object.keys(model.slots).length > 0 && (
                  <span className="sge-artifact-count">{Object.keys(model.slots).length}</span>
                )}
              </Button>
              <Button
                size="small"
                icon={<PlusOutlined />}
                onClick={handleAddNode}
              >
                添加步骤
              </Button>
            </>
          )}
        </div>
        <div className="sge-toolbar-right">
          {errors.length > 0 && (
            <span className="sge-toolbar-error-badge">{errors.length} 个错误</span>
          )}
          {onSave && (
            <span className="sge-autosave-status">
              {saveStatus === 'pending' && <span className="sge-autosave-pending">待保存…</span>}
              {saveStatus === 'saving' && <span className="sge-autosave-saving"><LoadingOutlined /> 保存中…</span>}
              {saveStatus === 'saved' && <span className="sge-autosave-saved"><CheckCircleOutlined /> 已保存</span>}
              {saveStatus === 'error' && <span className="sge-autosave-error">保存失败</span>}
            </span>
          )}
          {onClose && (
            <Button size="small" onClick={async () => {
              await doSave(modelRef.current);
              onClose();
            }}>
              关闭
            </Button>
          )}
        </div>
      </div>

      {/* Main content */}
      <div className="sge-content">
        {view === 'canvas' ? (
          <>
            <GraphCanvas
              model={model}
              errors={errors}
              onModelChange={updateModel}
              canvasRef={canvasRef}
            />
            {model.nodes.length === 0 && (
              <div className="sge-empty-state" aria-hidden="true">
                <div className="sge-empty-state-content">
                  <p className="sge-empty-state-title">用流程图描述你的工作</p>
                  <ol className="sge-empty-state-list">
                    <li>点击「添加步骤」创建一个步骤，每个步骤代表一个执行环节</li>
                    <li>点击「素材」定义步骤间传递的内容，如文字、图片、文件等</li>
                    <li>拖拽步骤上的连接点来连接各步骤，表示执行顺序</li>
                  </ol>
                  <p className="sge-empty-state-hint">也可以双击画布空白处快速添加步骤</p>
                </div>
              </div>
            )}
            {showArtifacts && (
              <ArtifactPanel
                model={model}
                onClose={() => setShowArtifacts(false)}
                onModelChange={updateModel}
              />
            )}
          </>
        ) : (
          <YamlEditor
            value={yamlText}
            onChange={handleYamlChange}
            errors={errors}
          />
        )}
      </div>

      {/* Bottom validation panel */}
      <ValidationPanel errors={errors} onSelectNode={handleSelectNode} />
    </div>
  );
}
