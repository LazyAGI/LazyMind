import { useEffect, useRef, useState } from 'react';
import { Button, Dropdown, Input, InputNumber } from 'antd';
import type { InputRef, MenuProps } from 'antd';
import { PlusOutlined, EllipsisOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import type { PluginModel, PluginUiTab, PluginSlotDef, WidgetConfig, WidgetType, CompositePanelNode } from '../core/pluginModel';
import { SLOT_DEFAULT_WIDGET } from '../core/pluginModel';
import type { SlotDef } from '../core/model';
import WidgetPlaceholder from './WidgetPlaceholder';
import UiEditorCanvas from './UiEditorCanvas';
import './UiWysiwygPreview.scss';

const LAYOUT_LABELS: Record<string, string> = {
  vertical: 'Vertical',
  list: 'Vertical (legacy)',
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
  slotId: string,
  uiSlots: Record<string, WidgetConfig>,
  slotMap: Record<string, SlotDef>,
  pluginSlotMap: Record<string, PluginSlotDef>,
): WidgetConfig {
  if (uiSlots[slotId]) return uiSlots[slotId];
  const { type, cardinality } = resolveSlot(slotId, slotMap, pluginSlotMap);
  const key = `${type}/${cardinality ?? 'single'}`;
  const widgetType: WidgetType = (SLOT_DEFAULT_WIDGET[key] ?? 'text-single') as WidgetType;
  return { widgetType } as WidgetConfig;
}

// ---------------------------------------------------------------------------
// Composite read-only renderer: supports format C recursive tree
// ---------------------------------------------------------------------------

function renderCompositeNode(
  node: CompositePanelNode,
  uiSlots: Record<string, WidgetConfig>,
  slotMap: Record<string, SlotDef>,
  pluginSlotMap: Record<string, PluginSlotDef>,
): React.ReactNode {
  if (node.slot) {
    const widget = getWidgetConfig(node.slot, uiSlots, slotMap, pluginSlotMap);
    const label = resolveSlot(node.slot, slotMap, pluginSlotMap).label;
    return <WidgetPlaceholder widgetConfig={widget} label={label} />;
  }

  if (node.tabs && node.tabs.length > 0) {
    const firstTab = node.tabs[0];
    const widget = firstTab.slot ? getWidgetConfig(firstTab.slot, uiSlots, slotMap, pluginSlotMap) : { widgetType: 'text-single' as const };
    const label = firstTab.slot ? resolveSlot(firstTab.slot, slotMap, pluginSlotMap).label : firstTab.label;
    return (
      <div className='wywp-composite-tabs'>
        <div className='wywp-composite-tab-bar'>
          {node.tabs.map((tab, idx) => (
            <span key={idx} className='wywp-composite-tab-chip'>
              {tab.label}
            </span>
          ))}
        </div>
        <WidgetPlaceholder widgetConfig={widget} label={label} />
      </div>
    );
  }

  if (node.direction && node.children) {
    const childTotalWeight = node.children.reduce((s, c) => s + (c.weight ?? 1), 0);
    return (
      <div className={`wywp-composite-container wywp-composite-container--${node.direction}`}>
        {node.children.map((child, idx) => {
          const pct = childTotalWeight > 0 ? ((child.weight ?? 1) / childTotalWeight) * 100 : 100 / node.children!.length;
          return (
            <div
              key={idx}
              className='wywp-composite-cell'
              style={node.direction === 'row' ? { flexBasis: `${pct}%`, flexGrow: child.weight ?? 1 } : { flex: child.weight ?? 1 }}
            >
              {renderCompositeNode(child, uiSlots, slotMap, pluginSlotMap)}
            </div>
          );
        })}
      </div>
    );
  }

  return null;
}

// ---------------------------------------------------------------------------
// TabContent
// ---------------------------------------------------------------------------

interface TabContentProps {
  tab: PluginUiTab;
  uiSlots: Record<string, WidgetConfig>;
  slotMap: Record<string, SlotDef>;
  pluginSlotMap: Record<string, PluginSlotDef>;
  gridCols?: number;
  selectedSlotId?: string | null;
  onSelectSlot?: (slotId: string | null) => void;
  onSlotsChange?: (slots: Array<{ id: string }>) => void;
  onCompositeLayoutChange?: (value: CompositePanelNode) => void;
  onCompositeTabPositionChange?: (pos: PluginUiTab['composite_tab_position']) => void;
}

function TabContent({
  tab,
  uiSlots,
  slotMap,
  pluginSlotMap,
  gridCols,
  selectedSlotId,
  onSelectSlot,
  onSlotsChange,
  onCompositeLayoutChange,
  onCompositeTabPositionChange,
}: TabContentProps) {
  const { t } = useTranslation();
  if (tab.layout !== 'composite' && tab.slots.length === 0) {
    return <div className="wywp-no-slots">{t('selfEvolutionRun.uiWysiwygNoSlots')}</div>;
  }

  // Editable canvas
  if (onSlotsChange && onCompositeLayoutChange && onCompositeTabPositionChange) {
    return (
      <UiEditorCanvas
        tab={tab}
        slotMap={slotMap}
        uiSlots={uiSlots}
        gridCols={gridCols}
        selectedSlotId={selectedSlotId ?? null}
        onSelectSlot={onSelectSlot ?? (() => {})}
        onSlotsChange={onSlotsChange}
        onCompositeLayoutChange={onCompositeLayoutChange}
        onCompositeTabPositionChange={onCompositeTabPositionChange}
      />
    );
  }

  // Composite read-only fallback
  if (tab.layout === 'composite') {
    if (tab.composite_layout?.direction) {
      return (
        <div className='wywp-layout-composite'>
          {renderCompositeNode(tab.composite_layout, uiSlots, slotMap, pluginSlotMap)}
        </div>
      );
    }
    return (
      <div className='wywp-layout-vertical'>
        {tab.slots.map((s) => {
          const widget = getWidgetConfig(s.id, uiSlots, slotMap, pluginSlotMap);
          const label = resolveSlot(s.id, slotMap, pluginSlotMap).label;
          return <WidgetPlaceholder key={s.id} widgetConfig={widget} label={label} />;
        })}
      </div>
    );
  }

  // Generic read-only fallback
  const layoutClass = `wywp-layout-${tab.layout ?? 'vertical'}`;
  const layoutStyle: React.CSSProperties = {};
  if (tab.layout === 'grid' && gridCols) {
    (layoutStyle as Record<string, unknown>)['--wywp-grid-cols'] = `repeat(${gridCols}, 1fr)`;
  }
  return (
    <div className={layoutClass} style={layoutStyle}>
      {tab.slots.map((s) => {
        const widget = getWidgetConfig(s.id, uiSlots, slotMap, pluginSlotMap);
        const label = resolveSlot(s.id, slotMap, pluginSlotMap).label;
        return <WidgetPlaceholder key={s.id} widgetConfig={widget} label={label} />;
      })}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Props
// ---------------------------------------------------------------------------

interface Props {
  pluginModel: PluginModel;
  activeTabId?: string;
  activeLayout?: PluginUiTab['layout'];
  activeGridCols?: number;
  editorMode?: boolean;
  selectedSlotId?: string | null;
  onSelectSlot?: (slotId: string | null) => void;
  autoEditTabId?: string;
  onAutoEditDone?: () => void;
  slotMap: Record<string, SlotDef>;
  onTabSelect?: (tabId: string) => void;
  onAddTab?: () => void;
  onRenameTab?: (tabId: string, label: string) => void;
  onDeleteTab?: (tabId: string) => void;
  onLayoutChange?: (layout: PluginUiTab['layout']) => void;
  onGridColsChange?: (gridCols: number | null) => void;
  onSlotsChange?: (slots: Array<{ id: string }>) => void;
  onCompositeLayoutChange?: (value: CompositePanelNode) => void;
  onCompositeTabPositionChange?: (pos: PluginUiTab['composite_tab_position']) => void;
  extraRightAction?: React.ReactNode;
}

export default function UiWysiwygPreview({
  pluginModel,
  activeTabId,
  activeLayout = 'vertical',
  activeGridCols,
  editorMode = false,
  selectedSlotId,
  onSelectSlot,
  autoEditTabId,
  onAutoEditDone,
  slotMap,
  onTabSelect,
  onAddTab,
  onRenameTab,
  onDeleteTab,
  onLayoutChange,
  onGridColsChange,
  onSlotsChange,
  onCompositeLayoutChange,
  onCompositeTabPositionChange,
  extraRightAction,
}: Props) {
  const { t } = useTranslation();
  const tabs = pluginModel.ui?.tabs ?? [];
  const uiSlots: Record<string, WidgetConfig> = (pluginModel.ui?.slots ?? {}) as Record<string, WidgetConfig>;
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

  useEffect(() => {
    if (!autoEditTabId) return;
    const tab = tabs.find((t) => t.id === autoEditTabId);
    if (tab) {
      startEdit(tab);
      onAutoEditDone?.();
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [autoEditTabId]);

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
          {t('selfEvolutionRun.uiWysiwygEmptyHint')}
        </div>
        {onAddTab && (
          <Button size="small" icon={<PlusOutlined />} onClick={onAddTab} style={{ marginTop: 12 }}>
            {t('selfEvolutionRun.uiWysiwygAddTab')}
          </Button>
        )}
      </div>
    );
  }

  return (
    <div className="wywp-root">
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
                        ...(onRenameTab ? [{ key: 'rename', label: t('selfEvolutionRun.uiWysiwygRenameTab') as React.ReactNode, onClick: ({ domEvent }: { domEvent: React.MouseEvent }) => { domEvent.stopPropagation(); startEdit(tab); } }] : []),
                        ...(onDeleteTab && tabs.length > 1 ? [{ key: 'delete', label: <span style={{ color: '#ff4d4f' }}>{t('selfEvolutionRun.uiWysiwygDeleteTab')}</span> as React.ReactNode, onClick: ({ domEvent }: { domEvent: React.MouseEvent }) => { domEvent.stopPropagation(); onDeleteTab(tabId); } }] : []),
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
            <Button type="text" size="small" icon={<PlusOutlined />} className="wywp-stepbar-add" onClick={onAddTab}>
              {t('selfEvolutionRun.uiWysiwygAddTab')}
            </Button>
          )}
        </div>

        <div className="wywp-stepbar-right">
          {onLayoutChange && (
            <Dropdown menu={{ items: layoutMenuItems }} trigger={['click']}>
              <Button size="small" className="wywp-layout-btn">
                {t('selfEvolutionRun.uiWysiwygLayoutLabel', { layout: LAYOUT_LABELS[activeLayout ?? 'vertical'] })}
              </Button>
            </Dropdown>
          )}
          {activeLayout === 'grid' && onGridColsChange && (
            <div className="wywp-grid-cols-control">
              <span className="wywp-grid-cols-label">{t('selfEvolutionRun.uiWysiwygGridColsLabel')}</span>
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
          {extraRightAction}
        </div>
      </div>

      <div className="wywp-content">
        {activeTab && (
          <TabContent
            key={activeTab.id}
            tab={activeTab}
            uiSlots={uiSlots}
            slotMap={slotMap}
            pluginSlotMap={pluginSlotMap}
            gridCols={activeGridCols}
            selectedSlotId={selectedSlotId}
            onSelectSlot={onSelectSlot}
            onSlotsChange={onSlotsChange}
            onCompositeLayoutChange={onCompositeLayoutChange}
            onCompositeTabPositionChange={onCompositeTabPositionChange}
          />
        )}
      </div>

      {!editorMode && (
        <div className="wywp-footer">
          <button type="button" className="wywp-btn wywp-btn--ghost">{t('selfEvolutionRun.uiWysiwygFooterRetry') }</button>
          <button type="button" className="wywp-btn wywp-btn--primary">{t('selfEvolutionRun.uiWysiwygFooterContinue')}</button>
        </div>
      )}
    </div>
  );
}
