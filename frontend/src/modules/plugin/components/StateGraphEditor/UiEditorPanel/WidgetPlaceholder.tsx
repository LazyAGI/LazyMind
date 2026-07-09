import { PictureOutlined, FileOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import type { WidgetConfig } from '../core/pluginModel';
import './UiWysiwygPreview.scss';

const JSON_SAMPLE = `{\n  "key": "value",\n  "items": [1, 2, 3],\n  "status": "success"\n}`;

interface Props {
  widgetConfig: WidgetConfig;
  label?: string;
}

function TextSinglePlaceholder({ config, label }: { config: Extract<WidgetConfig, { widgetType: 'text-single' }>; label?: string }) {
  const { t } = useTranslation();
  const style: React.CSSProperties = {};
  if (config.maxHeight) style.maxHeight = config.maxHeight;
  return (
    <div className="wp-root">
      {label && <div className="wp-label">{label}</div>}
      <div
        className={`wp-text-single${config.readOnly ? ' wp-text-single--readonly' : ''}`}
        style={style}
      >
        {t('selfEvolutionRun.widgetPlaceholderLoremText')}
      </div>
    </div>
  );
}

function TextListPlaceholder({ config, label }: { config: Extract<WidgetConfig, { widgetType: 'text-list' }>; label?: string }) {
  const { t } = useTranslation();
  const items = 3;
  const gridCols = config.gridMaxCols ?? 2;
  const containerClass = [
    'wp-text-list',
    config.itemLayout === 'horizontal' ? 'wp-text-list--horizontal' : '',
    config.itemLayout === 'grid' ? 'wp-text-list--grid' : '',
  ].filter(Boolean).join(' ');
  const containerStyle: React.CSSProperties = {};
  if (config.itemLayout === 'grid') (containerStyle as Record<string, unknown>)['--wp-grid-cols'] = `repeat(${gridCols}, 1fr)`;
  if (config.itemMaxWidth && config.itemLayout !== 'vertical') containerStyle.maxWidth = config.itemMaxWidth;

  return (
    <div className="wp-root">
      {label && <div className="wp-label">{label}</div>}
      <div className={containerClass} style={containerStyle}>
        {Array.from({ length: items }).map((_, i) => (
          <div key={i} className="wp-text-list-item">
            <span className="wp-text-list-badge">{i + 1}</span>
            {t('selfEvolutionRun.widgetPlaceholderLoremShort')}
          </div>
        ))}
        {config.showAddButton !== false && (
          <button type="button" className="wp-add-btn">+ {t('selfEvolutionRun.widgetPlaceholderAddNew')}</button>
        )}
      </div>
    </div>
  );
}

function TextMarkdownPlaceholder({ config, label }: { config: Extract<WidgetConfig, { widgetType: 'text-markdown' }>; label?: string }) {
  const { t } = useTranslation();
  const style: React.CSSProperties = {};
  if (config.maxHeight) style.maxHeight = config.maxHeight;
  return (
    <div className="wp-root">
      {label && <div className="wp-label">{label}</div>}
      <div className="wp-text-markdown" style={style}>
        <p><strong>{t('selfEvolutionRun.widgetPlaceholderMarkdownTitle')}</strong></p>
        <p>{t('selfEvolutionRun.widgetPlaceholderMarkdownBody')}</p>
      </div>
    </div>
  );
}

function ImageSinglePlaceholder({ config, label }: { config: Extract<WidgetConfig, { widgetType: 'image-single' }>; label?: string }) {
  const { t } = useTranslation();
  const style: React.CSSProperties = {};
  if (config.imageHeight) style.height = config.imageHeight;
  return (
    <div className="wp-root">
      {label && <div className="wp-label">{label}</div>}
      <div className="wp-image-single" style={style}>
        <PictureOutlined />
        <span style={{ fontSize: 12 }}>{label ?? t('selfEvolutionRun.widgetPlaceholderImageLabel')}</span>
      </div>
    </div>
  );
}

function ImageGalleryPlaceholder({ config, label }: { config: Extract<WidgetConfig, { widgetType: 'image-gallery' }>; label?: string }) {
  const { t } = useTranslation();
  const cardW = config.itemWidth ?? 180;
  const cardH = config.itemHeight ?? 140;
  const isGrid = config.itemLayout === 'grid';
  const gridCols = config.gridMaxCols ?? 3;
  const containerClass = `wp-image-gallery${isGrid ? ' wp-image-gallery--grid' : ''}`;
  const containerStyle: React.CSSProperties = {};
  if (isGrid) (containerStyle as Record<string, unknown>)['--wp-gallery-cols'] = `repeat(${gridCols}, ${cardW}px)`;

  return (
    <div className="wp-root">
      {label && <div className="wp-label">{label}</div>}
      <div className={containerClass} style={containerStyle}>
        {[0, 1, 2].map((i) => (
          <div
            key={i}
            className="wp-image-gallery-card"
            style={{ width: cardW, height: cardH }}
          >
            <PictureOutlined />
          </div>
        ))}
        {config.showAddButton !== false && (
          <div
            className="wp-image-gallery-card"
            style={{ width: cardW, height: cardH, border: '1.5px dashed #d9d9d9', color: '#bfbfbf', fontSize: 12, gap: 4 }}
          >
            <span style={{ fontSize: 20 }}>+</span>
            <span>{t('selfEvolutionRun.widgetPlaceholderAddAttachment')}</span>
          </div>
        )}
      </div>
    </div>
  );
}

function FileCardPlaceholder({ config, label }: { config: Extract<WidgetConfig, { widgetType: 'file-card' }>; label?: string }) {
  const { t } = useTranslation();
  return (
    <div className="wp-root">
      {label && <div className="wp-label">{label}</div>}
      <div className={`wp-file-card${config.readOnly ? ' wp-file-card--readonly' : ''}`}>
        <FileOutlined className="wp-file-icon" />
        <div className="wp-file-info">
          <span className="wp-file-name">{label ?? t('selfEvolutionRun.widgetPlaceholderFileLabel')} {t('selfEvolutionRun.widgetPlaceholderSampleFileName')}</span>
          <span className="wp-file-size">128 KB</span>
        </div>
        <div className="wp-file-actions">
          <button type="button" className="wp-file-action-btn">{t('selfEvolutionRun.widgetPlaceholderPreview')}</button>
          <button type="button" className="wp-file-action-btn">{t('selfEvolutionRun.widgetPlaceholderDownload')}</button>
        </div>
      </div>
    </div>
  );
}

function JsonBlockPlaceholder({ config, label }: { config: Extract<WidgetConfig, { widgetType: 'json-block' }>; label?: string }) {
  const style: React.CSSProperties = {};
  if (config.maxHeight) style.maxHeight = config.maxHeight;
  return (
    <div className="wp-root">
      {label && <div className="wp-label">{label}</div>}
      <pre
        className={`wp-json-block${config.collapsed ? ' wp-json-collapsed' : ''}`}
        style={style}
      >
        {JSON_SAMPLE}
      </pre>
    </div>
  );
}

/** Renders a static placeholder preview for a given WidgetConfig. No interactions. */
export default function WidgetPlaceholder({ widgetConfig, label }: Props) {
  switch (widgetConfig.widgetType) {
    case 'text-single':
      return <TextSinglePlaceholder config={widgetConfig} label={label} />;
    case 'text-list':
      return <TextListPlaceholder config={widgetConfig} label={label} />;
    case 'text-markdown':
      return <TextMarkdownPlaceholder config={widgetConfig} label={label} />;
    case 'image-single':
      return <ImageSinglePlaceholder config={widgetConfig} label={label} />;
    case 'image-gallery':
      return <ImageGalleryPlaceholder config={widgetConfig} label={label} />;
    case 'file-card':
      return <FileCardPlaceholder config={widgetConfig} label={label} />;
    case 'json-block':
      return <JsonBlockPlaceholder config={widgetConfig} label={label} />;
    default:
      return null;
  }
}
