import { useRef, useState } from 'react';
import { Button, Dropdown, Input, InputNumber } from 'antd';
import type { InputRef, MenuProps } from 'antd';
import {
  PlusOutlined,
  EllipsisOutlined,
} from '@ant-design/icons';
import type { PluginModel, PluginUiTab, PluginSlotDef, WidgetConfig, WidgetType } from '../core/pluginModel';
import { SLOT_DEFAULT_WIDGET } from '../core/pluginModel';
import type { SlotDef } from '../core/model';
import WidgetPlaceholder from './WidgetPlaceholder';
import UiEditorCanvas from './UiEditorCanvas';
import './UiWysiwygPreview.scss';

const LAYOUT_LABELS: Record<string, string> = {
  vertical: 'Vertical',
  list: 'Vertical (旧)',
  grid: 'Grid',
  horizontal: 'Horizontal',
  composite: 'Composite',
};

function resolveSlot(
  slotId: string,
  slotMap: Record<string, SlotDef>,
  pluginSlotMap: Record<string, PluginSlotDef>,
) {
  const g = slotMap[slotId];
  const p = pluginSlotMap[slotId];
  return {
    type: (g?.type ?? p?.type ?? 'text') as 'text' | 'image' | 'file' | 'json',
    cardinality: g?.cardinality ?? p?.cardinality,
    label: g?.label ?? p?.label ?? slotId,
  };
}

function getWidgetConfig(
  slotEntry: { id: string; widget?: WidgetConfig },
  slotMap: Record<string, SlotDef>,
  pluginSlotMap: Record<string, PluginSlotDef>,
): WidgetConfig {
  if (slotEntry.widget) return slotEntry.widget;
  const { type, cardinality } = resolveSlot(slotEntry.id, slotMap, pluginSlotMap);
  const key = `${type}/${cardinality ?? 'single'}`;
  const widgetType: WidgetType = (SLOT_DEFAULT_WIDGET[key] ?? 'text-single') as WidgetType;
  return { widgetType } as WidgetConfig;
}

interface CompositeColumnNode {
  slot?: string;
  weight?: number;
  direction?: string;
  children?: unknown[];
}

function buildCompositeColumns(tab: PluginUiTab): Array<{ slotId: string; weight: number }> {
  const layout = tab.composite_layout;
  if (!Array.isArray(layout) || layout.length === 0) {
    return tab.slots.map((s) => ({ slotId: s.id, weight: 1 }));
  }
  // Support new tree format { direction, children }
  if (!Array.isArray(layout) && typeof layout === 'object' && layout !== null && 'direction' in (layout as object)) {
    return tab.slots.map((s) => ({ slotId: s.id, weight: 1 }));
  }
  return (layout as unknown[])
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

interface TabContentProps {
  tab: PluginUiTab;
  slotMap: Record<string, SlotDef>;
  pluginSlotMap: Record<string, PluginSlotDef>;
  gridCols?: number;
  onSlotsChange?: (slots: Array<{ id: string; widget?: WidgetConfig }>) => void;
  onCompositeLayoutChange?: (value: unknown) => void;
}

function TabContent({ tab, slotMap, pluginSlotMap, gridCols, onSlotsChange, onCompositeLayoutChange }: TabContentProps) {
  // Composite layout always renders the editor (even with no slots yet)
  if (tab.layout !== 'composite' && tab.slots.length === 0) {
    return (
      <div className="wywp-no-slots">将左侧素材拖入此处，或点击素材行「加入 Tab」</div>
    );
  }

  // Editable canvas (covers all layouts including composite)
  if (onSlotsChange && onCompositeLayoutChange) {
    return (
      <UiEditorCanvas
        tab={tab}
        slotMap={slotMap}
        onSlotsChange={onSlotsChange}
        onCompositeLayoutChange={onCompositeLayoutChange}
      />
    );
  }

  // Composite layout (read-only fallback)
  if (tab.layout === 'composite') {
    const columns = buildCompositeColumns(tab);
    if (columns.length === 0) {
      return (
        <div className="wywp-layout-vertical">
          {tab.slots.map((s) => {
            const widget = getWidgetConfig(s, slotMap, pluginSlotMap);
            const label = resolveSlot(s.id, slotMap, pluginSlotMap).label;
            return <WidgetPlaceholder key={s.id} widgetConfig={widget} label={label} />;
          })}
        </div>
      );
    }
    const totalWeight = columns.reduce((sum, c) => sum + c.weight, 0);
    return (
      <div className="wywp-layout-composite">
        {columns.map((col) => {
          const pct = totalWeight > 0 ? (col.weight / totalWeight) * 100 : 100 / columns.length;
          const slotEntry = tab.slots.find((s) => s.id === col.slotId) ?? { id: col.slotId };
          const widget = getWidgetConfig(slotEntry, slotMap, pluginSlotMap);
          const label = resolveSlot(col.slotId, slotMap, pluginSlotMap).label;
          return (
            <div key={col.slotId} className="wywp-composite-col" style={{ flexBasis: `${pct}%` }}>
              <WidgetPlaceholder widgetConfig={widget} label={label} />
            </div>
          );
        })}
      </div>
    );
  }

  // Read-only fallback: render widget placeholders
  const layoutClass = `wywp-layout-${tab.layout ?? 'vertical'}`;
  const layoutStyle: React.CSSProperties = {};
  if (tab.layout === 'grid' && gridCols) {
    (layoutStyle as Record<string, unknown>)['--wywp-grid-cols'] = `repeat(${gridCols}, 1fr)`;
  }
  return (
    <div className={layoutClass} style={layoutStyle}>
      {tab.slots.map((s) => {
        const widget = getWidgetConfig(s, slotMap, pluginSlotMap);
        const label = resolveSlot(s.id, slotMap, pluginSlotMap).label;
        return <WidgetPlaceholder key={s.id} widgetConfig={widget} label={label} />;
      })}
    </div>
  );
}

interface Props {
  pluginModel: PluginModel;
  activeTabId?: string;
  activeLayout?: PluginUiTab['layout'];
  activeGridCols?: number;
  slotMap: Record<string, SlotDef>;
  onTabSelect?: (tabId: string) => void;
  onAddTab?: () => void;
  onRenameTab?: (tabId: string, label: string) => void;
  onDeleteTab?: (tabId: string) => void;
  onLayoutChange?: (layout: PluginUiTab['layout']) => void;
  onGridColsChange?: (gridCols: number | null) => void;
  onSlotsChange?: (slots: Array<{ id: string; widget?: WidgetConfig }>) => void;
  onCompositeLayoutChange?: (value: unknown) => void;
}

export default function UiWysiwygPreview({
  pluginModel,
  activeTabId,
  activeLayout = 'vertical',
  activeGridCols,
  slotMap,
  onTabSelect,
  onAddTab,
  onRenameTab,
  onDeleteTab,
  onLayoutChange,
  onGridColsChange,
  onSlotsChange,
  onCompositeLayoutChange,
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

  const layoutMenuItems = (['vertical', 'grid', 'horizontal', 'composite'] as const).map((l) => ({
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
                          ? [{ key: 'rename', label: '重命名' as React.ReactNode, onClick: ({ domEvent }: { domEvent: React.MouseEvent }) => { domEvent.stopPropagation(); startEdit(tab); } }]
                          : []),
                        ...(onDeleteTab && tabs.length > 1
                          ? [{
                              key: 'delete',
                              label: <span style={{ color: '#ff4d4f' }}>删除 Tab</span> as React.ReactNode,
                              onClick: ({ domEvent }: { domEvent: React.MouseEvent }) => {
                                domEvent.stopPropagation();
                                onDeleteTab(tabId);
                              },
                            }]
                          : []),
                      ] as MenuProps['items'],
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

        {/* Layout picker + grid cols config */}
        <div className="wywp-stepbar-right">
          {onLayoutChange && (
            <Dropdown menu={{ items: layoutMenuItems }} trigger={['click']}>
              <Button size="small" className="wywp-layout-btn">
                布局: {LAYOUT_LABELS[activeLayout ?? 'vertical']} ▾
              </Button>
            </Dropdown>
          )}
          {activeLayout === 'grid' && onGridColsChange && (
            <div className="wywp-grid-cols-control">
              <span className="wywp-grid-cols-label">列数:</span>
              <InputNumber
                size="small"
                min={1}
                max={12}
                value={activeGridCols}
                placeholder="auto"
                onChange={onGridColsChange}
                style={{ width: 64 }}
              />
            </div>
          )}
        </div>
      </div>

      {/* Content */}
      <div className="wywp-content">
        {activeTab && (
          <TabContent
            key={activeTab.id}
            tab={activeTab}
            slotMap={slotMap}
            pluginSlotMap={pluginSlotMap}
            gridCols={activeGridCols}
            onSlotsChange={onSlotsChange}
            onCompositeLayoutChange={onCompositeLayoutChange}
          />
        )}
      </div>

      {/* Footer actions (read-only preview) */}
      <div className="wywp-footer">
        <button type="button" className="wywp-btn wywp-btn--ghost">重试</button>
        <button type="button" className="wywp-btn wywp-btn--primary">继续</button>
      </div>
    </div>
  );
}
