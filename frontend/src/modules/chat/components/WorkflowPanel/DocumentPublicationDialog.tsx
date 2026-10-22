import { useCallback, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent, type ReactNode } from 'react';
import { Alert, Button, Checkbox, Modal, Popover, Select, Spin, Tag } from 'antd';
import { CheckCircleOutlined, CloudOutlined, CloudUploadOutlined, ExportOutlined, ReloadOutlined } from '@ant-design/icons';
import type { CheckboxChangeEvent } from 'antd/es/checkbox';
import { useTranslation } from 'react-i18next';
import type { DocumentProvider, DocumentPublicationGroupRequest, DocumentPublicationGroupState, DocumentPublicationTargetState } from '@/api/generated/core-client';
import { WorkflowSessionApi } from '@/modules/chat/utils/request';
import { CLOUD_DOCUMENTS_OBSIDIAN_SETUP_PATH, getCloudDocumentsUrl } from '@/modules/modelProvider/utils/cloudDocumentUrls';
import { useWriterProviderAvailability } from './useWriterProviderAvailability';
import { documentPublicationTargetUrl } from './documentPublicationUrl';
import { documentPublicationErrorMessage } from './documentPublicationError';
import { WriterProviderIcon } from './DocumentProviderChoice';
import './DocumentPublicationDialog.scss';

const running = new Set(['preparing', 'write_started', 'provider_confirmed', 'local_persist_failed', 'local_conflict']);
const blocked = new Set([...running, 'outcome_unknown']);
type Baseline = { id: string; revision: number; draft?: number };
export type PublicationProviderChoices = Record<string, { selectable: boolean; reason: string }>;
type Props = {
  artifactId: string;
  providers: DocumentProvider[];
  open: boolean;
  initialProvider?: string;
  dirty: boolean;
  readOnly?: boolean;
  onClose: () => void;
  beforePublish: (providers: string[]) => Promise<Baseline | undefined>;
  onProviderChoicesChange?: (choices: PublicationProviderChoices) => void;
};

export function DocumentPublicationDialog({ artifactId, providers, open, initialProvider, dirty, readOnly, onClose, beforePublish, onProviderChoicesChange }: Props) {
  const { t } = useTranslation();
  const label = (provider: string) => t(`chat.writerIR.providers.${provider}`, { defaultValue: provider });
  const availability = useWriterProviderAvailability(providers.map(item => item.id));
  const [state, setState] = useState<DocumentPublicationGroupState>();
  const [selected, setSelected] = useState<string[]>([]);
  const [connectionChoices, setConnectionChoices] = useState<Record<string, string>>({});
  const [retryOperations, setRetryOperations] = useState<Record<string, string>>({});
  const [error, setError] = useState('');
  const [authorizationProvider, setAuthorizationProvider] = useState<string>();
  const [loading, setLoading] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [resultsOpen, setResultsOpen] = useState(false);
  const resultsId = useId();
  const resultsButton = useRef<HTMLButtonElement>(null);
  const resultsPanel = useRef<HTMLElement>(null);
  const focusResults = useRef(false);
  const lock = useRef(false);
  const generation = useRef(0);
  const pending = useRef<{ id: string; body: DocumentPublicationGroupRequest }>();
  const [uncertain, setUncertain] = useState(false);
  const initialized = useRef(false);
  const load = useCallback(async () => {
    const request = ++generation.current;
    const response = await WorkflowSessionApi().getPublicationTargets(artifactId, { silentError: true });
    if (request === generation.current) setState(response.data.data);
    return response.data.data;
  }, [artifactId]);

  const accountFor = useCallback((provider: string, target?: DocumentPublicationTargetState) => {
    const accounts = availability.accounts?.[provider] ?? [];
    const previous = accounts.find(account => account.connection_id === target?.connection_id);
    if (target?.bound && target.connection_id) return previous;
    if (connectionChoices[provider]) return accounts.find(account => account.connection_id === connectionChoices[provider]);
    return previous || (accounts.length === 1 ? accounts[0] : undefined);
  }, [availability.accounts, connectionChoices]);
  const isAuthorized = useCallback((provider: string) => {
    if (availability.states[provider] !== 'ready') return false;
    if (provider === 'obsidian') return true;
    const accounts = availability.accounts?.[provider] ?? [];
    const target = state?.targets.find(item => item.provider === provider);
    return target?.bound && target.connection_id ? accounts.some(account => account.connection_id === target.connection_id) : accounts.length > 0;
  }, [availability.states, availability.accounts, state?.targets]);

  const providerChoices = useMemo<PublicationProviderChoices>(() => Object.fromEntries(providers.map(provider => {
    const target = state?.targets.find(item => item.provider === provider.id);
    const unsupported = provider.id === 'obsidian' && !target?.bound;
    const busy = !!target?.operation && blocked.has(target.operation.status);
    const ready = isAuthorized(provider.id);
    const reason = !state ? t('chat.writerLocal.checking') : unsupported ? t('chat.publication.boundOnly')
      : busy ? t(`chat.publication.status.${target!.operation!.status}`, { defaultValue: t('chat.publication.status.outcome_unknown') })
        : ready ? '' : t(availability.states[provider.id] === 'chat-disabled' ? 'chat.writerLocal.feishuChatDisabledShort'
          : availability.states[provider.id] === 'failed' ? 'chat.writerLocal.platformFailed'
            : ['authorize', 'ready'].includes(availability.states[provider.id]) ? 'chat.writerLocal.authorizeShort' : 'chat.writerLocal.checking');
    return [provider.id, { selectable: !!state && !unsupported && !busy && ready, reason }];
  })), [providers, state, isAuthorized, availability.states, t]);
  const canSelect = useCallback((provider: string) => providerChoices[provider]?.selectable === true, [providerChoices]);
  useEffect(() => { onProviderChoicesChange?.(providerChoices); }, [providerChoices, onProviderChoicesChange]);

  useEffect(() => {
    let canceled = false;
    let timer: ReturnType<typeof setTimeout>;
    const refresh = async () => {
      try {
        const next = await load();
        if (!canceled && next.targets.some(item => item.operation && running.has(item.operation.status))) timer = setTimeout(refresh, 2000);
      } catch { if (!canceled) setError(t('chat.publication.loadFailed')); }
    };
    void refresh();
    return () => { canceled = true; generation.current++; clearTimeout(timer); };
  }, [load, open, submitting, t]);

  useEffect(() => {
    if (!open) { initialized.current = false; return; }
    if (!state || initialized.current || pending.current || providers.some(item => !availability.states[item.id] || availability.states[item.id] === 'checking')) return;
    initialized.current = true;
    setConnectionChoices({});
    setSelected((initialProvider ? [initialProvider] : state.preferred_providers).filter(canSelect));
    setRetryOperations({});
    setError('');
  }, [open, state, initialProvider, providers, availability.states, canSelect]);

  useEffect(() => {
    if (!open || pending.current) return;
    setSelected(current => {
      const next = current.filter(id => !availability.states[id] || availability.states[id] === 'checking' || canSelect(id));
      return next.length === current.length ? current : next;
    });
  }, [open, availability.states, canSelect]);

  const refresh = async () => {
    if (lock.current) return;
    setLoading(true);
    try {
      for (const target of state?.targets ?? []) {
        if (target.operation?.actions.includes('check')) await WorkflowSessionApi().recoverPublication(target.operation.operation_id, { action: 'check' }, { silentError: true });
      }
      await Promise.all([load(), availability.refresh()]);
      setError('');
    } catch { setError(t('chat.publication.loadFailed')); }
    finally { setLoading(false); }
  };
  const submit = async () => {
    if (lock.current || readOnly || !selected.length) return;
    lock.current = true; setSubmitting(true); setError(''); setAuthorizationProvider(undefined);
    try {
      if (!pending.current) {
        // Keep the confirmed account stable while saving the document and reloading targets.
        const accountsToPublish = Object.fromEntries(selected.map(provider => [provider, accountFor(provider, state?.targets.find(item => item.provider === provider))]));
        for (const provider of selected) {
          if (!canSelect(provider)) throw new Error(providerChoices[provider]?.reason || t('chat.publication.loadFailed'));
          if (provider !== 'obsidian' && isAuthorized(provider) && !accountsToPublish[provider]) {
            throw new Error(t('chat.publication.chooseAccountRequired', { platform: label(provider) }));
          }
        }
        const baseline = await beforePublish(selected);
        if (!baseline) return;
        const current = (await WorkflowSessionApi().getPublicationTargets(baseline.id, { silentError: true })).data.data;
        setState(current);
        const targets = selected.map(provider => {
          const existing = current.targets.find(item => item.provider === provider);
          if (existing?.operation && blocked.has(existing.operation.status)) throw new Error(t('chat.publication.targetBusy'));
          const account = accountsToPublish[provider];
          if (provider !== 'obsidian' && (!account || availability.states[provider] !== 'ready')) {
            setAuthorizationProvider(provider);
            throw new Error(`${label(provider)}: ${t('chat.publication.accountRequired')}`);
          }
          if (!existing?.bound && provider === 'obsidian') throw new Error(t('chat.publication.obsidianUnsupported'));
          if (!existing?.bound && provider === 'github' && !existing?.parent_uri) throw new Error(t('chat.publication.githubTargetRequired'));
          return { provider, connection_id: account?.connection_id, parent_uri: existing?.bound ? undefined : existing?.parent_uri, retry_operation_id: retryOperations[provider] };
        });
        if (targets.some(input => input.retry_operation_id && current.targets.find(item => item.provider === input.provider)?.operation_hash !== current.current_hash)) {
          setRetryOperations({});
          setSelected(current.preferred_providers.filter(canSelect));
          throw new Error(t('chat.publication.changedRetry'));
        }
        pending.current = { id: baseline.id, body: { base_revision: baseline.revision, base_draft_version: baseline.draft, idempotency_key: crypto.randomUUID(), targets } };
      }
      const attempt = pending.current;
      const response = await WorkflowSessionApi().createPublicationGroup(attempt.id, attempt.body, { silentError: true });
      setState(response.data.data); pending.current = undefined; setUncertain(false);
      onClose();
    } catch (failure) {
      const code = (failure as { response?: { data?: { data?: { code?: string } } } })?.response?.data?.data?.code;
      // A lost acceptance response is replayed with the exact same key and body.
      // Do not rebuild it from a newer draft or a different platform selection.
      if (code) { pending.current = undefined; setUncertain(false); }
      else if (pending.current) setUncertain(true);
      setError(code ? documentPublicationErrorMessage(code) : pending.current ? t('chat.publication.acceptanceUnknown') : failure instanceof Error ? failure.message : t('chat.publication.loadFailed'));
    } finally { lock.current = false; setSubmitting(false); }
  };

  const retryFailed = () => {
    if (!state) return;
    const failed = state.targets.filter(item => item.operation?.status === 'failed_no_write');
    setSelected(failed.map(item => item.provider).filter(canSelect));
    setRetryOperations(Object.fromEntries(failed.map(item => [item.provider, item.operation!.operation_id])));
  };
  const results = state?.targets.filter(item => item.operation || item.bound) ?? [];
  const selectionReady = selected.every(provider => canSelect(provider)
    && (provider === 'obsidian' || !!accountFor(provider, state?.targets.find(item => item.provider === provider))));
  const accountSelectors = selected.filter(provider => {
    if (provider === 'obsidian') return false;
    const target = state?.targets.find(item => item.provider === provider);
    if (target?.bound && target.connection_id) return false;
    const accounts = availability.accounts?.[provider] ?? [];
    return accounts.length > 1 || (!!connectionChoices[provider] && !accounts.some(account => account.connection_id === connectionChoices[provider]));
  });
  const statusLabel = (status: string) => t(`chat.publication.status.${status}`, { defaultValue: t('chat.publication.status.outcome_unknown') });
  const closeResultsOnEscape = (event: KeyboardEvent<HTMLElement>) => {
    if (event.key !== 'Escape') return;
    event.preventDefault();
    event.stopPropagation();
    setResultsOpen(false);
    resultsButton.current?.focus();
  };

  const resultContent = <section className='document-publication-results' id={resultsId} ref={resultsPanel} role='dialog' tabIndex={-1}
    aria-label={t('chat.publication.results')} aria-live='polite' onKeyDown={closeResultsOnEscape}
    onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget) && event.relatedTarget !== resultsButton.current) setResultsOpen(false); }}>
      <div className='document-publication-results-header'>
        <strong>{t('chat.publication.results')}</strong>
        <Button type='text' size='small' aria-label={t('common.refresh')} title={t('common.refresh')} icon={<ReloadOutlined aria-hidden='true' />} loading={loading} onClick={() => void refresh()} />
      </div>
      <ul className='document-publication-results-list'>
      {results.map(item => {
        const status = item.operation?.status;
        const statusColor = status === 'succeeded' ? 'success' : status === 'failed_no_write' ? 'error'
          : status && ['outcome_unknown', 'local_conflict', 'local_persist_failed'].includes(status) ? 'warning'
            : status && running.has(status) ? 'processing' : 'default';
        const targetUrl = documentPublicationTargetUrl({ uri: item.operation?.target_url || item.target_url }, item.provider);
        return <li className='document-publication-result' key={item.provider}>
        <div className='document-publication-result-main'>
          <span className='document-publication-result-icon' aria-hidden='true'><WriterProviderIcon provider={item.provider} /></span>
          <div className='document-publication-result-summary'>
            <strong>{label(item.provider)}</strong>
            <Tag className='document-publication-result-status' bordered={false} color={statusColor} icon={status === 'succeeded' ? <CheckCircleOutlined aria-hidden='true' /> : undefined}>
              {status === 'succeeded' && item.provider === 'github' && (item.operation?.target_url || item.target_url)?.includes('/pull/') ? t('chat.publication.pullRequestCreated') : status ? statusLabel(status) : t('chat.publication.bound')}
            </Tag>
            {item.last_success_hash && (dirty || item.last_success_hash !== state?.current_hash) && <Tag bordered={false} color='warning'>{t('chat.publication.newEdits')}</Tag>}
          </div>
        </div>
        <div className='document-publication-result-actions'>
        {targetUrl && <a className='document-publication-open' href={targetUrl} target='_blank' rel='noopener noreferrer' aria-label={`${label(item.provider)} · ${t('chat.writerIR.openCloudDocument')}`} title={`${label(item.provider)} · ${t('chat.writerIR.openCloudDocument')}`}><ExportOutlined aria-hidden='true' /></a>}
        {item.operation?.actions.includes('cancel') && !readOnly && <Button size='small' onClick={async () => {
          if (lock.current) return; lock.current = true;
          try { await WorkflowSessionApi().cancelPublication(item.operation!.operation_id, { silentError: true }); await load(); }
          catch { setError(t('chat.publication.loadFailed')); } finally { lock.current = false; }
        }}>{t('chat.publication.cancelTarget')}</Button>}
        {item.operation?.actions.includes('retry_local') && !readOnly && <Button size='small' onClick={async () => {
          if (lock.current) return; lock.current = true;
          try { await WorkflowSessionApi().retryPublicationLocal(item.operation!.operation_id, { silentError: true }); await load(); }
          catch { setError(t('chat.publication.loadFailed')); } finally { lock.current = false; }
        }}>{t('chat.publication.saveResult')}</Button>}
        </div>
        {item.provider === 'wechat' && status === 'succeeded' && !item.operation?.target_url && <p className='document-publication-result-note'>{t('chat.writerIR.wechatDraftLinkUnavailable')}</p>}
        {item.operation?.error_code && <p className='document-publication-error'>{documentPublicationErrorMessage(item.operation.error_code, item.provider)}</p>}
        {status === 'outcome_unknown' && <p className='document-publication-error'>{t('chat.publication.unknownHelp')}</p>}
      </li>;
      })}
      </ul>
    </section>;

  return <>
    {results.length > 0 && <div className='document-publication-results-trigger'>
      <Popover content={resultContent} trigger={['hover', 'click']} placement='topRight' arrow={false}
        overlayClassName='document-publication-results-popover' open={resultsOpen && !open} onOpenChange={setResultsOpen}
        mouseEnterDelay={0.15} mouseLeaveDelay={0.2} destroyOnHidden
        afterOpenChange={visible => {
          if (visible && focusResults.current) resultsPanel.current?.focus();
          focusResults.current = false;
        }}>
        <Button ref={resultsButton} type='text' size='small' icon={<CloudOutlined aria-hidden='true' />}
          aria-haspopup='dialog' aria-expanded={resultsOpen && !open} aria-controls={resultsOpen && !open ? resultsId : undefined}
          onKeyDown={event => {
            closeResultsOnEscape(event);
            if (['Enter', ' ', 'ArrowDown'].includes(event.key)) {
              event.preventDefault();
              if (resultsOpen) resultsPanel.current?.focus();
              else { focusResults.current = true; setResultsOpen(true); }
            }
          }}>{t('chat.publication.resultsButton')}</Button>
      </Popover>
    </div>}
    {!open && error && <Alert type='error' message={error} action={<Button size='small' onClick={() => void refresh()}>{t('common.retry')}</Button>} />}
    <Modal className='document-publication-modal' open={open} width={760} centered title={<div className='document-publication-heading'><CloudUploadOutlined aria-hidden='true' />{t('chat.publication.title')}</div>} onCancel={() => { if (!lock.current) onClose(); }}
      maskClosable={!submitting} closable={!submitting} cancelButtonProps={{ disabled: submitting }}
      onOk={() => void submit()} confirmLoading={submitting} okButtonProps={{ disabled: readOnly || !selected.length || !state || loading || (!uncertain && !selectionReady) }}
      footer={(buttons: ReactNode) => <><span className='document-publication-selection-count'>{t('chat.publication.selectedCount', { count: selected.length })}</span><div className='document-publication-footer-actions'>{buttons}</div></>}
      okText={uncertain ? t('chat.publication.confirmAgain') : selected.length ? t('chat.publication.confirm', { platforms: selected.map(label).join('、') }) : t('chat.publication.selectPlatforms')}>
      <p className='document-publication-description'>{t('chat.publication.latestOnly')}</p>
      <div className='document-publication-scroll'>
        {error && <Alert type='error' message={error} showIcon action={authorizationProvider && <a href={getCloudDocumentsUrl(authorizationProvider === 'feishu' || authorizationProvider === 'googledrive' || authorizationProvider === 'wechat' ? authorizationProvider : undefined)} target='_blank' rel='noopener noreferrer'>{t('chat.publication.configure')}</a>} />}
        <div className='document-publication-section-heading'>
          <div><strong>{t('chat.publication.platforms')}</strong><span>{t('chat.publication.multipleAllowed')}</span></div>
          <Button type='text' size='small' icon={<ReloadOutlined aria-hidden='true' />} onClick={() => void refresh()} loading={loading} disabled={submitting || uncertain}>{t('common.refresh')}</Button>
        </div>
        {!state ? <div className='document-publication-loading'><Spin /></div> : <>
          <div className='document-publication-platforms' role='group' aria-label={t('chat.publication.platforms')}>
            {providers.map(provider => {
              const target = state.targets.find(item => item.provider === provider.id);
              const isObsidian = provider.id === 'obsidian';
              const unsupported = isObsidian && !target?.bound;
              const busy = !!target?.operation && blocked.has(target.operation.status);
              const ready = isAuthorized(provider.id);
              const authorizationNeeded = provider.id !== 'obsidian' && !ready && ['authorize', 'chat-disabled', 'ready'].includes(availability.states[provider.id]);
              const status = providerChoices[provider.id]?.reason || t(isObsidian ? 'modelProvider.cloudDocuments.obsidianBoundStatus' : 'chat.publication.authorized');
              return <div className={`document-publication-platform-card${authorizationNeeded ? ' has-authorization' : isObsidian ? ' has-guide' : ''}`} key={provider.id}>
              <Checkbox className='document-publication-platform' aria-label={label(provider.id)} title={unsupported ? t('chat.publication.obsidianUnsupported') : undefined}
                checked={selected.includes(provider.id)} disabled={submitting || uncertain || !canSelect(provider.id)}
                onChange={(event: CheckboxChangeEvent) => {
                  if (!canSelect(provider.id)) return;
                  initialized.current = true;
                  setSelected(current => event.target.checked ? [...current, provider.id] : current.filter(id => id !== provider.id));
                }}>
                <span className='document-publication-platform-content'>
                  <span className='document-publication-icon' aria-hidden='true'><WriterProviderIcon provider={provider.id} /></span>
                  <span className='document-publication-platform-copy'>
                    <strong>{label(provider.id)}</strong>
                    <small className={ready && !unsupported && !busy ? 'is-ready' : ''}>{status}</small>
                    {isObsidian && <small title={t('modelProvider.cloudDocuments.obsidianEnvironmentHint')}>{t('modelProvider.cloudDocuments.obsidianEnvironmentSummary')}</small>}
                  </span>
                </span>
              </Checkbox>
              {isObsidian && <a className='document-publication-authorize' aria-label={t('modelProvider.cloudDocuments.obsidianSetupGuideAction')}
                href={CLOUD_DOCUMENTS_OBSIDIAN_SETUP_PATH} target='_blank' rel='noopener noreferrer'>{t('modelProvider.cloudDocuments.obsidianSetupGuideShort')}</a>}
              {authorizationNeeded && !submitting && !uncertain && <a className='document-publication-authorize' aria-label={t('chat.publication.authorizePlatform', { platform: label(provider.id) })}
                href={getCloudDocumentsUrl(provider.id === 'feishu' || provider.id === 'googledrive' || provider.id === 'wechat' ? provider.id : undefined)} target='_blank' rel='noopener noreferrer'>{t('chat.publication.authorize')}</a>}
              </div>;
            })}
          </div>
          {accountSelectors.length > 0 && <div className='document-publication-accounts'>
            {accountSelectors.map(provider => {
              const target = state.targets.find(item => item.provider === provider);
              const accounts = availability.accounts?.[provider] ?? [];
              const account = accountFor(provider, target);
              const checking = availability.states[provider] === 'checking';
              const unavailable = !checking && !!connectionChoices[provider] && !account;
              const fieldId = `${resultsId}-${provider}-account`;
              return <div className='document-publication-account' key={provider}>
                <label htmlFor={fieldId}>{t('chat.publication.accountLabel', { platform: label(provider) })}</label>
                <Select id={fieldId} aria-label={t('chat.publication.accountLabel', { platform: label(provider) })}
                  aria-describedby={unavailable ? `${fieldId}-error` : undefined} aria-invalid={unavailable}
                  placeholder={t('chat.publication.chooseAccount')} value={account?.connection_id}
                  loading={checking} disabled={submitting || uncertain || checking || loading} status={unavailable ? 'error' : undefined}
                  options={accounts.map(item => {
                    const name = item.display_name || item.provider_account_id || item.connection_id;
                    const repeated = accounts.some(other => other.connection_id !== item.connection_id && (other.display_name || other.provider_account_id || other.connection_id) === name);
                    return { value: item.connection_id, label: repeated ? `${name} · ${item.connection_id.slice(-8)}` : name };
                  })}
                  onChange={(connectionId: string) => {
                    setConnectionChoices(current => ({ ...current, [provider]: connectionId }));
                    setRetryOperations(current => { const next = { ...current }; delete next[provider]; return next; });
                  }} />
                {unavailable && <span id={`${fieldId}-error`} className='document-publication-account-error'>{t('chat.publication.accountUnavailable')}</span>}
              </div>;
            })}
            <p>{t('chat.publication.rememberAccount')}</p>
          </div>}
          {state.targets.some(item => item.operation?.status === 'failed_no_write') && <div className='document-publication-tools'><Button onClick={retryFailed} disabled={submitting || uncertain}>{t('chat.publication.retryFailed')}</Button></div>}
        </>}
      </div>
    </Modal>
  </>;
}
