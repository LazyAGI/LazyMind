import { getLocalizedErrorMessage } from '@/components/request';
import { CloudDownloadOutlined } from '@ant-design/icons';
import { Alert, Button, Empty, Spin, Table, message } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { useCallback, useEffect, useState } from 'react';
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
  const [loading, setLoading] = useState(false);
  const [loadFailed, setLoadFailed] = useState(false);
  const [downloading, setDownloading] = useState<string>();

  const load = useCallback(async () => {
    setLoading(true);
    setLoadFailed(false);
    try {
      setItems(await listCloudResources(resourceType));
    } catch {
      setItems([]);
      setLoadFailed(true);
    } finally {
      setLoading(false);
    }
  }, [resourceType]);

  useEffect(() => {
    void load();
  }, [load]);

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

  if (loading && items.length === 0) {
    return (
      <div role="status">
        <Spin size="small" /> {t('admin.memoryCloudLoading')}
      </div>
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
