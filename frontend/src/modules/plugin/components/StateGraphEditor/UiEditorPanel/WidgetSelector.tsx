import { Select } from 'antd';
import type { WidgetType } from '../core/pluginModel';
import { SLOT_COMPATIBLE_WIDGETS, SLOT_DEFAULT_WIDGET } from '../core/pluginModel';

const WIDGET_LABELS: Record<WidgetType, string> = {
  'text-single': '单行文本块',
  'text-list': '编号文本列表',
  'text-markdown': 'Markdown 渲染',
  'image-single': '单张图片',
  'image-gallery': '图片画廊',
  'file-card': '文件卡片',
  'json-block': 'JSON 代码块',
};

interface Props {
  slotType: string;
  cardinality?: string;
  value?: WidgetType;
  onChange?: (widgetType: WidgetType) => void;
  size?: 'small' | 'middle' | 'large';
}

export default function WidgetSelector({ slotType, cardinality, value, onChange, size = 'small' }: Props) {
  const key = `${slotType}/${cardinality ?? 'single'}`;
  const compatible = SLOT_COMPATIBLE_WIDGETS[key] ?? ['text-single'];
  const defaultWidget = SLOT_DEFAULT_WIDGET[key] ?? 'text-single';
  const currentValue = value ?? defaultWidget;

  const options = compatible.map((wt) => ({
    value: wt,
    label: WIDGET_LABELS[wt] ?? wt,
  }));

  return (
    <Select
      size={size}
      value={currentValue}
      options={options}
      onChange={(v) => onChange?.(v as WidgetType)}
      style={{ minWidth: 120 }}
      popupMatchSelectWidth={false}
    />
  );
}
