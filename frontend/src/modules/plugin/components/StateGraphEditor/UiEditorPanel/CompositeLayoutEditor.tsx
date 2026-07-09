import { Button, Tooltip } from 'antd';
import {
  ColumnWidthOutlined,
  LayoutOutlined,
  ReloadOutlined,
} from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
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

const TEMPLATE_NODES: CompositePanelNode[] = [
  { direction: 'row', children: [{ slot: '', weight: 1 }] },
  { direction: 'row', children: [{ slot: '', weight: 1 }, { slot: '', weight: 1 }] },
  { direction: 'column', children: [{ slot: '', weight: 1 }, { slot: '', weight: 1 }] },
  {
    direction: 'column',
    children: [
      { slot: '', weight: 2 },
      { direction: 'row', weight: 1, children: [{ slot: '', weight: 1 }, { slot: '', weight: 1 }] },
    ],
  },
  {
    direction: 'column',
    children: [
      { direction: 'row', weight: 1, children: [{ slot: '', weight: 1 }, { slot: '', weight: 1 }] },
      { slot: '', weight: 2 },
    ],
  },
  {
    direction: 'row',
    children: [
      { direction: 'column', weight: 1, children: [{ slot: '', weight: 1 }, { slot: '', weight: 1 }] },
      { slot: '', weight: 1 },
    ],
  },
];

// ---------------------------------------------------------------------------
// Step 1: Template picker
// ---------------------------------------------------------------------------

function TemplatePicker({ templates, onSelect }: { templates: Array<{ label: string; icon: React.ReactNode; node: CompositePanelNode }>; onSelect: (node: CompositePanelNode) => void }) {
  const { t } = useTranslation();
  return (
    <div className='cle-step cle-step-templates'>
      <div className='cle-step-title'>
        <span>{t('selfEvolutionRun.cleSelectTemplate')}</span>
      </div>
      <div className='cle-templates-grid'>
        {templates.map((tpl) => (
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
  const { t } = useTranslation();

  const TEMPLATES: Array<{ label: string; icon: React.ReactNode; node: CompositePanelNode }> = [
    { label: t('selfEvolutionRun.cleTemplateLabel1'), icon: LayoutIcons.single, node: TEMPLATE_NODES[0] },
    { label: t('selfEvolutionRun.cleTemplateLabel2'), icon: LayoutIcons.double, node: TEMPLATE_NODES[1] },
    { label: t('selfEvolutionRun.cleTemplateLabel3'), icon: LayoutIcons.topbottom, node: TEMPLATE_NODES[2] },
    { label: t('selfEvolutionRun.cleTemplateLabel4'), icon: LayoutIcons.tshape, node: TEMPLATE_NODES[3] },
    { label: t('selfEvolutionRun.cleTemplateLabel5'), icon: LayoutIcons.invtshape, node: TEMPLATE_NODES[4] },
    { label: t('selfEvolutionRun.cleTemplateLabel6'), icon: LayoutIcons.lshape, node: TEMPLATE_NODES[5] },
  ];
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
        <TemplatePicker templates={TEMPLATES} onSelect={handleSelectTemplate} />
      )}

      {/* Canvas (visible once template is selected) */}
      {hasLayout && tab.composite_layout && (
        <div className='cle-step cle-step-canvas'>
          <div className='cle-composite-desc'>
            <div className='cle-composite-desc-header'>
              <span className='cle-composite-desc-title'>{t('selfEvolutionRun.cleCompositeDescTitle')}</span>
              <Tooltip title={t('selfEvolutionRun.cleResetTooltip')}>
                <Button
                  size='small'
                  icon={<ReloadOutlined />}
                  onClick={handleReset}
                  danger
                >
                  {t('selfEvolutionRun.cleResetLayout')}
                </Button>
              </Tooltip>
            </div>
            <p className='cle-composite-desc-text'>
              {t('selfEvolutionRun.cleCompositeDescText')}
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
            <ColumnWidthOutlined /> {t('selfEvolutionRun.cleHintResize')}&nbsp;
            <LayoutOutlined /> {t('selfEvolutionRun.cleHintTab')}
          </p>
        </div>
      )}
    </div>
  );
}
