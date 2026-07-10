import { Select } from 'antd';
import { useTranslation } from 'react-i18next';
import type { WidgetType } from '../core/pluginModel';
import { SLOT_COMPATIBLE_WIDGETS, SLOT_DEFAULT_WIDGET } from '../core/pluginModel';

interface Props {
  slotType: string;
  cardinality?: string;
  value?: WidgetType;
  onChange?: (widgetType: WidgetType) => void;
  size?: 'small' | 'middle' | 'large';
}

export default function WidgetSelector({ slotType, cardinality, value, onChange, size = 'small' }: Props) {
  const { t } = useTranslation();
  const WIDGET_LABELS: Record<WidgetType, string> = {
    'text-single': t('selfEvolutionRun.widgetTextSingle'),
    'text-list': t('selfEvolutionRun.widgetTextList'),
    'text-markdown': t('selfEvolutionRun.widgetTextMarkdown'),
    'image-single': t('selfEvolutionRun.widgetImageSingle'),
    'image-gallery': t('selfEvolutionRun.widgetImageGallery'),
    'file-card': t('selfEvolutionRun.widgetFileCard'),
    'json-block': t('selfEvolutionRun.widgetJsonBlock'),
  };
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
