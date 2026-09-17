import { getLocalizedErrorMessage } from '@/components/request';
import { useCallback, useEffect, useState, type ChangeEvent } from 'react';
import {
  Button,
  Empty,
  Input,
  message,
  Modal,
  QRCode,
  Space,
  Spin,
  Tag,
  Typography,
} from 'antd';
import {
  CheckCircleFilled,
  CloseCircleFilled,
  LinkOutlined,
  LockOutlined,
  MobileOutlined,
  QrcodeOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  WechatOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';
import { useTranslation } from 'react-i18next';
import { useSearchParams } from 'react-router-dom';
import { getAccountDetail, getReferences, providers, type AccountDetail, type Reference } from '@/modules/notifications/api';
import ChannelBrand from '@/modules/notifications/ChannelBrand';
import '@/modules/notifications/index.scss';

import type {
  ChannelAccount,
  ChannelProvider,
  ConnectionSession,
} from '../api';
import {
  disconnectChannelAccount,
  listChannelAccounts,
} from '../api';
import { useChannelConnection } from '../hooks/useChannelConnection';
import './channelConnectionPage.scss';

const { Paragraph, Text, Title } = Typography;

function ChannelIcon({ provider }: { provider: ChannelProvider }) {
  return provider === 'wechat'
    ? <WechatOutlined />
    : (
      <img
        className="feishu-official-icon"
        src="/feishu-official.svg"
        alt=""
        aria-hidden="true"
      />
    );
}

function formatTime(value: string | null | undefined): string {
  if (!value) {
    return '-';
  }
  const parsed = dayjs(value);
  return parsed.isValid() ? parsed.format('YYYY-MM-DD HH:mm:ss') : value;
}

function statusColor(status: string): string {
  switch (status) {
    case 'connected':
    case 'running':
      return 'success';
    case 'waiting_scan':
    case 'scanned':
    case 'confirming':
    case 'preparing':
    case 'starting':
      return 'processing';
    case 'verification_required':
    case 'degraded':
      return 'warning';
    case 'failed':
    case 'expired':
    case 'canceled':
    case 'stopped':
    case 'unsupported':
      return 'error';
    default:
      return 'default';
  }
}

function canAct(
  session: ConnectionSession | null,
  action: ConnectionSession['allowed_actions'][number],
): boolean {
  return Boolean(session?.allowed_actions?.includes(action));
}

function isActiveScan(session: ConnectionSession | null): boolean {
  if (!session) {
    return false;
  }
  return !['connected', 'expired', 'canceled', 'failed'].includes(session.status);
}

function currentStep(session: ConnectionSession | null): number {
  if (!session) return 1;
  if (session.status === 'connected') return 3;
  if (['scanned', 'verification_required', 'confirming'].includes(session.status)) return 3;
  return 2;
}

function renderSessionVisual(
  session: ConnectionSession,
  labels: { preparing: string; connected: string; failed: string },
) {
  if (session.status === 'connected') {
    return (
      <div className="wechat-connection-result is-success" aria-label={labels.connected}>
        <CheckCircleFilled />
        <span>{labels.connected}</span>
      </div>
    );
  }
  if (['failed', 'expired', 'canceled'].includes(session.status)) {
    return (
      <div className="wechat-connection-result is-error" aria-label={labels.failed}>
        <CloseCircleFilled />
        <span>{labels.failed}</span>
      </div>
    );
  }
  if (session.qr?.payload) {
    return <QRCode value={session.qr.payload} size={220} status="active" bordered={false} />;
  }
  return (
    <div className="wechat-connection-qr-placeholder">
      <Spin />
      <span>{labels.preparing}</span>
    </div>
  );
}

interface ChannelConnectionPageProps {
  provider: ChannelProvider;
  accountId?: string;
  onConnected?: () => void;
}

function ChannelConnectionPage({ provider, accountId, onConnected }: ChannelConnectionPageProps) {
  const translationKey = `channelGateway.${provider}`;
  const copy = (name: string) => `${translationKey}.${name}`;
  const channelIcon = <ChannelIcon provider={provider} />;
  const {
    t,
    accounts,
    session,
    sessionStarting,
    actionLoading,
    challengeValue,
    setChallengeValue,
    startScan,
    cancelScan,
    refreshQr,
    submitChallenge,
    closeSessionPanel,
  } = useChannelConnection(provider);

  const [botId, setBotId] = useState('');
  const [secret, setSecret] = useState('');
  useEffect(() => { if (session?.status === 'connected') onConnected?.(); }, [session?.status, onConnected]);
  const step = currentStep(session);
  const hasAccounts = accounts.length > 0;
  const activeScan = isActiveScan(session);
  const connectWorkspaceId = `${provider}-connect-workspace`;
  const connectTitleId = `${provider}-connect-title`;

  const beginScan = () => {
    void startScan({ accountId });
  };

  const connectWorkspace = (
    <section
      id={connectWorkspaceId}
      className="wechat-connect-workspace"
      aria-labelledby={connectTitleId}
    >
      <div className="wechat-connect-workspace-head">
        <div>
          <Text className="wechat-section-kicker">{t(copy('quickConnect'))}</Text>
          <Title id={connectTitleId} level={3}>
            {hasAccounts
              ? t(copy('newConnectionTitle'))
              : t(copy('guideTitle'))}
          </Title>
          <Paragraph>
            {hasAccounts
              ? t(copy('newConnectionHint'))
              : t(copy('guideHint'))}
          </Paragraph>
        </div>
      </div>

      <div className="wechat-connect-workspace-body">
        <div className="wechat-connect-guide">
          <ol className="wechat-connect-steps">
            <li className={step >= 1 ? 'is-active' : ''}>
              <span className="wechat-step-index">1</span>
              <span className="wechat-step-icon"><MobileOutlined /></span>
              <div>
                <strong>{t(copy('stepOpenTitle'))}</strong>
                <p>{t(copy('stepOpenHint'))}</p>
              </div>
            </li>
            <li className={step >= 2 ? 'is-active' : ''}>
              <span className="wechat-step-index">2</span>
              <span className="wechat-step-icon"><QrcodeOutlined /></span>
              <div>
                <strong>{t(copy('stepScanTitle'))}</strong>
                <p>{t(copy('stepScanHint'))}</p>
              </div>
            </li>
            <li className={step >= 3 ? 'is-active' : ''}>
              <span className="wechat-step-index">3</span>
              <span className="wechat-step-icon"><SafetyCertificateOutlined /></span>
              <div>
                <strong>{t(copy('stepConfirmTitle'))}</strong>
                <p>{t(copy('stepConfirmHint'))}</p>
              </div>
            </li>
          </ol>

          <div className="wechat-security-note">
            <LockOutlined />
            <span>{t(copy('securityHint'))}</span>
          </div>
        </div>

        <div className={`wechat-scan-stage ${session ? 'has-session' : 'is-idle'}`}>
          {session ? (
            <>
              <div
                className="wechat-scan-status"
                role={session.error ? 'alert' : 'status'}
                aria-live="polite"
              >
                <span
                  className={`wechat-status-dot status-${statusColor(session.status)}`}
                  aria-hidden="true"
                />
                <div>
                  <Text strong>
                    {t(copy(`sessionStatusMap.${session.status}`), {
                      defaultValue: session.status,
                    })}
                  </Text>
                  <Paragraph>{session.message}</Paragraph>
                  {session.error ? <Paragraph type="danger">{session.error.message}</Paragraph> : null}
                </div>
              </div>

              <div className="wechat-connection-qr-wrap">
                {renderSessionVisual(session, {
                  preparing: t(copy('preparingQr')),
                  connected: t(copy('connectSuccessVisual')),
                  failed: t(copy('connectFailedVisual')),
                })}
                {session.qr?.expires_at && activeScan ? (
                  <Text type="secondary">
                    {t(copy('qrExpiresAt'), { time: formatTime(session.qr.expires_at) })}
                  </Text>
                ) : null}
              </div>

              {session.status === 'verification_required' || canAct(session, 'submit_challenge') ? (
                <div className="wechat-connection-challenge">
                  <Text strong>
                    {session.challenge?.prompt || t(copy('challengePrompt'))}
                  </Text>
                  <Space.Compact className="wechat-challenge-input">
                    <Input
                      value={challengeValue}
                      maxLength={12}
                      inputMode="numeric"
                      aria-label={t(copy('challengePrompt'))}
                      placeholder={t(copy('challengePlaceholder'))}
                      onChange={(event: ChangeEvent<HTMLInputElement>) => setChallengeValue(event.target.value)}
                      onPressEnter={() => void submitChallenge()}
                    />
                    <Button
                      type="primary"
                      loading={actionLoading}
                      onClick={() => void submitChallenge()}
                    >
                      {t(copy('submitChallenge'))}
                    </Button>
                  </Space.Compact>
                </div>
              ) : null}

              <Space wrap className="wechat-connection-scan-actions">
                {canAct(session, 'refresh') ? (
                  <Button icon={<ReloadOutlined />} loading={actionLoading} onClick={() => void refreshQr()}>
                    {t(copy('refreshQr'))}
                  </Button>
                ) : null}
                {canAct(session, 'cancel') ? (
                  <Button loading={actionLoading} onClick={() => void cancelScan()}>
                    {t(copy('cancelScan'))}
                  </Button>
                ) : null}
                {!activeScan ? (
                  <Button onClick={closeSessionPanel}>
                    {session.status === 'connected'
                      ? t(copy('addAnotherAccount'))
                      : t(copy('closePanel'))}
                  </Button>
                ) : null}
              </Space>
            </>
          ) : (
            <div className="wechat-scan-empty">
              <span className="wechat-scan-empty-icon" aria-hidden="true">{channelIcon}</span>
              <div>
                <Title level={4}>{t(copy('readyTitle'))}</Title>
                <Paragraph>{t(copy('readyHint'))}</Paragraph>
              </div>
              <Button
                type="primary"
                size="large"
                icon={<QrcodeOutlined />}
                loading={sessionStarting}
                onClick={beginScan}
              >
                {t(copy('startScan'))}
              </Button>
              <Text type="secondary">{t(copy('estimatedTime'))}</Text>
            </div>
          )}
        </div>
      </div>
    </section>
  );

  if (provider === 'wecom') return <section className="notification-wecom-connect">
    <ChannelBrand channel="wecom" />
    <h3>{t('notifications.' + (accountId ? 'reconnect' : 'newAccount'))}</h3>
    <p>{t('notifications.credentialsHint')}</p>
    <label>{t('notifications.botId')}<Input aria-label="BotID" value={botId} maxLength={256} disabled={sessionStarting} onChange={(e: ChangeEvent<HTMLInputElement>) => setBotId(e.target.value)} autoComplete="off" /></label>
    <label>{t('notifications.secret')}<Input.Password aria-label="Secret" value={secret} maxLength={4096} disabled={sessionStarting} onChange={(e: ChangeEvent<HTMLInputElement>) => setSecret(e.target.value)} autoComplete="new-password" /></label>
    <Button type="primary" loading={sessionStarting} onClick={async () => {
      if (!botId.trim() || /[\s\x00-\x1f]/.test(botId.trim()) || !secret.trim()) { message.warning(t('notifications.credentialsRequired')); return; }
      await startScan({ accountId, credentials: { bot_id: botId.trim(), secret } });
      setSecret('');
    }}>{t('notifications.connectAction')}</Button>
    {session && <Tag color={statusColor(session.status)}>{session.status === 'connected' ? t('notifications.connected') : session.status === 'failed' ? getLocalizedErrorMessage({ code: session.error?.code || 'WECOM_AUTH_FAILED' }) : t('notifications.loading')}</Tag>}
  </section>;

  return (
    <div className={`wechat-connection-page is-${provider} is-embedded`}>
      <main className="wechat-connection-content">
        {connectWorkspace}
      </main>
    </div>
  );
}

function AccountDisclosure({ account, onReconnect, onChanged }: { account: ChannelAccount; onReconnect: () => void; onChanged: () => void }) {
  const { t } = useTranslation();
  const [detail, setDetail] = useState<AccountDetail>();
  const [refs, setRefs] = useState<Reference[]>([]);
  const [cursor, setCursor] = useState('');
  const [error, setError] = useState(false);
  const [busy, setBusy] = useState(false);
  const load = async () => {
    setBusy(true); setError(false);
    try {
      const [d, r] = await Promise.all([getAccountDetail(account.id), getReferences(account.id)]);
      setDetail(d); setRefs(r.items); setCursor(r.next_cursor);
    } catch { setError(true); } finally { setBusy(false); }
  };
  const disconnect = async () => {
    setBusy(true);
    try {
      // Re-read impact immediately before asking for confirmation; an unavailable dependency must not look like zero references.
      const r = await getReferences(account.id);
      Modal.confirm({ title: t('notifications.disconnectTitle'), content: <><p>{t('notifications.disconnectHint')}</p><p>{t('notifications.referenceCount', { count: r.total })}</p>{r.items.map(item => <p key={item.kind + item.id}>{item.name}</p>)}</>, okButtonProps: { danger: true }, okText: t('notifications.disconnect'), cancelText: t('notifications.cancel'), onOk: async () => { await disconnectChannelAccount(account.id); onChanged(); } });
    } catch { setError(true); } finally { setBusy(false); }
  };
  return <details className="notification-account" onToggle={e => { if (e.currentTarget.open && !detail && !busy) void load(); }}>
    <summary><ChannelBrand channel={account.provider as ChannelProvider} avatar={account.avatar_url} /><strong>{account.label}</strong><Tag color={account.status === 'connected' ? 'success' : 'default'}>{t('notifications.' + (account.status === 'connected' ? 'connected' : 'disconnected'))}</Tag></summary>
    {busy && <Spin size="small" />}
    {error && <p role="alert">{t('notifications.loadFailed')} <Button onClick={() => void load()}>{t('notifications.retry')}</Button></p>}
    {detail && <div className="notification-account-details">
      <div><small>{t('notifications.primary')}</small><p>{detail.primary_recipient?.label || t('notifications.noPrimary')}</p></div>
      <div><small>{t('notifications.runtime')}</small><p>{t(`channelGateway.feishu.runtimeStatusMap.${detail.runtime_status}`)}</p></div>
      <div><small>{t('notifications.connectedAt')}</small><p>{formatTime(detail.connected_at)}</p></div>
      <div><small>{t('notifications.lastMessageAt')}</small><p>{formatTime(detail.last_message_at)}</p></div>
      <div className="notification-wide"><small>{t('notifications.references')} · {detail.notification_reference_count}</small><p>{refs.map(r => r.name).join('、') || t('notifications.noReferences')}</p>
        {cursor && <Button disabled={busy} onClick={async () => { setBusy(true); try { const r = await getReferences(account.id, cursor); setRefs(old => [...old, ...r.items]); setCursor(r.next_cursor); } catch { setError(true); } finally { setBusy(false); } }}>{t('notifications.loadMore')}</Button>}
      </div>
    </div>}
    <footer><Button disabled={busy} danger={account.status === 'connected'} onClick={account.status === 'connected' ? () => void disconnect() : onReconnect}>{t('notifications.' + (account.status === 'connected' ? 'disconnect' : 'reconnect'))}</Button><small>{account.id}</small></footer>
  </details>;
}

export function TerminalConnectionPage({ initialProvider, embedded = false }: { initialProvider?: ChannelProvider; embedded?: boolean } = {}) {
  const { t } = useTranslation();
  const [searchParams, setSearchParams] = useSearchParams();
  const fromURL = searchParams.get('provider') as ChannelProvider;
  const [provider, setProvider] = useState<ChannelProvider>(initialProvider || (providers.includes(fromURL) ? fromURL : 'feishu'));
  const [accounts, setAccounts] = useState<ChannelAccount[]>([]);
  const [error, setError] = useState(false);
  const [loading, setLoading] = useState(true);
  const [reconnectId, setReconnectId] = useState<string>();
  const [refresh, setRefresh] = useState(0);
  const onChanged = useCallback(() => setRefresh(n => n + 1), []);
  useEffect(() => {
    let active = true;
    setLoading(true); setError(false);
    Promise.all(providers.map(listChannelAccounts)).then(results => { if (active) setAccounts(results.flatMap(r => r.items)); }).catch(() => { if (active) setError(true); }).finally(() => { if (active) setLoading(false); });
    return () => { active = false; };
  }, [refresh]);
  const select = (p: ChannelProvider) => {
    setProvider(p); setReconnectId(undefined);
    if (!embedded) { const params = new URLSearchParams(searchParams); params.set('provider', p); setSearchParams(params, { replace: true }); }
  };
  return <div className="notification-connections">
    <header className="notification-heading"><LinkOutlined /><div><h2>{t('notifications.connectTitle')}</h2><p>{t('notifications.connectHint')}</p></div><Button loading={loading} onClick={onChanged} icon={<ReloadOutlined />}>{t('notifications.refresh')}</Button></header>
    {error && <p role="alert">{t('notifications.loadFailed')}</p>}
    <nav className="notification-provider-tabs" aria-label={t('notifications.channels')}>{providers.map(p => {
      const count = accounts.filter(a => a.provider === p && a.status === 'connected').length;
      return <button key={p} type="button" aria-pressed={provider === p} onClick={() => select(p)}><ChannelBrand channel={p} /><span><strong>{t('notifications.' + p)}</strong>{count > 0 && <small>{t('notifications.enabledCount', { count })}</small>}</span><small>{t('notifications.' + (count ? 'connected' : 'notConnected'))}</small></button>;
    })}</nav>
    <div className="notification-connection-columns"><section><h3>{t('notifications.accounts')}</h3><p>{t('notifications.accountHint')}</p>
      {loading && <Spin />}
      {!loading && !error && !accounts.some(a => a.provider === provider) && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description={t('notifications.noAccounts')} />}
      {accounts.filter(a => a.provider === provider).map(account => <AccountDisclosure key={account.id + account.updated_at} account={account} onChanged={onChanged} onReconnect={() => setReconnectId(account.id)} />)}
    </section><section><h3>{t('notifications.' + (reconnectId ? 'reconnect' : 'newAccount'))}</h3><p>{t('notifications.newAccountHint')}</p>
      {reconnectId && <Button onClick={() => setReconnectId(undefined)}>{t('notifications.newAccount')}</Button>}
      <ChannelConnectionPage key={provider + (reconnectId || '')} provider={provider} accountId={reconnectId} onConnected={onChanged} />
    </section></div>
  </div>;
}
