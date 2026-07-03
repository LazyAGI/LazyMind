import { useCallback, useRef, useState } from 'react';
import { Button, Segmented, message } from 'antd';
import { SaveOutlined, PlusOutlined } from '@ant-design/icons';
import { v4 as uuidv4 } from 'uuid';
import type { GraphModel } from './core/model';
import { createEmptyModel, VIRTUAL_END } from './core/model';
import { parseYaml } from './core/parser';
import { serializeModel } from './core/serializer';
import { validateStateGraph } from './core/validator';
import type { ValidationError } from './core/validator';
import GraphCanvas from './GraphCanvas';
import YamlEditor from './YamlEditor';
import ValidationPanel from './ValidationPanel';
import './index.scss';

type ViewMode = 'canvas' | 'yaml';

const EMPTY_YAML = `slots: {}

steps: []
`;

interface Props {
  /** Initial YAML content. If omitted, starts with an empty model. */
  initialYaml?: string;
  /** Called when user clicks "Save Draft" with valid (or user-confirmed) YAML */
  onSave?: (yaml: string) => Promise<void>;
  /** Called when user clicks "Close" */
  onClose?: () => void;
}

export default function StateGraphEditor({ initialYaml, onSave, onClose }: Props) {
  const [view, setView] = useState<ViewMode>('canvas');
  const [saving, setSaving] = useState(false);

  // GraphModel is the single source of truth in memory
  const modelRef = useRef<GraphModel>(
    initialYaml ? (parseYaml(initialYaml) ?? createEmptyModel()) : createEmptyModel(),
  );
  const [model, setModelState] = useState<GraphModel>(modelRef.current);

  // YAML displayed in the editor (stripped of x-layout)
  const [yamlText, setYamlText] = useState<string>(
    initialYaml ?? EMPTY_YAML,
  );

  const [errors, setErrors] = useState<ValidationError[]>(() =>
    validateStateGraph(modelRef.current),
  );

  // Debounce timer ref for YAML editing
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const updateModel = useCallback((nextModel: GraphModel) => {
    modelRef.current = nextModel;
    setModelState(nextModel);
    setErrors(validateStateGraph(nextModel));
    // Sync YAML text (without layout)
    setYamlText(serializeModel(nextModel, false));
  }, []);

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
    [],
  );

  // Add a new step node from toolbar
  const handleAddNode = useCallback(() => {
    const newId = `step_${uuidv4().slice(0, 6)}`;
    const nextModel: GraphModel = {
      ...modelRef.current,
      nodes: [
        ...modelRef.current.nodes,
        {
          id: newId,
          label: '新步骤',
          mode: 'human',
          inputs: [],
          outputs: [],
          transitions: [{ to: VIRTUAL_END, condition: 'always' }],
        },
      ],
      layout: {
        ...modelRef.current.layout,
        [newId]: {
          x: 200 + modelRef.current.nodes.length * 220,
          y: 200,
        },
      },
    };
    updateModel(nextModel);
  }, [updateModel]);

  const handleSave = useCallback(async () => {
    if (!onSave) return;
    const finalYaml = serializeModel(modelRef.current, true);
    setSaving(true);
    try {
      await onSave(finalYaml);
      message.success('草稿已保存');
    } catch (err) {
      message.error('保存失败，请重试');
    } finally {
      setSaving(false);
    }
  }, [onSave]);

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
            <Button
              size="small"
              icon={<PlusOutlined />}
              onClick={handleAddNode}
            >
              新增节点
            </Button>
          )}
        </div>
        <div className="sge-toolbar-right">
          {errors.length > 0 && (
            <span className="sge-toolbar-error-badge">{errors.length} 个错误</span>
          )}
          {onSave && (
            <Button
              type="primary"
              size="small"
              icon={<SaveOutlined />}
              loading={saving}
              onClick={() => void handleSave()}
            >
              保存草稿
            </Button>
          )}
          {onClose && (
            <Button size="small" onClick={onClose}>
              关闭
            </Button>
          )}
        </div>
      </div>

      {/* Main content */}
      <div className="sge-content">
        {view === 'canvas' ? (
          <GraphCanvas
            model={model}
            errors={errors}
            onModelChange={updateModel}
          />
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
