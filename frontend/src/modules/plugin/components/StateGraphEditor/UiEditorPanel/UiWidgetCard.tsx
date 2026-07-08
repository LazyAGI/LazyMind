import { useSortable } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { Button, Tooltip } from 'antd';
import { CloseOutlined, HolderOutlined, FileTextOutlined } from '@ant-design/icons';
import type { SlotDef } from '../core/model';
import type { WidgetConfig, WidgetType } from '../core/pluginModel';
import { SLOT_DEFAULT_WIDGET } from '../core/pluginModel';
import { SLOT_TYPE_ICONS } from './slotTypeIcon';
import WidgetPlaceholder from './WidgetPlaceholder';

interface Props {
  slotId: string;
  slotDef?: SlotDef;
  widget?: WidgetConfig;
  isSelected?: boolean;
  onSelect?: (slotId: string) => void;
  onRemove: (slotId: string) => void;
}

export default function UiWidgetCard({ slotId, slotDef, widget, isSelected, onSelect, onRemove }: Props) {
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

  return (
    <div
      ref={setNodeRef}
      style={style}
      className={`uep-widget-card${isSelected ? ' uep-widget-card--selected' : ''}`}
      onClick={() => onSelect?.(slotId)}
      {...attributes}
    >
      {/* Zone 1: Header */}
      <div className="uep-widget-card-header">
        <span className="uep-widget-drag" {...listeners} aria-label="拖拽排序" onClick={(e) => e.stopPropagation()}>
          <HolderOutlined />
        </span>
        <span className="uep-widget-icon">{icon}</span>
        <span className="uep-widget-label">{label}</span>
        <Tooltip title="从当前 Tab 移除">
          <Button
            type="text"
            size="small"
            icon={<CloseOutlined />}
            className="uep-widget-remove"
            onClick={(e) => { e.stopPropagation(); onRemove(slotId); }}
            aria-label={`移除 ${label}`}
          />
        </Tooltip>
      </div>

      {/* Zone 2: Placeholder preview */}
      <div className="uep-widget-preview">
        <WidgetPlaceholder widgetConfig={activeWidget} label={label} />
      </div>
    </div>
  );
}
