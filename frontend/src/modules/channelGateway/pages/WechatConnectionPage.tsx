import {
  Button,
  Empty,
  Input,
  QRCode,
  Space,
  Spin,
  Table,
  Tag,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  CheckCircleFilled,
  CloseCircleFilled,
  ReloadOutlined,
  WechatOutlined,
} from '@ant-design/icons';
import dayjs from 'dayjs';

import type { ChannelAccount, ConnectionSession } from '../api';
import { useWechatConnection } from '../hooks/useWechatConnection';
import './wechatConnectionPage.scss';

const { Paragraph, Text, Title } = Typography;

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

function canAct(session: ConnectionSession | null, action: string): boolean {
  return Boolean(session?.allowed_actions?.includes(action));
}

function isActiveScan(session: ConnectionSession | null): boolean {
  if (!session) {
    return false;
  }
  return ![
    'connected',
    'expired',
    'canceled',
    'failed',
  ].includes(session.status);
}

function renderSessionVisual(
  session: ConnectionSession,
  labels: {
    preparing: string;
    connected: string;
    failed: string;
  },
) {
  if (session.status === 'connected') {
    return (
      <div className="wechat-connection-result is-success" aria-label={labels.connected}>
        <CheckCircleFilled />
        <span>{labels.connected}</span>
      </div>
    );
  }
  if (
    session.status === 'failed' ||
    session.status === 'expired' ||
    session.status === 'canceled'
  ) {
    return (
      <div className="wechat-connection-result is-error" aria-label={labels.failed}>
        <CloseCircleFilled />
        <span>{labels.failed}</span>
      </div>
    );
  }
  if (session.qr?.payload) {
    return (
      <QRCode
        value={session.qr.payload}
        size={220}
        status="active"
      />
    );
  }
  return (
    <div className="wechat-connection-qr-placeholder">
      <Spin />
      <span>{labels.preparing}</span>
    </div>
  );
}

export default function WechatConnectionPage() {
  const {
    t,
    accounts,
    accountsLoading,
    session,
    sessionStarting,
    actionLoading,
    challengeValue,
    setChallengeValue,
    loadAccounts,
    startScan,
    cancelScan,
    refreshQr,
    submitChallenge,
    closeSessionPanel,
  } = useWechatConnection();

  const columns: ColumnsType<ChannelAccount> = [
    {
      title: t('channelGateway.wechat.accountLabel'),
      dataIndex: 'label',
      key: 'label',
      render: (value: string) => value || '-',
    },
    {
      title: t('channelGateway.wechat.accountStatus'),
      dataIndex: 'status',
      key: 'status',
      width: 140,
      render: (value: string) => (
        <Tag color={statusColor(value)}>
          {t(`channelGateway.wechat.accountStatusMap.${value}`, {
            defaultValue: value,
          })}
        </Tag>
      ),
    },
    {
      title: t('channelGateway.wechat.runtimeStatus'),
      dataIndex: 'runtime_status',
      key: 'runtime_status',
      width: 140,
      render: (value: string) => (
        <Tag color={statusColor(value)}>
          {t(`channelGateway.wechat.runtimeStatusMap.${value}`, {
            defaultValue: value,
          })}
        </Tag>
      ),
    },
    {
      title: t('channelGateway.wechat.connectedAt'),
      dataIndex: 'connected_at',
      key: 'connected_at',
      width: 180,
      render: formatTime,
    },
    {
      title: t('channelGateway.wechat.lastMessageAt'),
      dataIndex: 'last_message_at',
      key: 'last_message_at',
      width: 180,
      render: formatTime,
    },
    {
      title: t('channelGateway.wechat.lastError'),
      dataIndex: 'last_error',
      key: 'last_error',
      ellipsis: true,
      render: (value: string | null) => value || '-',
    },
  ];

  return (
    <div className="wechat-connection-page">
      <header className="wechat-connection-header">
        <div className="wechat-connection-heading">
          <span aria-hidden="true">
            <WechatOutlined />
          </span>
          <div>
            <Title level={2}>{t('channelGateway.wechat.title')}</Title>
            <Paragraph>{t('channelGateway.wechat.subtitle')}</Paragraph>
          </div>
        </div>
        <Space wrap>
          <Button icon={<ReloadOutlined />} onClick={() => void loadAccounts()}>
            {t('channelGateway.wechat.refreshAccounts')}
          </Button>
          <Button
            type="primary"
            loading={sessionStarting}
            onClick={() => void startScan()}
          >
            {t('channelGateway.wechat.startScan')}
          </Button>
        </Space>
      </header>

      <main className="wechat-connection-content">
        {session ? (
          <section className="wechat-connection-scan-panel">
            <div className="wechat-connection-scan-copy">
              <Text strong>
                {t(`channelGateway.wechat.sessionStatusMap.${session.status}`, {
                  defaultValue: session.status,
                })}
              </Text>
              <Paragraph>{session.message}</Paragraph>
              {session.error ? (
                <Paragraph type="danger">
                  {session.error.message}
                </Paragraph>
              ) : null}
            </div>

            <div className="wechat-connection-qr-wrap">
              {renderSessionVisual(session, {
                preparing: t('channelGateway.wechat.preparingQr'),
                connected: t('channelGateway.wechat.connectSuccessVisual'),
                failed: t('channelGateway.wechat.connectFailedVisual'),
              })}
              {session.qr?.expires_at && isActiveScan(session) ? (
                <Text type="secondary">
                  {t('channelGateway.wechat.qrExpiresAt', {
                    time: formatTime(session.qr.expires_at),
                  })}
                </Text>
              ) : null}
            </div>

            {session.status === 'verification_required' ||
            canAct(session, 'submit_challenge') ? (
              <div className="wechat-connection-challenge">
                <Text>
                  {session.challenge?.prompt ||
                    t('channelGateway.wechat.challengePrompt')}
                </Text>
                <Space.Compact style={{ width: '100%', maxWidth: 360 }}>
                  <Input
                    value={challengeValue}
                    maxLength={12}
                    inputMode="numeric"
                    placeholder={t('channelGateway.wechat.challengePlaceholder')}
                    onChange={(event) => setChallengeValue(event.target.value)}
                    onPressEnter={() => void submitChallenge()}
                  />
                  <Button
                    type="primary"
                    loading={actionLoading}
                    onClick={() => void submitChallenge()}
                  >
                    {t('channelGateway.wechat.submitChallenge')}
                  </Button>
                </Space.Compact>
              </div>
            ) : null}

            <Space wrap className="wechat-connection-scan-actions">
              {canAct(session, 'refresh') ? (
                <Button
                  loading={actionLoading}
                  onClick={() => void refreshQr()}
                >
                  {t('channelGateway.wechat.refreshQr')}
                </Button>
              ) : null}
              {canAct(session, 'cancel') ? (
                <Button
                  danger
                  loading={actionLoading}
                  onClick={() => void cancelScan()}
                >
                  {t('channelGateway.wechat.cancelScan')}
                </Button>
              ) : null}
              {!isActiveScan(session) ? (
                <Button onClick={closeSessionPanel}>
                  {t('channelGateway.wechat.closePanel')}
                </Button>
              ) : null}
            </Space>
          </section>
        ) : null}

        <section className="wechat-connection-accounts">
          <div className="wechat-connection-accounts-head">
            <Title level={4}>{t('channelGateway.wechat.accountsTitle')}</Title>
            <Text type="secondary">
              {t('channelGateway.wechat.accountsHint')}
            </Text>
          </div>
          <Table<ChannelAccount>
            rowKey="id"
            loading={accountsLoading}
            columns={columns}
            dataSource={accounts}
            pagination={false}
            locale={{
              emptyText: (
                <Empty
                  description={t('channelGateway.wechat.accountsEmpty')}
                />
              ),
            }}
          />
        </section>
      </main>
    </div>
  );
}
