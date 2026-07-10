import { Checkbox, InputNumber, Select } from 'antd';
import { useTranslation } from 'react-i18next';
import type { WidgetConfig } from '../core/pluginModel';

interface Props {
  config: WidgetConfig;
  onChange: (next: WidgetConfig) => void;
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="wcp-field">
      <span className="wcp-field-label">{label}</span>
      <div className="wcp-field-value">{children}</div>
    </div>
  );
}

function BaseFields({ config, onChange }: { config: WidgetConfig; onChange: (next: WidgetConfig) => void }) {
  const { t } = useTranslation();
  return (
    <>
      <Field label={t('selfEvolutionRun.wcpReadOnly')}>
        <Checkbox
          checked={!!config.readOnly}
          onChange={(e) => onChange({ ...config, readOnly: e.target.checked || undefined } as WidgetConfig)}
        />
      </Field>
      <Field label={t('selfEvolutionRun.wcpMaxHeight')}>
        <InputNumber
          size="small"
          min={0}
          value={config.maxHeight}
          placeholder={t('selfEvolutionRun.wcpPlaceholderUnlimited')}
          onChange={(v) => onChange({ ...config, maxHeight: v ?? undefined } as WidgetConfig)}
          style={{ width: 80 }}
          addonAfter="px"
        />
      </Field>
    </>
  );
}

function TextListFields({ config, onChange }: { config: Extract<WidgetConfig, { widgetType: 'text-list' }>; onChange: (next: WidgetConfig) => void }) {
  const { t } = useTranslation();
  return (
    <>
      <BaseFields config={config} onChange={onChange} />
      <Field label={t('selfEvolutionRun.wcpItemLayout')}>
        <Select
          size="small"
          value={config.itemLayout ?? 'vertical'}
          options={[
            { value: 'vertical', label: t('selfEvolutionRun.wcpLayoutVertical') },
            { value: 'horizontal', label: t('selfEvolutionRun.wcpLayoutHorizontal') },
            { value: 'grid', label: 'Grid' },
          ]}
          onChange={(v) => onChange({ ...config, itemLayout: v })}
          style={{ width: 90 }}
        />
      </Field>
      {config.itemLayout === 'horizontal' && (
        <Field label={t('selfEvolutionRun.wcpItemMaxWidth')}>
          <InputNumber
            size="small"
            min={0}
            value={config.itemMaxWidth}
            placeholder={t('selfEvolutionRun.wcpPlaceholderUnlimited')}
            onChange={(v) => onChange({ ...config, itemMaxWidth: v ?? undefined })}
            style={{ width: 80 }}
            addonAfter="px"
          />
        </Field>
      )}
      {config.itemLayout === 'grid' && (
        <Field label={t('selfEvolutionRun.wcpGridMaxCols')}>
          <InputNumber
            size="small"
            min={1}
            max={12}
            value={config.gridMaxCols}
            placeholder={t('selfEvolutionRun.wcpPlaceholderAuto')}
            onChange={(v) => onChange({ ...config, gridMaxCols: v ?? undefined })}
            style={{ width: 70 }}
          />
        </Field>
      )}
      <Field label={t('selfEvolutionRun.wcpShowAddButton')}>
        <Checkbox
          checked={config.showAddButton !== false}
          onChange={(e) => onChange({ ...config, showAddButton: e.target.checked ? undefined : false })}
        />
      </Field>
    </>
  );
}

function ImageSingleFields({ config, onChange }: { config: Extract<WidgetConfig, { widgetType: 'image-single' }>; onChange: (next: WidgetConfig) => void }) {
  const { t } = useTranslation();
  return (
    <>
      <BaseFields config={config} onChange={onChange} />
      <Field label={t('selfEvolutionRun.wcpImageHeight')}>
        <InputNumber
          size="small"
          min={0}
          value={config.imageHeight}
          placeholder={t('selfEvolutionRun.wcpPlaceholderAuto')}
          onChange={(v) => onChange({ ...config, imageHeight: v ?? undefined })}
          style={{ width: 80 }}
          addonAfter="px"
        />
      </Field>
    </>
  );
}

function ImageGalleryFields({ config, onChange }: { config: Extract<WidgetConfig, { widgetType: 'image-gallery' }>; onChange: (next: WidgetConfig) => void }) {
  const { t } = useTranslation();
  return (
    <>
      <BaseFields config={config} onChange={onChange} />
      <Field label={t('selfEvolutionRun.wcpGalleryLayout')}>
        <Select
          size="small"
          value={config.itemLayout ?? 'horizontal'}
          options={[
            { value: 'horizontal', label: t('selfEvolutionRun.wcpLayoutHorizontal') },
            { value: 'grid', label: 'Grid' },
          ]}
          onChange={(v) => onChange({ ...config, itemLayout: v })}
          style={{ width: 90 }}
        />
      </Field>
      <Field label={t('selfEvolutionRun.wcpCardWidth')}>
        <InputNumber
          size="small"
          min={60}
          value={config.itemWidth ?? 180}
          onChange={(v) => onChange({ ...config, itemWidth: v ?? 180 })}
          style={{ width: 80 }}
          addonAfter="px"
        />
      </Field>
      <Field label={t('selfEvolutionRun.wcpCardHeight')}>
        <InputNumber
          size="small"
          min={40}
          value={config.itemHeight ?? 140}
          onChange={(v) => onChange({ ...config, itemHeight: v ?? 140 })}
          style={{ width: 80 }}
          addonAfter="px"
        />
      </Field>
      {config.itemLayout === 'grid' && (
        <Field label={t('selfEvolutionRun.wcpGridMaxCols')}>
          <InputNumber
            size="small"
            min={1}
            max={12}
            value={config.gridMaxCols}
            placeholder={t('selfEvolutionRun.wcpPlaceholderAuto')}
            onChange={(v) => onChange({ ...config, gridMaxCols: v ?? undefined })}
            style={{ width: 70 }}
          />
        </Field>
      )}
      <Field label={t('selfEvolutionRun.wcpShowAddButton')}>
        <Checkbox
          checked={config.showAddButton !== false}
          onChange={(e) => onChange({ ...config, showAddButton: e.target.checked ? undefined : false })}
        />
      </Field>
    </>
  );
}

function JsonBlockFields({ config, onChange }: { config: Extract<WidgetConfig, { widgetType: 'json-block' }>; onChange: (next: WidgetConfig) => void }) {
  const { t } = useTranslation();
  return (
    <>
      <BaseFields config={config} onChange={onChange} />
      <Field label={t('selfEvolutionRun.wcpCollapsed')}>
        <Checkbox
          checked={!!config.collapsed}
          onChange={(e) => onChange({ ...config, collapsed: e.target.checked || undefined })}
        />
      </Field>
    </>
  );
}

/** Inline property editor panel for a WidgetConfig. Renders only fields relevant to the widgetType. */
export default function WidgetConfigPanel({ config, onChange }: Props) {
  switch (config.widgetType) {
    case 'text-single':
    case 'text-markdown':
    case 'file-card':
      return (
        <div className="wcp-root">
          <BaseFields config={config} onChange={onChange} />
        </div>
      );
    case 'text-list':
      return (
        <div className="wcp-root">
          <TextListFields config={config} onChange={onChange} />
        </div>
      );
    case 'image-single':
      return (
        <div className="wcp-root">
          <ImageSingleFields config={config} onChange={onChange} />
        </div>
      );
    case 'image-gallery':
      return (
        <div className="wcp-root">
          <ImageGalleryFields config={config} onChange={onChange} />
        </div>
      );
    case 'json-block':
      return (
        <div className="wcp-root">
          <JsonBlockFields config={config} onChange={onChange} />
        </div>
      );
    default:
      return null;
  }
}
