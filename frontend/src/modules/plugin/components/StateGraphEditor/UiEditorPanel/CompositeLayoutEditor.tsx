import { Button, Tooltip } from 'antd';
import {
  ColumnWidthOutlined,
  LayoutOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import type { PluginUiTab, WidgetConfig, CompositePanelNode } from '../core/pluginModel';
import type { SlotDef } from '../core/model';
import CompositeCanvas from './CompositeCanvas';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface Props {
  tab: PluginUiTab;
  slotMap: Record<string, SlotDef>;
  uiSlots: Record<string, WidgetConfig>;
  onChange: (layout: CompositePanelNode) => void;
  onPageBarPositionChange: (pos: PluginUiTab['composite_tab_position']) => void;
}

// ---------------------------------------------------------------------------
// SVG layout preview thumbnails
// ---------------------------------------------------------------------------

const W = 48;
const H = 36;
const PAD = 3;
const R = 2;
const FILL = '#d9e3f0';
const STROKE = '#a0b4cc';

function Rect({ x, y, w, h }: { x: number; y: number; w: number; h: number }) {
  return <rect x={x} y={y} width={w} height={h} rx={R} fill={FILL} stroke={STROKE} strokeWidth={0.8} />;
}

const LayoutIcons: Record<string, React.ReactNode> = {
  single: (
    <svg width={W} height={H} viewBox={`0 0 ${W} ${H}`}>
      <Rect x={PAD} y={PAD} w={W - PAD * 2} h={H - PAD * 2} />
    </svg>
  ),
  double: (
    <svg width={W} height={H} viewBox={`0 0 ${W} ${H}`}>
      <Rect x={PAD} y={PAD} w={(W - PAD * 2 - 3) / 2} h={H - PAD * 2} />
      <Rect x={PAD + (W - PAD * 2 - 3) / 2 + 3} y={PAD} w={(W - PAD * 2 - 3) / 2} h={H - PAD * 2} />
    </svg>
  ),
  topbottom: (
    <svg width={W} height={H} viewBox={`0 0 ${W} ${H}`}>
      <Rect x={PAD} y={PAD} w={W - PAD * 2} h={(H - PAD * 2 - 3) / 2} />
      <Rect x={PAD} y={PAD + (H - PAD * 2 - 3) / 2 + 3} w={W - PAD * 2} h={(H - PAD * 2 - 3) / 2} />
    </svg>
  ),
  tshape: (
    <svg width={W} height={H} viewBox={`0 0 ${W} ${H}`}>
      <Rect x={PAD} y={PAD} w={W - PAD * 2} h={(H - PAD * 2) * 0.55} />
      <Rect x={PAD} y={PAD + (H - PAD * 2) * 0.55 + 3} w={(W - PAD * 2 - 3) / 2} h={(H - PAD * 2) * 0.45 - 3} />
      <Rect x={PAD + (W - PAD * 2 - 3) / 2 + 3} y={PAD + (H - PAD * 2) * 0.55 + 3} w={(W - PAD * 2 - 3) / 2} h={(H - PAD * 2) * 0.45 - 3} />
    </svg>
  ),
  invtshape: (
    <svg width={W} height={H} viewBox={`0 0 ${W} ${H}`}>
      <Rect x={PAD} y={PAD} w={(W - PAD * 2 - 3) / 2} h={(H - PAD * 2) * 0.45} />
      <Rect x={PAD + (W - PAD * 2 - 3) / 2 + 3} y={PAD} w={(W - PAD * 2 - 3) / 2} h={(H - PAD * 2) * 0.45} />
      <Rect x={PAD} y={PAD + (H - PAD * 2) * 0.45 + 3} w={W - PAD * 2} h={(H - PAD * 2) * 0.55 - 3} />
    </svg>
  ),
  lshape: (
    <svg width={W} height={H} viewBox={`0 0 ${W} ${H}`}>
      <Rect x={PAD} y={PAD} w={(W - PAD * 2) * 0.45} h={(H - PAD * 2 - 3) / 2} />
      <Rect x={PAD} y={PAD + (H - PAD * 2 - 3) / 2 + 3} w={(W - PAD * 2) * 0.45} h={(H - PAD * 2 - 3) / 2} />
      <Rect x={PAD + (W - PAD * 2) * 0.45 + 3} y={PAD} w={(W - PAD * 2) * 0.55 - 3} h={H - PAD * 2} />
    </svg>
  ),
};

// ---------------------------------------------------------------------------
// Layout templates
// ---------------------------------------------------------------------------

const TEMPLATES: Array<{ label: string; icon: React.ReactNode; node: CompositePanelNode }> = [
  {
    label: '单列',
    icon: LayoutIcons.single,
    node: { direction: 'row', children: [{ slot: '', weight: 1 }] },
  },
  {
    label: '多列',
    icon: LayoutIcons.double,
    node: { direction: 'row', children: [{ slot: '', weight: 1 }, { slot: '', weight: 1 }] },
  },
  {
    label: '多行',
    icon: LayoutIcons.topbottom,
    node: { direction: 'column', children: [{ slot: '', weight: 1 }, { slot: '', weight: 1 }] },
  },
  {
    label: 'T 形',
    icon: LayoutIcons.tshape,
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
    icon: LayoutIcons.invtshape,
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
    icon: LayoutIcons.lshape,
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
// Step 1: Template picker
// ---------------------------------------------------------------------------

function TemplatePicker({ onSelect }: { onSelect: (node: CompositePanelNode) => void }) {
  return (
    <div className='cle-step cle-step-templates'>
      <div className='cle-step-title'>
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
  uiSlots,
  onChange,
  onPageBarPositionChange,
}: Props) {
  const hasLayout = !!(
    tab.composite_layout?.direction &&
    tab.composite_layout.children &&
    tab.composite_layout.children.length > 0
  );

  const handleSelectTemplate = (node: CompositePanelNode) => {
    onChange(node);
  };

  const handleReset = () => {
    onChange({ direction: 'row', children: [] });
  };

  return (
    <div className='cle-root'>
      {/* Step 1: Template picker (shown until a template is selected) */}
      {!hasLayout && (
        <TemplatePicker onSelect={handleSelectTemplate} />
      )}

      {/* Canvas (visible once template is selected) */}
      {hasLayout && tab.composite_layout && (
        <div className='cle-step cle-step-canvas'>
          <div className='cle-composite-desc'>
            <div className='cle-composite-desc-header'>
              <span className='cle-composite-desc-title'>多素材联合展示</span>
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
            <p className='cle-composite-desc-text'>
              将多个「列表」素材放入同一个 Composite 后，页码栏会统一翻页——
              选中第 N 页时，每个素材都展示其第 N 条数据，方便对比查看。
              加入的素材要么全部是「列表」类型，要么全部不是。
            </p>
          </div>
          <div className='cle-canvas-wrap'>
            <CompositeCanvas
              node={tab.composite_layout}
              slotMap={slotMap}
              uiSlots={uiSlots}
              pageBarPosition={tab.composite_tab_position ?? 'bottom'}
              onPageBarPositionChange={onPageBarPositionChange}
              onChange={onChange}
            />
          </div>
          <p className='cle-step-hint'>
            <ColumnWidthOutlined /> 拖拽分割线调整各分块比例；
            <LayoutOutlined /> 点击「+ Tab」按钮可将分块变为 Tab 切换区域；
            将左侧素材拖入各分块完成绑定。
          </p>
        </div>
      )}
    </div>
  );
}
