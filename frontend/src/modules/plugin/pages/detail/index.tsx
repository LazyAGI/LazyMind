import { useState, useEffect, useCallback, useRef } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { Alert, Breadcrumb, Skeleton, Spin, Input, message } from 'antd';
import { SyncOutlined } from '@ant-design/icons';
import { getPluginDraft, updatePluginDraftContent } from '../../pluginDraftApi';
import type { PluginDraftRecord } from '../../pluginDraftApi';
import StateGraphEditor from '../../components/StateGraphEditor';
import type { SavePayload } from '../../components/StateGraphEditor';
import './index.scss';

const POLL_INTERVAL_MS = 3000;

export default function PluginDetailPage() {
  const { pluginId } = useParams<{ pluginId: string }>();
  const navigate = useNavigate();

  const [draft, setDraft] = useState<PluginDraftRecord | null>(null);
  const [loading, setLoading] = useState(true);
  const [editingName, setEditingName] = useState(false);
  const [nameValue, setNameValue] = useState('');
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

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

  // Poll for generate_status changes when status == 'generating'
  const startPolling = useCallback(() => {
    if (pollRef.current) clearInterval(pollRef.current);
    pollRef.current = setInterval(async () => {
      if (!pluginId) return;
      try {
        const data = await getPluginDraft(pluginId);
        setDraft(data);
        if (data.generate_status !== 'generating') {
          if (pollRef.current) clearInterval(pollRef.current);
          pollRef.current = null;
        }
      } catch {
        // ignore polling errors
      }
    }, POLL_INTERVAL_MS);
  }, [pluginId]);

  useEffect(() => {
    void loadDraft();
    return () => { if (pollRef.current) clearInterval(pollRef.current); };
  }, [loadDraft]);

  useEffect(() => {
    if (draft?.generate_status === 'generating') {
      startPolling();
    } else {
      if (pollRef.current) clearInterval(pollRef.current);
    }
  }, [draft?.generate_status, startPolling]);

  const handleSave = useCallback(
    async (payload: SavePayload) => {
      if (!pluginId) return;
      await updatePluginDraftContent(pluginId, {
        state_yaml_content: payload.stateYaml,
        plugin_yaml_content: payload.pluginYaml,
        scenario_content: payload.scenarioContent,
        scripts_content: payload.scriptsContent,
      });
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

  const isGenerating = draft.generate_status === 'generating';
  const isFailed = draft.generate_status === 'failed';

  // Determine which YAML content to use: prefer new columns, fallback to legacy content
  const stateYaml = draft.state_yaml_content || draft.content || undefined;
  // Ensure plugin.yaml always contains at least the draft name so the modal can pre-fill it
  let pluginYaml = draft.plugin_yaml_content || undefined;
  if (!pluginYaml && draft.name) {
    pluginYaml = `name: "${draft.name.replace(/"/g, '\\"')}"\n`;
  }

  return (
    <div className="plugin-detail-page">
      {isGenerating && (
        <Alert
          className="plugin-detail-banner"
          type="info"
          icon={<SyncOutlined spin />}
          showIcon
          message="AI 正在生成插件内容，通常需要 10~30 秒…"
        />
      )}

      {isFailed && (
        <Alert
          className="plugin-detail-banner"
          type="error"
          showIcon
          message="生成失败，你可以手动编辑或重新生成"
        />
      )}

      {isGenerating ? (
        <div className="plugin-detail-skeleton">
          <Skeleton active paragraph={{ rows: 12 }} />
        </div>
      ) : (
        <div className="plugin-detail-editor">
          <StateGraphEditor
            initialStateYaml={stateYaml}
            initialPluginYaml={pluginYaml}
            initialScenarioContent={draft.scenario_content || undefined}
            initialScriptsContent={draft.scripts_content || undefined}
            pluginName={
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
            }
            onSave={handleSave}
            onClose={() => navigate('/memory-management/plugins')}
          />
        </div>
      )}
    </div>
  );
}
