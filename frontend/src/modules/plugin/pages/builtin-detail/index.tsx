import { useState, useEffect } from 'react';
import { useParams, useNavigate, useOutletContext } from 'react-router-dom';
import { Breadcrumb, Skeleton, Alert } from 'antd';
import { getBuiltinPlugin } from '../../pluginDraftApi';
import type { BuiltinPlugin } from '../../pluginDraftApi';
import StateGraphEditor from '../../components/StateGraphEditor';

export default function BuiltinPluginDetailPage() {
  const { pluginId } = useParams<{ pluginId: string }>();
  const navigate = useNavigate();
  const { isMenuCollapsed, toggleMenu } = useOutletContext<{
    isMenuCollapsed: boolean;
    toggleMenu: () => void;
  }>();

  const [plugin, setPlugin] = useState<BuiltinPlugin | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!pluginId) return;
    setLoading(true);
    setError(null);
    getBuiltinPlugin(pluginId)
      .then((data) => setPlugin(data))
      .catch(() => setError('内置插件加载失败'))
      .finally(() => setLoading(false));
  }, [pluginId]);

  // Collapse side menu when entering detail page (same pattern as PluginDetailPage)
  useEffect(() => {
    if (!isMenuCollapsed) toggleMenu();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  if (loading) {
    return (
      <div style={{ padding: 24 }}>
        <Skeleton active />
      </div>
    );
  }

  if (error || !plugin) {
    return (
      <div style={{ padding: 24 }}>
        <Alert
          type="error"
          message={error ?? '内置插件不存在'}
          action={
            <button
              style={{ cursor: 'pointer', background: 'none', border: 'none', color: '#1677ff' }}
              onClick={() => navigate('/memory-management/skills?skillView=plugins')}
            >
              返回列表
            </button>
          }
        />
      </div>
    );
  }

  const pluginName = (
    <Breadcrumb
      items={[
        { title: <a onClick={() => navigate('/memory-management/skills?skillView=plugins')}>插件列表</a> },
        { title: plugin.name || plugin.id },
      ]}
    />
  );

  return (
    <StateGraphEditor
      initialPluginYaml={plugin.plugin_yaml_raw}
      initialStateYaml={plugin.state_yaml_raw}
      initialScenarioContent={plugin.scenario_raw}
      pluginName={pluginName}
      readonly={true}
      showEmptyHint={false}
      onClose={() => navigate('/memory-management/skills?skillView=plugins')}
    />
  );
}
