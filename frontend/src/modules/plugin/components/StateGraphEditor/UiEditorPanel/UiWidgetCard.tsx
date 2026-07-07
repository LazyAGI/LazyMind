import { useSortable } from '@dnd-kit/sortable';
import { CSS } from '@dnd-kit/utilities';
import { Button, Tooltip } from 'antd';
import { CloseOutlined, HolderOutlined, FileTextOutlined } from '@ant-design/icons';
import type { SlotDef } from '../core/model';
import { SLOT_TYPE_ICONS, SLOT_TYPE_LABELS } from './slotTypeIcon';

interface Props {
  slotId: string;
  slotDef?: SlotDef;
  onRemove: (slotId: string) => void;
}

export default function UiWidgetCard({ slotId, slotDef, onRemove }: Props) {
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } =
    useSortable({ id: slotId });

  const style: React.CSSProperties = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.4 : 1,
  };

  const type = slotDef?.type ?? 'text';
  const icon = SLOT_TYPE_ICONS[type] ?? <FileTextOutlined />;
  const typeLabel = SLOT_TYPE_LABELS[type] ?? type;
  const label = slotDef?.label ?? slotId;
  const isList = slotDef?.cardinality === 'list';

  return (
    <div
      ref={setNodeRef}
      style={style}
      className={`uep-widget-card uep-widget-card--${type}`}
      {...attributes}
    >
      <span className="uep-widget-drag" {...listeners} aria-label="拖拽排序">
        <HolderOutlined />
      </span>
      <span className="uep-widget-icon">{icon}</span>
      <span className="uep-widget-label">{label}</span>
      <span className="uep-widget-meta">
        {typeLabel}{isList ? ' · 列表' : ''}
      </span>
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
  );
}
