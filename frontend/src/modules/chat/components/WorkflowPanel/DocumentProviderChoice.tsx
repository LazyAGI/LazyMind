import { useState } from 'react';
import { Radio, type RadioChangeEvent } from 'antd';
import { FolderOpenOutlined, GithubOutlined } from '@ant-design/icons';
import type { DocumentProvider } from '@/api/generated/core-client';
import { cloudProviderOptions } from '@/modules/modelProvider/constants/cloudProviderOptions';
import i18n from '@/i18n';

const legacyProviders = ['feishu', 'notion', 'github', 'wechat', 'obsidian'];
function ObsidianIcon() {
  const [failed, setFailed] = useState(false);
  return failed ? <FolderOpenOutlined aria-hidden='true' />
    : <img src='https://obsidian.md/images/obsidian-logo-gradient.svg' alt='' aria-hidden='true' onError={() => setFailed(true)} />;
}

export function WriterProviderIcon({ provider }: { provider: string }) {
  const config = cloudProviderOptions.find((item) => item.type === provider);
  return provider === 'github' ? <GithubOutlined aria-hidden='true' /> : provider === 'obsidian' ? <ObsidianIcon />
    : config?.logoUrl ? <img src={config.logoUrl} alt='' aria-hidden='true' /> : config?.icon ?? <FolderOpenOutlined aria-hidden='true' />;
}

// Provider IDs and eligibility come from the registry. Known icons are only
// presentation; the optional old props remain compatible with older callers.
export function WriterProviderChoice({ initialProvider, githubEnabled, providers, onChange }: {
  initialProvider: string; githubEnabled: boolean; providers?: DocumentProvider[]; onChange: (provider: string) => void;
}) {
  const [value, setValue] = useState(initialProvider);
  const ids = providers?.map((provider) => provider.id) ?? legacyProviders;
  return <div className='workflow-writer-provider-picker'>
    <div className='workflow-writer-provider-picker__hint'>{i18n.t('chat.writerIR.providerPickerHint')}</div>
    <Radio.Group value={value} className='workflow-writer-provider-picker__options'
      onChange={(event: RadioChangeEvent) => { setValue(event.target.value); onChange(event.target.value); }}>
      {ids.map((id) => {
        const disabled = providers === undefined && id === 'github' && !githubEnabled;
        const key = `chat.writerIR.providers.${id}`;
        return <Radio key={id} value={id} disabled={disabled}>
          <span className='workflow-writer-provider-picker__option'>
            <WriterProviderIcon provider={id} />
            <span>{i18n.exists(key) ? i18n.t(key) : id}</span>
            {disabled && <small>{i18n.t('chat.writerIR.githubTargetRequired')}</small>}
          </span>
        </Radio>;
      })}
    </Radio.Group>
  </div>;
}
