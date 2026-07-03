import { useState, useEffect, useCallback } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { Breadcrumb, Spin, Input, message } from 'antd';
import { ArrowLeftOutlined } from '@ant-design/icons';
import { getPluginDraft, updatePluginDraftContent } from '../../pluginDraftApi';
import type { PluginDraftRecord } from '../../pluginDraftApi';
import StateGraphEditor from '../../components/StateGraphEditor';
import './index.scss';

export default function PluginDetailPage() {
  const { pluginId } = useParams<{ pluginId: string }>();
  const navigate = useNavigate();

  const [draft, setDraft] = useState<PluginDraftRecord | null>(null);
  const [loading, setLoading] = useState(true);
  const [editingName, setEditingName] = useState(false);
  const [nameValue, setNameValue] = useState('');

  const loadDraft = useCallback(async () => {
    if (!pluginId) return;
    setLoading(true);
    try {
      const data = await getPluginDraft(pluginId);
      setDraft(data);
      setNameValue(data.name);
    } catch {
      message.error('加载插件草稿失败');
    } finally {
      setLoading(false);
    }
  }, [pluginId]);

  useEffect(() => {
    void loadDraft();
  }, [loadDraft]);

  const handleSave = useCallback(
    async (yaml: string) => {
      if (!pluginId) return;
      await updatePluginDraftContent(pluginId, yaml);
      setDraft((prev) => (prev ? { ...prev, content: yaml } : prev));
    },
    [pluginId],
  );

  if (loading) {
    return (
      <div className="plugin-detail-loading">
        <Spin tip="加载中..." />
      </div>
    );
  }

  if (!draft) {
    return (
      <div className="plugin-detail-error">
        <p>插件草稿不存在</p>
      </div>
    );
  }

  return (
    <div className="plugin-detail-page">
      <div className="plugin-detail-header">
        <button
          type="button"
          className="plugin-detail-back"
          onClick={() => navigate('/memory-management/plugins')}
          aria-label="返回插件列表"
        >
          <ArrowLeftOutlined />
        </button>
        <Breadcrumb
          items={[
            { title: '我的插件', href: '/memory-management/plugins' },
            {
              title: editingName ? (
                <Input
                  autoFocus
                  size="small"
                  value={nameValue}
                  style={{ width: 200 }}
                  onChange={(e) => setNameValue(e.target.value)}
                  onBlur={() => setEditingName(false)}
                  onPressEnter={() => setEditingName(false)}
                />
              ) : (
                <button
                  type="button"
                  className="plugin-detail-name"
                  onClick={() => setEditingName(true)}
                  title="点击编辑名称"
                >
                  {nameValue}
                </button>
              ),
            },
          ]}
        />
      </div>

      <div className="plugin-detail-editor">
        <StateGraphEditor
          initialYaml={draft.content || undefined}
          onSave={handleSave}
          onClose={() => navigate('/memory-management/plugins')}
        />
      </div>
    </div>
  );
}
