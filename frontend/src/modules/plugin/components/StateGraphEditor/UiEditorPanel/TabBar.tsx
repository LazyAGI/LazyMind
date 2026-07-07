import { useRef, useState } from 'react';
import { Button, Dropdown, Input } from 'antd';
import type { InputRef } from 'antd';
import { PlusOutlined, EllipsisOutlined } from '@ant-design/icons';
import type { PluginUiTab } from '../core/pluginModel';

interface Props {
  tabs: PluginUiTab[];
  activeTabId: string | undefined;
  onSelect: (tabId: string) => void;
  onAdd: () => void;
  onRename: (tabId: string, label: string) => void;
  onDelete: (tabId: string) => void;
}

export default function TabBar({ tabs, activeTabId, onSelect, onAdd, onRename, onDelete }: Props) {
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editValue, setEditValue] = useState('');
  const inputRef = useRef<InputRef | null>(null);

  const startEdit = (tab: PluginUiTab) => {
    setEditingId(tab.id);
    setEditValue(tab.label ?? tab.id);
    // Focus after render
    setTimeout(() => inputRef.current?.focus(), 30);
  };

  const commitEdit = (tabId: string) => {
    onRename(tabId, editValue.trim() || tabId);
    setEditingId(null);
  };

  return (
    <div className="uep-tabbar">
      {tabs.map((tab) => (
        <div
          key={tab.id}
          className={`uep-tab${tab.id === activeTabId ? ' uep-tab--active' : ''}`}
          onClick={() => onSelect(tab.id)}
          onDoubleClick={(e) => { e.stopPropagation(); startEdit(tab); }}
          role="tab"
          aria-selected={tab.id === activeTabId}
        >
          {editingId === tab.id ? (
            <Input
              ref={inputRef}
              size="small"
              value={editValue}
              onChange={(e) => setEditValue(e.target.value)}
              onBlur={() => commitEdit(tab.id)}
              onPressEnter={() => commitEdit(tab.id)}
              onClick={(e) => e.stopPropagation()}
              className="uep-tab-input"
            />
          ) : (
            <span className="uep-tab-label">{tab.label ?? tab.id}</span>
          )}
          <Dropdown
            menu={{
              items: [
                { key: 'rename', label: '重命名', onClick: ({ domEvent }) => { domEvent.stopPropagation(); startEdit(tab); } },
                { key: 'delete', label: <span style={{ color: '#ff4d4f' }}>删除 Tab</span>, onClick: ({ domEvent }) => { domEvent.stopPropagation(); onDelete(tab.id); } },
              ],
            }}
            trigger={['click']}
          >
            <Button
              type="text"
              size="small"
              icon={<EllipsisOutlined />}
              className="uep-tab-menu"
              onClick={(e) => e.stopPropagation()}
              aria-label="Tab 操作"
            />
          </Dropdown>
        </div>
      ))}
      <Button
        type="text"
        size="small"
        icon={<PlusOutlined />}
        className="uep-tabbar-add"
        onClick={onAdd}
        aria-label="新建 Tab"
      >
        新建 Tab
      </Button>
    </div>
  );
}
