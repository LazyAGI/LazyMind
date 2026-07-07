import { useSortable } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { Button, Tooltip } from 'antd';
import { CloseOutlined, HolderOutlined, FileTextOutlined } from '@ant-design/icons';
import type { SlotDef } from '../core/model';
import type { WidgetConfig, WidgetType } from '../core/pluginModel';
import { SLOT_DEFAULT_WIDGET } from '../core/pluginModel';
import { SLOT_TYPE_ICONS } from './slotTypeIcon';
import WidgetPlaceholder from './WidgetPlaceholder';
import WidgetConfigPanel from './WidgetConfigPanel';
import WidgetSelector from './WidgetSelector';

interface Props {
  slotId: string;
  slotDef?: SlotDef;
  widget?: WidgetConfig;
  onRemove: (slotId: string) => void;
  onWidgetChange?: (slotId: string, widget: WidgetConfig) => void;
}

export default function UiWidgetCard({ slotId, slotDef, widget, onRemove, onWidgetChange }: Props) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } =
    useSortable({ id: slotId });

  const style: React.CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.4 : 1,
  };

  const type = slotDef?.type ?? 'text';
  const cardinality = slotDef?.cardinality;
  const icon = SLOT_TYPE_ICONS[type] ?? <FileTextOutlined />;
  const label = slotDef?.label ?? slotId;

  const slotKey = `${type}/${cardinality ?? 'single'}`;
  const defaultWidgetType = (SLOT_DEFAULT_WIDGET[slotKey] ?? 'text-single') as WidgetType;
  const activeWidget: WidgetConfig = widget ?? ({ widgetType: defaultWidgetType } as WidgetConfig);

  const handleWidgetTypeChange = (newType: WidgetType) => {
    const newWidget: WidgetConfig = { widgetType: newType } as WidgetConfig;
    onWidgetChange?.(slotId, newWidget);
  };

  const handleConfigChange = (next: WidgetConfig) => {
    onWidgetChange?.(slotId, next);
  };

  return (
    <div
      ref={setNodeRef}
      style={style}
      className="uep-widget-card"
      {...attributes}
    >
      {/* Zone 1: Header */}
      <div className="uep-widget-card-header">
        <span className="uep-widget-drag" {...listeners} aria-label="拖拽排序">
          <HolderOutlined />
        </span>
        <span className="uep-widget-icon">{icon}</span>
        <span className="uep-widget-label">{label}</span>
        <WidgetSelector
          slotType={type}
          cardinality={cardinality}
          value={activeWidget.widgetType}
          onChange={handleWidgetTypeChange}
          size="small"
        />
        <Tooltip title="从当前 Tab 移除">
          <Button
            type="text"
            size="small"
            icon={<CloseOutlined />}
            className="uep-widget-remove"
            onClick={() => onRemove(slotId)}
            aria-label={`移除 ${label}`}
          />
        </Tooltip>
      </div>

      {/* Zone 2: Placeholder preview */}
      <div className="uep-widget-preview">
        <WidgetPlaceholder widgetConfig={activeWidget} label={label} />
      </div>

      {/* Zone 3: Inline config panel */}
      {onWidgetChange && (
        <div className="uep-widget-config">
          <WidgetConfigPanel config={activeWidget} onChange={handleConfigChange} />
        </div>
      )}
    </div>
  );
}
