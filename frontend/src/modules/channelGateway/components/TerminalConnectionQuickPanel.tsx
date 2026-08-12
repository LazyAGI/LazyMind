import { useEffect, useRef, useState } from 'react';
import {
  Button,
  Input,
  QRCode,
  Spin,
  Typography,
} from 'antd';
import {
  CheckCircleFilled,
  LinkOutlined,
  MobileOutlined,
  QrcodeOutlined,
  ReloadOutlined,
  SafetyCertificateOutlined,
  SettingOutlined,
  WechatOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';
import { useTranslation } from 'react-i18next';

import {
  listChannelAccounts,
  type ChannelProvider,
  type ConnectionSession,
} from '../api';
import { useChannelConnection } from '../hooks/useChannelConnection';
import '../pages/channelConnectionPage.scss';

const { Text } = Typography;

function ProviderIcon({ provider }: { provider: ChannelProvider }) {
  if (provider === 'wechat') {
    return <WechatOutlined />;
  }
  return (
    <img
      className="feishu-official-icon"
      src="/feishu-official.svg"
      alt=""
      aria-hidden="true"
    />
  );
}

function currentStep(session: ConnectionSession | null) {
  if (!session) return 1;
  if (
    session.status === 'connected' ||
    ['scanned', 'verification_required', 'confirming'].includes(session.status)
  ) {
    return 3;
  }
  return 2;
}

function formatExpiry(value?: string | null) {
  if (!value) return '';
  const parsed = dayjs(value);
  return parsed.isValid() ? parsed.format('HH:mm:ss') : value;
}

interface ProviderConnectionProps {
  provider: ChannelProvider;
}

function ProviderConnection({ provider }: ProviderConnectionProps) {
  const {
    t,
    session,
    sessionStarting,
    actionLoading,
    challengeValue,
    setChallengeValue,
    startScan,
    refreshQr,
    submitChallenge,
  } = useChannelConnection(provider);
  const copy = (name: string) => `channelGateway.${provider}.${name}`;
  const step = currentStep(session);
  const isConnected = session?.status === 'connected';
  const isFailed = Boolean(
    session && ['failed', 'expired', 'canceled'].includes(session.status),
  );
  const canRefresh = Boolean(session?.allowed_actions?.includes('refresh'));
  const autoStartRef = useRef(false);

  useEffect(() => {
    if (autoStartRef.current) {
      return;
    }
    autoStartRef.current = true;
    void startScan();
  }, [startScan]);

  return (
    <>
      <section className={`terminal-quick-provider is-${provider}`}>
        <span className="terminal-quick-provider-icon" aria-hidden="true">
          <ProviderIcon provider={provider} />
        </span>
        <div>
          <strong>{t(`channelGateway.terminal.${provider}Title`)}</strong>
          <small>{t(`channelGateway.terminal.${provider}Hint`)}</small>
        </div>
      </section>

      <p className="terminal-quick-description">
        {t(`channelGateway.terminal.${provider}QuickDescription`)}
      </p>

      <ol className="terminal-quick-steps">
        <li className={step >= 1 ? 'is-active' : ''}>
          <span className="terminal-quick-step-index">1</span>
          <MobileOutlined />
          <div>
            <strong>{t(copy('stepOpenTitle'))}</strong>
            <small>{t(copy('stepOpenHint'))}</small>
          </div>
        </li>
        <li className={step >= 2 ? 'is-active' : ''}>
          <span className="terminal-quick-step-index">2</span>
          <QrcodeOutlined />
          <div>
            <strong>{t(copy('stepScanTitle'))}</strong>
            <small>{t(copy('stepScanHint'))}</small>
          </div>
        </li>
        <li className={step >= 3 ? 'is-active' : ''}>
          <span className="terminal-quick-step-index">3</span>
          <SafetyCertificateOutlined />
          <div>
            <strong>{t(copy('stepConfirmTitle'))}</strong>
            <small>{t(copy('stepConfirmHint'))}</small>
          </div>
        </li>
      </ol>

      <section className="terminal-quick-scan" aria-live="polite">
        {session?.qr?.payload ? (
          <>
            <QRCode
              value={session.qr.payload}
              size={160}
              bordered={false}
              status="active"
            />
            <Text className="terminal-quick-status" type="secondary">
              <span aria-hidden="true" />
              {t(copy(`sessionStatusMap.${session.status}`), {
                defaultValue: session.status,
              })}
              {session.qr.expires_at
                ? ` · ${t('channelGateway.terminal.expiresAt', {
                    time: formatExpiry(session.qr.expires_at),
                  })}`
                : ''}
            </Text>
          </>
        ) : isConnected ? (
          <div className="terminal-quick-result is-success">
            <CheckCircleFilled />
            <strong>{t(copy('connectSuccessVisual'))}</strong>
          </div>
        ) : sessionStarting || (session && !isFailed) ? (
          <div className="terminal-quick-preparing">
            <Spin />
            <strong>{t(copy('preparingQr'))}</strong>
            <small>{session?.message || t(copy('readyHint'))}</small>
          </div>
        ) : (
          <div className="terminal-quick-empty">
            <span aria-hidden="true"><ProviderIcon provider={provider} /></span>
            <strong>{isFailed ? t(copy('connectFailedVisual')) : t(copy('readyTitle'))}</strong>
            <small>{session?.message || t(copy('readyHint'))}</small>
            <Button
              type="primary"
              icon={<QrcodeOutlined />}
              onClick={() => void startScan()}
            >
              {isFailed ? t('common.retry') : t(copy('startScan'))}
            </Button>
          </div>
        )}

        {session?.status === 'verification_required' ||
        session?.allowed_actions?.includes('submit_challenge') ? (
          <div className="terminal-quick-challenge">
            <Text strong>{session.challenge?.prompt || t(copy('challengePrompt'))}</Text>
            <Input
              value={challengeValue}
              maxLength={12}
              inputMode="numeric"
              placeholder={t(copy('challengePlaceholder'))}
              aria-label={t(copy('challengePrompt'))}
              onChange={(event) => setChallengeValue(event.target.value)}
              onPressEnter={() => void submitChallenge()}
              addonAfter={(
                <button type="button" onClick={() => void submitChallenge()}>
                  {t(copy('submitChallenge'))}
                </button>
              )}
            />
          </div>
        ) : null}

        <Text className="terminal-quick-security" type="secondary">
          {t(copy('securityHint'))}
        </Text>
      </section>

      <Button
        className="terminal-quick-refresh"
        icon={<ReloadOutlined />}
        loading={actionLoading}
        disabled={!canRefresh}
        onClick={() => void refreshQr()}
      >
        {t(copy('refreshQr'))}
      </Button>
    </>
  );
}

interface TerminalConnectionQuickPanelProps {
  onManage: () => void;
}

export default function TerminalConnectionQuickPanel({
  onManage,
}: TerminalConnectionQuickPanelProps) {
  const { t } = useTranslation();
  const [provider, setProvider] = useState<ChannelProvider>('wechat');
  const [connectedCount, setConnectedCount] = useState<number | null>(null);

  useEffect(() => {
    let active = true;
    void Promise.all([
      listChannelAccounts('wechat'),
      listChannelAccounts('feishu'),
    ]).then(([wechat, feishu]) => {
      if (active) {
        setConnectedCount(wechat.items.length + feishu.items.length);
      }
    }).catch(() => {
      if (active) setConnectedCount(0);
    });
    return () => {
      active = false;
    };
  }, []);

  return (
    <section
      className="terminal-quick-panel"
      role="dialog"
      aria-modal="false"
      aria-label={t('channelGateway.terminal.title')}
    >
      <header className="terminal-quick-header">
        <span className="terminal-quick-header-icon" aria-hidden="true">
          <LinkOutlined />
        </span>
        <div>
          <strong>{t('channelGateway.terminal.title')}</strong>
          <small>{t('channelGateway.terminal.quickSubtitle')}</small>
        </div>
        <span className="terminal-quick-count">
          {connectedCount == null
            ? <Spin size="small" />
            : t('channelGateway.terminal.connectedCount', { count: connectedCount })}
        </span>
      </header>

      <div className="terminal-quick-body">
        <div className="terminal-quick-tabs" role="tablist" aria-label={t('channelGateway.terminal.providerLabel')}>
          {(['wechat', 'feishu'] as ChannelProvider[]).map((item) => (
            <button
              key={item}
              type="button"
              role="tab"
              aria-selected={provider === item}
              className={provider === item ? 'is-active' : ''}
              onClick={() => setProvider(item)}
            >
              {t(`channelGateway.terminal.${item}Title`)}
            </button>
          ))}
        </div>

        <ProviderConnection key={provider} provider={provider} />
      </div>

      <footer className="terminal-quick-footer">
        <Button type="text" icon={<SettingOutlined />} onClick={onManage}>
          {t('channelGateway.terminal.manageSettings')}
        </Button>
      </footer>
    </section>
  );
}
