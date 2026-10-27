import { useTranslation } from 'react-i18next';

export function WriterImageGroupControls({ layout, readOnly, onLayout }: {
  layout?: 'sliding' | 'vertical'; readOnly?: boolean;
  onLayout: (layout: 'sliding' | 'vertical') => void;
}) {
  const { t } = useTranslation();
  if (readOnly) return null;
  return <div className='writer-image-group-controls' contentEditable={false} onMouseDown={event => event.preventDefault()}>
    <button type='button' onClick={() => onLayout(layout === 'sliding' ? 'vertical' : 'sliding')}>
      {t(layout === 'sliding' ? 'chat.writerImageGroups.restoreVertical' : 'chat.writerImageGroups.setSliding')}
    </button>
  </div>;
}
