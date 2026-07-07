import { useRef, useState } from 'react';
import { Button, Dropdown, Input } from 'antd';
import type { InputRef } from 'antd';
import {
  PlusOutlined,
  EllipsisOutlined,
  FileTextOutlined,
  PictureOutlined,
  FileOutlined,
  CodeOutlined,
} from '@ant-design/icons';
import type { PluginModel, PluginUiTab, PluginSlotDef } from '../core/pluginModel';
import type { SlotDef } from '../core/model';
import './UiWysiwygPreview.scss';

// ~200-char Chinese placeholder text
const LOREM_TEXT =
  '这是一段示例文本，用于模拟插件运行时的真实内容。在实际使用中，此处将展示由 AI 自动生成或用户手动填写的文字内容，包括分析结果、描述信息、操作建议等。内容长度因场景而异，通常在数十字到数百字之间。';

const LAYOUT_LABELS: Record<string, string> = {
  list: 'List',
  grid: 'Grid',
  horizontal: 'Horizontal',
  composite: 'Composite',
};

interface CompositeColumnNode {
  slot?: string;
  weight?: number;
}

function buildCompositeColumns(tab: PluginUiTab): Array<{ slotId: string; weight: number }> {
  const layout = tab.composite_layout;
  if (!Array.isArray(layout) || layout.length === 0) {
    return tab.slots.map((s) => ({ slotId: s.id, weight: 1 }));
  }
  return layout
    .map((node) => {
      if (typeof node === 'string') return { slotId: node, weight: 1 };
      if (typeof node === 'object' && node !== null && 'slot' in node) {
        const n = node as CompositeColumnNode;
        return n.slot ? { slotId: n.slot, weight: n.weight ?? 1 } : null;
      }
      return null;
    })
    .filter((c): c is { slotId: string; weight: number } => c !== null);
}

function resolveSlot(
  slotId: string,
  slotMap: Record<string, SlotDef>,
  pluginSlotMap: Record<string, PluginSlotDef>,
) {
  const g = slotMap[slotId];
  const p = pluginSlotMap[slotId];
  return {
    type: (g?.type ?? p?.type ?? 'text') as 'text' | 'image' | 'file' | 'json',
    isList: (g?.cardinality ?? p?.cardinality) === 'list',
    label: g?.label ?? p?.label ?? slotId,
  };
}

interface SlotPlaceholderProps {
  slotId: string;
  slotMap: Record<string, SlotDef>;
  pluginSlotMap: Record<string, PluginSlotDef>;
}

function ImagePlaceholder({ label }: { label: string }) {
  return (
    <div className="wywp-img-placeholder">
      <PictureOutlined className="wywp-img-icon" />
      <span className="wywp-img-label">{label}</span>
    </div>
  );
}

function FilePlaceholder({ label }: { label: string }) {
  return (
    <div className="wywp-file-card">
      <FileOutlined className="wywp-file-icon" />
      <div className="wywp-file-info">
        <span className="wywp-file-name">{label} 示例文件.pdf</span>
        <span className="wywp-file-size">128 KB</span>
      </div>
    </div>
  );
}

function SlotPlaceholder({ slotId, slotMap, pluginSlotMap }: SlotPlaceholderProps) {
  const { type, isList, label } = resolveSlot(slotId, slotMap, pluginSlotMap);

  if (type === 'image') {
    const count = isList ? 4 : 1;
    return (
      <div className="wywp-slot">
        <div className="wywp-slot-label">{label}</div>
        <div className={`wywp-img-group${isList ? ' wywp-img-group--list' : ''}`}>
          {Array.from({ length: count }).map((_, i) => (
            <ImagePlaceholder key={i} label={label} />
          ))}
        </div>
      </div>
    );
  }

  if (type === 'file') {
    const count = isList ? 2 : 1;
    return (
      <div className="wywp-slot">
        <div className="wywp-slot-label">{label}</div>
        <div className="wywp-file-group">
          {Array.from({ length: count }).map((_, i) => (
            <FilePlaceholder key={i} label={label} />
          ))}
        </div>
      </div>
    );
  }

  if (type === 'json') {
    return (
      <div className="wywp-slot">
        <div className="wywp-slot-label">
          <CodeOutlined style={{ marginRight: 4 }} />
          {label}
        </div>
        <pre className="wywp-json-block">
          {`{\n  "key": "value",\n  "items": [1, 2, 3],\n  "status": "success"\n}`}
        </pre>
      </div>
    );
  }

  const count = isList ? 3 : 1;
  return (
    <div className="wywp-slot">
      <div className="wywp-slot-label">
        <FileTextOutlined style={{ marginRight: 4 }} />
        {label}
      </div>
      <div className="wywp-text-group">
        {Array.from({ length: count }).map((_, i) => (
          <div key={i} className="wywp-text-block">
            {count > 1 && <span className="wywp-text-index">{i + 1}</span>}
            {LOREM_TEXT}
          </div>
        ))}
      </div>
    </div>
  );
}

interface TabContentProps {
  tab: PluginUiTab;
  slotMap: Record<string, SlotDef>;
  pluginSlotMap: Record<string, PluginSlotDef>;
}

function TabContent({ tab, slotMap, pluginSlotMap }: TabContentProps) {
  if (tab.slots.length === 0) {
    return (
      <div className="wywp-no-slots">将左侧素材拖入此处，或点击素材行「加入 Tab」</div>
    );
  }

  if (tab.layout === 'composite') {
    const columns = buildCompositeColumns(tab);
    if (columns.length === 0) {
      return (
        <div className="wywp-layout-list">
          {tab.slots.map((s) => (
            <SlotPlaceholder key={s.id} slotId={s.id} slotMap={slotMap} pluginSlotMap={pluginSlotMap} />
          ))}
        </div>
      );
    }
    const totalWeight = columns.reduce((s, c) => s + c.weight, 0);
    return (
      <div className="wywp-layout-composite">
        {columns.map((col) => {
          const pct = totalWeight > 0 ? (col.weight / totalWeight) * 100 : 100 / columns.length;
          return (
            <div key={col.slotId} className="wywp-composite-col" style={{ flexBasis: `${pct}%` }}>
              <SlotPlaceholder slotId={col.slotId} slotMap={slotMap} pluginSlotMap={pluginSlotMap} />
            </div>
          );
        })}
      </div>
    );
  }

  const layoutClass = `wywp-layout-${tab.layout ?? 'list'}`;
  return (
    <div className={layoutClass}>
      {tab.slots.map((s) => (
        <SlotPlaceholder key={s.id} slotId={s.id} slotMap={slotMap} pluginSlotMap={pluginSlotMap} />
      ))}
    </div>
  );
}

interface Props {
  pluginModel: PluginModel;
  activeTabId?: string;
  activeLayout?: PluginUiTab['layout'];
  slotMap: Record<string, SlotDef>;
  onTabSelect?: (tabId: string) => void;
  onAddTab?: () => void;
  onRenameTab?: (tabId: string, label: string) => void;
  onDeleteTab?: (tabId: string) => void;
  onLayoutChange?: (layout: PluginUiTab['layout']) => void;
}

export default function UiWysiwygPreview({
  pluginModel,
  activeTabId,
  activeLayout = 'list',
  slotMap,
  onTabSelect,
  onAddTab,
  onRenameTab,
  onDeleteTab,
  onLayoutChange,
}: Props) {
  const tabs = pluginModel.ui?.tabs ?? [];
  const pluginSlotMap = Object.fromEntries(pluginModel.slots.map((s) => [s.id, s]));

  const activeIdx = Math.max(0, tabs.findIndex((t) => t.id === activeTabId));
  const activeTab = tabs[activeIdx];

  const [editingId, setEditingId] = useState<string | null>(null);
  const [editValue, setEditValue] = useState('');
  const inputRef = useRef<InputRef | null>(null);

  const startEdit = (tab: PluginUiTab) => {
    setEditingId(tab.id);
    setEditValue(tab.label ?? tab.id);
    setTimeout(() => inputRef.current?.focus(), 30);
  };

  const commitEdit = (tabId: string) => {
    onRenameTab?.(tabId, editValue.trim() || tabId);
    setEditingId(null);
  };

  const layoutMenuItems = (['list', 'grid', 'horizontal', 'composite'] as const).map((l) => ({
    key: l,
    label: LAYOUT_LABELS[l],
    onClick: () => onLayoutChange?.(l),
  }));

  if (tabs.length === 0) {
    return (
      <div className="wywp-root wywp-empty">
        <div className="wywp-empty-hint">
          暂无 UI 配置。点击「新建 Tab」并将左侧素材拖入后，效果将实时呈现。
        </div>
        {onAddTab && (
          <Button size="small" icon={<PlusOutlined />} onClick={onAddTab} style={{ marginTop: 12 }}>
            新建 Tab
          </Button>
        )}
      </div>
    );
  }

  return (
    <div className="wywp-root">
      {/* Step bar + layout picker on the right */}
      <div className="wywp-stepbar">
        <div className="wywp-stepbar-tabs">
          {tabs.map((tab, idx) => {
            // Capture tab.id in a local const to avoid closure over mutable loop var
            const tabId = tab.id;
            return (
              <div
                key={tabId}
                className={`wywp-step${idx === activeIdx ? ' wywp-step--active' : ''}${idx < activeIdx ? ' wywp-step--done' : ''}`}
                onClick={() => { if (editingId !== tabId) onTabSelect?.(tabId); }}
              >
                <span className="wywp-step-badge">{idx + 1}</span>
                {editingId === tabId ? (
                  <Input
                    ref={inputRef}
                    size="small"
                    value={editValue}
                    onChange={(e) => setEditValue(e.target.value)}
                    onBlur={() => commitEdit(tabId)}
                    onPressEnter={() => commitEdit(tabId)}
                    onClick={(e) => e.stopPropagation()}
                    className="wywp-step-input"
                  />
                ) : (
                  <span
                    className="wywp-step-label"
                    onDoubleClick={(e) => { e.stopPropagation(); startEdit(tab); }}
                  >
                    {tab.label ?? tabId}
                  </span>
                )}
                {(onRenameTab || (onDeleteTab && tabs.length > 1)) && (
                  <Dropdown
                    menu={{
                      items: [
                        ...(onRenameTab
                          ? [{ key: 'rename', label: '重命名', onClick: ({ domEvent }: { domEvent: React.MouseEvent }) => { domEvent.stopPropagation(); startEdit(tab); } }]
                          : []),
                        ...(onDeleteTab && tabs.length > 1
                          ? [{
                              key: 'delete',
                              label: <span style={{ color: '#ff4d4f' }}>删除 Tab</span>,
                              onClick: ({ domEvent }: { domEvent: React.MouseEvent }) => {
                                domEvent.stopPropagation();
                                // Use captured tabId, not closure over tab from outer map
                                onDeleteTab(tabId);
                              },
                            }]
                          : []),
                      ],
                    }}
                    trigger={['click']}
                  >
                    <Button
                      type="text"
                      size="small"
                      icon={<EllipsisOutlined />}
                      className="wywp-step-menu"
                      onClick={(e) => e.stopPropagation()}
                    />
                  </Dropdown>
                )}
              </div>
            );
          })}
          {onAddTab && (
            <Button
              type="text"
              size="small"
              icon={<PlusOutlined />}
              className="wywp-stepbar-add"
              onClick={onAddTab}
            >
              新建 Tab
            </Button>
          )}
        </div>

        {/* Layout picker — right side of the stepbar row */}
        {onLayoutChange && (
          <Dropdown menu={{ items: layoutMenuItems }} trigger={['click']}>
            <Button size="small" className="wywp-layout-btn">
              布局: {LAYOUT_LABELS[activeLayout]} ▾
            </Button>
          </Dropdown>
        )}
      </div>

      {/* Content */}
      <div className="wywp-content">
        {activeTab && (
          <TabContent tab={activeTab} slotMap={slotMap} pluginSlotMap={pluginSlotMap} />
        )}
      </div>

      {/* Footer actions */}
      <div className="wywp-footer">
        <button type="button" className="wywp-btn wywp-btn--ghost">重试</button>
        <button type="button" className="wywp-btn wywp-btn--primary">继续</button>
      </div>
    </div>
  );
}
