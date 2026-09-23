import { getLocalizedErrorMessage } from '@/components/request';
import { CloudDownloadOutlined } from '@ant-design/icons';
import { Alert, Button, Empty, Space, Spin, Table, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useCallback, useEffect, useState } from 'react';
import {
  beginCloudLogin,
  getCloudSession,
  isCloudBusinessAvailable,
  LAZYMIND_CLOUD_SESSION_CHANGED_EVENT,
} from '@/runtime/cloud/session';
import {
  closeCloudLoginPopup,
  openCloudLogin,
  openCloudRegister,
  reserveCloudLoginPopup,
} from '@/runtime/desktopBridge';
import {
  downloadCloudResource,
  listCloudResources,
  type CloudResourceItem,
  type CloudResourceType,
} from '../../cloudResourceApi';

interface CloudResourceTableProps {
  resourceType: CloudResourceType;
  t: (key: string, options?: Record<string, unknown>) => string;
  onDownloaded?: () => void | Promise<void>;
}

export default function CloudResourceTable({ resourceType, t, onDownloaded }: CloudResourceTableProps) {
  const [items, setItems] = useState<CloudResourceItem[]>([]);
  const [signedIn, setSignedIn] = useState(false);
  const [sessionLoading, setSessionLoading] = useState(true);
  const [loading, setLoading] = useState(false);
  const [loadFailed, setLoadFailed] = useState(false);
  const [loginLoading, setLoginLoading] = useState(false);
  const [downloading, setDownloading] = useState<string>();

  const load = useCallback(async () => {
    setSessionLoading(true);
    setLoadFailed(false);
    try {
      const session = await getCloudSession();
      const available = isCloudBusinessAvailable(session);
      setSignedIn(available);
      if (!available) {
        setItems([]);
        return;
      }
      setLoading(true);
      try {
        setItems(await listCloudResources(resourceType));
      } catch {
        setItems([]);
        setLoadFailed(true);
      }
    } catch {
      setItems([]);
      setSignedIn(false);
    } finally {
      setLoading(false);
      setSessionLoading(false);
    }
  }, [resourceType]);

  useEffect(() => {
    void load();
    const changed = () => { void load(); };
    window.addEventListener(LAZYMIND_CLOUD_SESSION_CHANGED_EVENT, changed);
    return () => window.removeEventListener(LAZYMIND_CLOUD_SESSION_CHANGED_EVENT, changed);
  }, [load]);

  const handleLogin = async () => {
    const popup = reserveCloudLoginPopup();
    if (popup === null) {
      message.error(t('layout.cloudOpenFailed'));
      return;
    }
    setLoginLoading(true);
    try {
      const login = await beginCloudLogin();
      const opened = await openCloudLogin(login.authorization_url, popup);
      if (!opened.ok) throw opened.error ?? new Error(opened.reason);
    } catch {
      closeCloudLoginPopup(popup);
      message.error(t('layout.cloudLoginFailed'));
    } finally {
      setLoginLoading(false);
    }
  };

  const handleRegister = async () => {
    let registrationURL = '';
    try {
      registrationURL = (await getCloudSession()).registration_url || '';
    } catch {
      registrationURL = '';
    }
    const result = await openCloudRegister(registrationURL);
    if (!result.ok) message.error(t('layout.cloudOpenFailed'));
  };

  const columns: ColumnsType<CloudResourceItem> = [
    {
      title: t('admin.memoryWorkflowColName'),
      dataIndex: 'resource_name',
      key: 'resource_name',
      ellipsis: true,
    },
    {
      title: t('admin.memoryWorkflowColId'),
      dataIndex: 'resource_id',
      key: 'resource_id',
      ellipsis: true,
    },
    {
      key: 'actions',
      width: 88,
      render: (_: unknown, row: CloudResourceItem) => (
        <Button
          type="text"
          size="small"
          icon={<CloudDownloadOutlined />}
          aria-label={t('admin.memoryCloudDownload')}
          loading={downloading === row.resource_id}
          onClick={async () => {
            setDownloading(row.resource_id);
            try {
              await downloadCloudResource(resourceType, row.resource_id);
              await onDownloaded?.();
              await load();
              message.success(t('admin.memoryCloudDownloadSuccess', { name: row.resource_name }));
            } catch (err) {
              message.error(getLocalizedErrorMessage(err, t('admin.memoryCloudDownloadFailed')));
            } finally {
              setDownloading(undefined);
            }
          }}
        />
      ),
    },
  ];

  if (sessionLoading && items.length === 0) {
    return (
      <div role="status">
        <Spin size="small" /> {t('admin.memoryCloudLoading')}
      </div>
    );
  }

  if (!signedIn && !loadFailed) {
    return (
      <Empty description={t('admin.memoryCloudLoginRequired')} style={{ marginTop: 60 }}>
        <Space>
          <Button type="primary" loading={loginLoading} onClick={() => void handleLogin()}>
            {t('layout.cloudLogin')}
          </Button>
          <Button onClick={() => void handleRegister()}>{t('admin.memoryCloudRegister')}</Button>
        </Space>
      </Empty>
    );
  }

  if (loadFailed) {
    return (
      <Alert
        type="error"
        showIcon
        message={t('admin.memoryCloudLoadFailed')}
        action={<Button onClick={() => void load()}>{t('common.retry')}</Button>}
      />
    );
  }

  if (items.length === 0) {
    return <Empty description={t('admin.memoryWorkflowEmptyNoResult')} />;
  }

  return (
    <Table<CloudResourceItem>
      className="admin-page-table memory-table memory-skill-installed-table"
      rowKey="resource_id"
      loading={loading}
      dataSource={items}
      columns={columns}
      pagination={false}
      tableLayout="fixed"
    />
  );
}
