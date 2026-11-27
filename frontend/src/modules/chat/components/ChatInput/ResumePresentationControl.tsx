import { useRef, useState, type KeyboardEvent } from 'react';
import { CheckOutlined, DownOutlined, FileTextOutlined } from '@ant-design/icons';
import { Modal } from 'antd';
import { useTranslation } from 'react-i18next';
import type { ComposerFileCandidate } from './composerReferences';
import { RESUME_TEMPLATES, resumePhotoCandidates, type ResumePresentation, type ResumeTemplateId } from '../../utils/resumePresentation';
import './ResumePresentationControl.scss';

function TemplateThumbnail({ template, withPhoto }: { template: ResumeTemplateId; withPhoto: boolean }) {
  const color = RESUME_TEMPLATES.find(item => item.id === template)!.color;
  const blocks = (x: number, y: number, width: number, groups = 3) => Array.from({ length: groups }, (_, group) => <g key={group}>
    <rect x={x} y={y + group * 27} width={width * .46} height='3.5' rx='1' fill={color} />
    {[0, 1, 2].map(line => <rect key={line} x={x} y={y + 8 + group * 27 + line * 5} width={width * (line === 2 ? .72 : .96)} height='1.6' rx='.7' fill='#b9c2cb' />)}
  </g>);
  const photo = withPhoto ? <rect x={template === 'sidebar' ? 13 : 80} y={template === 'sidebar' ? 14 : 12} width='16' height='21' rx='2' fill={template === 'modern' ? '#ffffff88' : '#dbe5eb'} /> : null;
  return <svg className={`resume-template-thumbnail is-${template}`} viewBox='0 0 110 148' aria-hidden='true'>
    <rect x='0' y='0' width='110' height='148' fill='#fff' />
    {template === 'modern' ? <><rect width='110' height='40' fill={color} /><rect x='12' y='14' width='47' height='6' rx='1' fill='#fff' /><rect x='12' y='26' width='57' height='2' fill='#ffffffaa' />{blocks(12, 53, 87)}</> :
      template === 'sidebar' ? <><rect width='39' height='148' fill={color} /><rect x='11' y={withPhoto ? 44 : 16} width='20' height='4' fill='#fff' />{[63, 90, 117].map(y => <g key={y}><rect x='9' y={y} width='21' height='2' fill='#ffffffbb' /><rect x='9' y={y + 8} width='17' height='1.6' fill='#ffffff88' /></g>)}<rect x='48' y='16' width='47' height='5' fill={color} />{blocks(48, 36, 52, 4)}</> :
      template === 'editorial' ? <><rect x='10' y='12' width='5' height='24' fill={color} /><rect x='22' y='13' width='49' height='8' fill='#343b43' /><rect x='22' y='29' width='45' height='2' fill='#89949d' /><rect x='10' y='45' width='90' height='1' fill={color} />{[57, 88, 119].map((y, i) => <g key={y}><text x='10' y={y + 5} fill={color} fontSize='6' fontWeight='700'>0{i + 1}</text>{blocks(27, y, 73, 1)}</g>)}</> :
      template === 'campus' ? <><rect x='8' y='8' width='94' height='32' rx='4' fill='#eaf5f0' /><rect x='14' y='15' width='43' height='6' fill={color} /><rect x='14' y='28' width='57' height='2' fill='#8ab1a6' /><path d='M8 49H102' stroke={color} strokeWidth='1.5' />{blocks(13, 58, 85)}</> :
      template === 'minimal' ? <><rect x='30' y='13' width='50' height='5' fill='#343b43' /><rect x='19' y='25' width='72' height='1.7' fill='#89949d' /><rect x='10' y='38' width='90' height='.8' fill='#343b43' />{blocks(11, 49, 88)}</> :
      <><rect x='11' y='13' width='45' height='7' fill={color} /><rect x='11' y='26' width='57' height='2' fill='#89949d' /><rect x='11' y='39' width='88' height='1.5' fill={color} />{blocks(11, 50, 88)}</>}
    {photo}
  </svg>;
}

export default function ResumePresentationControl({ value, onChange, files, disabled, onUploadPhoto }: {
  value: ResumePresentation;
  onChange: (value: ResumePresentation) => void;
  files: ComposerFileCandidate[];
  disabled: boolean;
  onUploadPhoto: () => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const trigger = useRef<HTMLButtonElement>(null);
  const options = useRef<HTMLDivElement>(null);
  const photos = resumePhotoCandidates(files);
  const selectedPhotoAvailable = !value.photoUri || photos.some(photo => photo.input?.uri === value.photoUri);
  const templateKey = (id: ResumeTemplateId) => `chat.resumePresentation.templates.${id}`;
  const handleKeys = (event: KeyboardEvent<HTMLDivElement>) => {
    const radios = [...(options.current?.querySelectorAll<HTMLButtonElement>('[role="radio"]') || [])];
    const index = radios.indexOf(document.activeElement as HTMLButtonElement);
    const step = event.key === 'ArrowRight' || event.key === 'ArrowDown' ? 1 : event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : 0;
    if (!step && event.key !== 'Home' && event.key !== 'End') return;
    event.preventDefault();
    const target = event.key === 'Home' ? 0 : event.key === 'End' ? radios.length - 1 : (index + step + radios.length) % radios.length;
    radios[target]?.focus();
    onChange({ ...value, template: RESUME_TEMPLATES[target].id });
  };
  return <div className='resume-presentation-control'>
    <div className='resume-presentation-toolbar'>
      <button ref={trigger} type='button' className='resume-template-trigger' disabled={disabled} onClick={() => setOpen(true)} aria-haspopup='dialog'>
        <FileTextOutlined aria-hidden /><span>{t('chat.resumePresentation.template')}: <strong>{t(`${templateKey(value.template)}.name`)}</strong></span><DownOutlined aria-hidden />
      </button>
      <label className='resume-photo-select'><span>{t('chat.resumePresentation.photo')}</span>
        <select aria-label={t('chat.resumePresentation.photo')} value={value.photoUri || ''} disabled={disabled}
          onChange={event => onChange({ ...value, photoUri: event.target.value || undefined })}>
          <option value=''>{t('chat.resumePresentation.noPhoto')}</option>
          {!selectedPhotoAvailable && <option value={value.photoUri}>{t('chat.resumePresentation.unavailablePhoto')}</option>}
          {photos.map(photo => <option key={photo.input!.uri} value={photo.input!.uri}>{photo.name}</option>)}
        </select>
      </label>
      {value.photoUri && <label className='resume-photo-fit'><span className='resume-sr-only'>{t('chat.resumePresentation.photoFit')}</span><select
        aria-label={t('chat.resumePresentation.photoFit')} value={value.photoFit} disabled={disabled}
        onChange={event => onChange({ ...value, photoFit: event.target.value === 'cover' ? 'cover' : 'contain' })}>
        <option value='contain'>{t('chat.resumePresentation.contain')}</option><option value='cover'>{t('chat.resumePresentation.cover')}</option>
      </select></label>}
      <button type='button' className='resume-photo-upload' disabled={disabled} onClick={onUploadPhoto}>{t('chat.resumePresentation.uploadPhoto')}</button>
    </div>
    {!selectedPhotoAvailable ? <p className='resume-photo-warning' role='alert'>{t('chat.resumePresentation.photoUnavailableHint')}</p> : null}
    <Modal title={t('chat.resumePresentation.chooseTemplate')} open={open} onCancel={() => setOpen(false)} footer={null} width={700}
      afterClose={() => trigger.current?.focus()} afterOpenChange={(visible: boolean) => { if (visible) options.current?.querySelector<HTMLButtonElement>('[aria-checked="true"]')?.focus(); }}>
      <p className='resume-template-intro'>{t('chat.resumePresentation.templateHint')}</p>
      <div ref={options} className='resume-template-grid' role='radiogroup' aria-label={t('chat.resumePresentation.chooseTemplate')} onKeyDown={handleKeys}>
        {RESUME_TEMPLATES.map(template => <button key={template.id} type='button' role='radio' aria-checked={value.template === template.id}
          tabIndex={value.template === template.id ? 0 : -1} className={`resume-template-card${value.template === template.id ? ' is-selected' : ''}`}
          onClick={() => onChange({ ...value, template: template.id })}>
          <TemplateThumbnail template={template.id} withPhoto={Boolean(value.photoUri)} />
          <span className='resume-template-card-name'>{t(`${templateKey(template.id)}.name`)}{value.template === template.id && <CheckOutlined aria-hidden />}</span>
          <small>{t(`${templateKey(template.id)}.description`)}</small>
        </button>)}
      </div>
      <div className='resume-template-footer'><span>{t('chat.resumePresentation.formats')}</span><button type='button' className='resume-template-done' onClick={() => setOpen(false)}>{t('chat.resumePresentation.done')}</button></div>
    </Modal>
  </div>;
}
