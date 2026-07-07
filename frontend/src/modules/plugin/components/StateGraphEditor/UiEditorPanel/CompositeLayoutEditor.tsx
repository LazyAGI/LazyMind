import { Button, Tooltip } from 'antd';
import {
  ColumnWidthOutlined,
  LayoutOutlined,
  ReloadOutlined,
  VerticalAlignTopOutlined,
  VerticalAlignBottomOutlined,
  MenuFoldOutlined,
  MenuUnfoldOutlined,
} from '@ant-design/icons';
import type { PluginUiTab, CompositePanelNode } from '../core/pluginModel';
import type { SlotDef } from '../core/model';
import CompositeCanvas from './CompositeCanvas';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface Props {
  tab: PluginUiTab;
  slotMap: Record<string, SlotDef>;
  onChange: (layout: CompositePanelNode) => void;
  onTabPositionChange: (pos: PluginUiTab['composite_tab_position']) => void;
}

// ---------------------------------------------------------------------------
// Layout templates
// ---------------------------------------------------------------------------

const TEMPLATES: Array<{ label: string; icon: React.ReactNode; node: CompositePanelNode }> = [
  {
    label: '单列',
    icon: '▭',
    node: { direction: 'row', children: [{ slot: '', weight: 1 }] },
  },
  {
    label: '双列',
    icon: '▭▭',
    node: { direction: 'row', children: [{ slot: '', weight: 1 }, { slot: '', weight: 1 }] },
  },
  {
    label: '上下',
    icon: '▬/▬',
    node: { direction: 'column', children: [{ slot: '', weight: 1 }, { slot: '', weight: 1 }] },
  },
  {
    label: 'T 形',
    icon: '▬/▭▭',
    node: {
      direction: 'column',
      children: [
        { slot: '', weight: 2 },
        { direction: 'row', weight: 1, children: [{ slot: '', weight: 1 }, { slot: '', weight: 1 }] },
      ],
    },
  },
  {
    label: '倒 T 形',
    icon: '▭▭/▬',
    node: {
      direction: 'column',
      children: [
        { direction: 'row', weight: 1, children: [{ slot: '', weight: 1 }, { slot: '', weight: 1 }] },
        { slot: '', weight: 2 },
      ],
    },
  },
  {
    label: 'L 形',
    icon: '▭/▭▭',
    node: {
      direction: 'row',
      children: [
        { direction: 'column', weight: 1, children: [{ slot: '', weight: 1 }, { slot: '', weight: 1 }] },
        { slot: '', weight: 1 },
      ],
    },
  },
];

// ---------------------------------------------------------------------------
// Step 1: Page-bar position selector (required, no toggle)
// ---------------------------------------------------------------------------

function PageBarPositionStep({
  position,
  onPositionChange,
}: {
  position: PluginUiTab['composite_tab_position'];
  onPositionChange: (pos: PluginUiTab['composite_tab_position']) => void;
}) {
  const positions: Array<{ value: PluginUiTab['composite_tab_position']; icon: React.ReactNode; label: string }> = [
    { value: 'top', icon: <VerticalAlignTopOutlined />, label: '顶部' },
    { value: 'bottom', icon: <VerticalAlignBottomOutlined />, label: '底部' },
    { value: 'left', icon: <MenuFoldOutlined />, label: '左侧' },
    { value: 'right', icon: <MenuUnfoldOutlined />, label: '右侧' },
  ];

  return (
    <div className='cle-step cle-step-1'>
      <div className='cle-step-title'>
        <span className='cle-step-badge'>1</span>
        <span>Page 条位置</span>
      </div>
      <div className='cle-tab-positions'>
        {positions.map((p) => (
          <Tooltip key={p.value} title={p.label}>
            <button
              type='button'
              className={`cle-tab-pos-btn${position === p.value ? ' cle-tab-pos-btn--active' : ''}`}
              onClick={() => onPositionChange(p.value)}
              aria-pressed={position === p.value}
            >
              {p.icon}
              <span>{p.label}</span>
            </button>
          </Tooltip>
        ))}
      </div>
      <p className='cle-step-hint'>
        Page 数量由运行时 slot 数据条数决定，此处只配置 page 条位置。
      </p>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Step 2: Template picker
// ---------------------------------------------------------------------------

function TemplatePicker({ onSelect }: { onSelect: (node: CompositePanelNode) => void }) {
  return (
    <div className='cle-step cle-step-2'>
      <div className='cle-step-title'>
        <span className='cle-step-badge'>2</span>
        <span>选择布局模板</span>
      </div>
      <div className='cle-templates-grid'>
        {TEMPLATES.map((tpl) => (
          <button
            key={tpl.label}
            type='button'
            className='cle-template-btn'
            onClick={() => onSelect(tpl.node)}
          >
            <span className='cle-template-icon'>{tpl.icon}</span>
            <span className='cle-template-label'>{tpl.label}</span>
          </button>
        ))}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

export default function CompositeLayoutEditor({
  tab,
  slotMap,
  onChange,
  onTabPositionChange,
}: Props) {
  // Treat layout as "set" only when there are actual children to display
  const hasLayout = !!(
    tab.composite_layout?.direction &&
    tab.composite_layout.children &&
    tab.composite_layout.children.length > 0
  );

  const handleTabPositionChange = (pos: PluginUiTab['composite_tab_position']) => {
    onTabPositionChange(pos);
  };

  const handleSelectTemplate = (node: CompositePanelNode) => {
    onChange(node);
  };

  const handleReset = () => {
    onChange({ direction: 'row', children: [] });
  };

  return (
    <div className='cle-root'>
      {/* Step 1: Page-bar position (required) */}
      <PageBarPositionStep
        position={tab.composite_tab_position ?? 'top'}
        onPositionChange={handleTabPositionChange}
      />

      {/* Step 2: Template picker (shown until a template is selected) */}
      {!hasLayout && (
        <TemplatePicker onSelect={handleSelectTemplate} />
      )}

      {/* Steps 3–5: Canvas (visible once template is selected) */}
      {hasLayout && tab.composite_layout && (
        <div className='cle-step cle-step-canvas'>
          <div className='cle-step-title'>
            <span className='cle-step-badge'>2</span>
            <span>可视化布局画布</span>
            <div className='cle-canvas-actions'>
              <Tooltip title='切换布局模板（重置）'>
                <Button
                  size='small'
                  icon={<ReloadOutlined />}
                  onClick={handleReset}
                  danger
                >
                  重置布局
                </Button>
              </Tooltip>
            </div>
          </div>
          <div className='cle-canvas-wrap'>
            <CompositeCanvas
              node={tab.composite_layout}
              slotMap={slotMap}
              onChange={onChange}
            />
          </div>
          <p className='cle-step-hint'>
            <ColumnWidthOutlined /> 拖拽分割线调整各分块比例；
            <LayoutOutlined /> 点击 Tab 按钮可将分块变为 Tab 切换区域；
            将左侧素材拖入各分块完成绑定。
          </p>
        </div>
      )}
    </div>
  );
}
