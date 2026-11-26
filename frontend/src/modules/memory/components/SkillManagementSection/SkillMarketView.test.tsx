import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { MarketSkillAsset } from "./skillMarketMockData";
import SkillMarketView from "./SkillMarketView";

const skill = (id: string, provider?: string): MarketSkillAsset => ({
  id,
  name: id,
  description: `${id} description`,
  category: "writing",
  tags: [],
  content: "",
  marketSource: "builtin",
  provider,
});

const t = (key: string, values?: Record<string, unknown>) => {
  if (key === 'admin.memorySkillMarketPageCategoryCount') return `本页 ${values?.count} 个`;
  if (key === 'common.totalItems') return `共 ${values?.total} 条`;
  if (key === 'admin.memorySkillMarketPageSize') return `${values?.count} 条起/页`;
  return ({
    'admin.memorySkillMarketCategories.writing': '写作与文档',
    'admin.memorySkillMarketCategories.research': '检索与研究',
    'admin.memorySkillMarketCategories.office': '数据与办公',
    'admin.memorySkillMarketDetail': '详情',
    'admin.memorySkillMarketInstall': '安装',
    'admin.memorySkillInstalledBadge': '已安装',
    'admin.memorySkillMarketDelete': '删除',
    'admin.memorySkillMarketPageSizeLabel': '每页数量',
  } as Record<string, string>)[key] || key;
};

const props = () => ({
  t, loading: false, skillAssets: [], installedSkills: [], isAdmin: false,
  onInstall: vi.fn(), onDetail: vi.fn(), onDelete: vi.fn(),
  page: 1, pageSize: 12, total: 0, pageCount: 2, onColumnsChange: vi.fn(), onPageChange: vi.fn(),
});

describe("SkillMarketView", () => {
  it('uses the actual page count while showing the actual skill total and retaining the page-size target', () => {
    const callbacks = props();
    render(<SkillMarketView {...callbacks} skillAssets={[skill('writer')]} total={89} pageCount={6} />);
    expect(screen.getByText('共 89 条')).toBeInTheDocument();
    expect(screen.getByTitle('6', { exact: true })).toBeInTheDocument();
    expect(screen.queryByTitle('8', { exact: true })).not.toBeInTheDocument();
    fireEvent.click(screen.getByTitle('6', { exact: true }));
    expect(callbacks.onPageChange).toHaveBeenCalledWith(6, 12);
    fireEvent.mouseDown(screen.getByRole('combobox', { name: '每页数量' }));
    fireEvent.click(screen.getByText('20 条起/页'));
    expect(callbacks.onPageChange).toHaveBeenCalledWith(1, 20);
  });

  it('reports the actual CSS grid column count initially and after resizing, then disconnects', () => {
    const callbacks = props();
    let tracks = '240px 240px 240px';
    let resize!: () => void;
    const disconnect = vi.fn();
    const original = window.getComputedStyle;
    const style = vi.spyOn(window, 'getComputedStyle').mockImplementation((element, pseudo) => {
      const result = original(element, pseudo);
      if (element.classList.contains('memory-skill-recommend-grid')) {
        Object.defineProperty(result, 'gridTemplateColumns', { value: tracks });
      }
      return result;
    });
    vi.stubGlobal('ResizeObserver', class {
      constructor(private callback: () => void) {}
      observe(element: Element) { if (element.classList.contains('memory-skill-market-scroll')) resize = this.callback; }
      unobserve() {}
      disconnect = disconnect;
    });
    try {
      const { unmount } = render(<SkillMarketView {...callbacks} skillAssets={[skill('writer')]} />);
      expect(callbacks.onColumnsChange).toHaveBeenLastCalledWith(3);
      tracks = '320px 320px';
      act(() => resize());
      expect(callbacks.onColumnsChange).toHaveBeenLastCalledWith(2);
      unmount();
      expect(disconnect).toHaveBeenCalled();
    } finally {
      style.mockRestore();
      vi.unstubAllGlobals();
    }
  });

  it('groups each skill once by its category, using the agreed category order and stable item order despite multiple tags', () => {
    const first = { ...skill('writer-a'), tags: ['report', 'search'] };
    const second = { ...skill('finder'), category: 'search', tags: ['report'] };
    const third = { ...skill('writer-b'), tags: ['report', 'writing'] };
    render(<SkillMarketView {...props()} skillAssets={[first, second, third]} total={89} />);
    expect(screen.getAllByRole('heading', { level: 3 }).map(node => node.textContent)).toEqual(['检索与研究', '写作与文档']);
    const writing = within(screen.getByRole('region', { name: '写作与文档' }));
    expect(writing.getAllByRole('article').map(node => within(node).getByRole('button', { name: /writer-/ }).textContent)).toEqual(['writer-a', 'writer-b']);
    expect(writing.getByText('本页 2 个')).toBeInTheDocument();
    expect(screen.getAllByRole('article')).toHaveLength(3);
    expect(screen.getByText('共 89 条')).toBeInTheDocument();
  });

  it('keeps custom and uncategorized skills visible without treating tags as duplicate categories', () => {
    render(<SkillMarketView {...props()} skillAssets={[
      { ...skill('custom'), category: '团队规范' },
      { ...skill('untagged'), category: '', tags: ['report'] },
    ]} />);
    expect(screen.getByRole('region', { name: '数据与办公' })).toBeInTheDocument();
    expect(screen.getAllByRole('article')).toHaveLength(2);
  });

  it('preserves detail, install, installed state and administrator deletion for the matching skill', () => {
    const callbacks = props();
    const available = skill('available');
    const installed = { ...skill('installed'), installed: true };
    const admin = { ...skill('admin-item'), marketSource: 'admin' as const };
    const assets = [available, installed, admin];
    const { rerender } = render(<SkillMarketView {...callbacks} isAdmin skillAssets={assets} />);
    const availableCard = within(screen.getByRole('button', { name: 'available' }).closest('article')!);
    fireEvent.click(availableCard.getByRole('button', { name: '详情' }));
    fireEvent.click(availableCard.getByRole('button', { name: 'available' }));
    expect(callbacks.onDetail).toHaveBeenNthCalledWith(1, available);
    expect(callbacks.onDetail).toHaveBeenNthCalledWith(2, available);
    fireEvent.click(availableCard.getByRole('button', { name: '安装' }));
    expect(callbacks.onInstall).toHaveBeenCalledWith(available);
    expect(screen.getByRole('button', { name: /已安装/ })).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: /删\s*除/ }));
    expect(callbacks.onDelete).toHaveBeenCalledWith(admin);
    rerender(<SkillMarketView {...callbacks} skillAssets={assets} />);
    expect(screen.queryByRole('button', { name: /删\s*除/ })).not.toBeInTheDocument();
  });

  it('uses the existing paging callback and replaces groups with the next page results', () => {
    const callbacks = props();
    const { rerender } = render(<SkillMarketView {...callbacks} skillAssets={[skill('writer')]} total={24} />);
    fireEvent.click(screen.getByTitle('2'));
    expect(callbacks.onPageChange).toHaveBeenCalledWith(2, 12);
    rerender(<SkillMarketView {...callbacks} page={2} skillAssets={[{ ...skill('finder'), category: 'search' }]} total={24} />);
    expect(screen.queryByRole('region', { name: '写作与文档' })).not.toBeInTheDocument();
    expect(screen.getByRole('region', { name: '检索与研究' })).toBeInTheDocument();
  });

  it("shows configured providers and preserves the builtin fallback", () => {
    render(
      <SkillMarketView
        t={(key) => key === "admin.memorySkillSourceBuiltin" ? "平台内置" : key}
        loading={false}
        skillAssets={[skill("qwen-skill", "千问办公"), skill("legacy-skill")]}
        installedSkills={[]}
        isAdmin={false}
        onInstall={vi.fn()}
        onDetail={vi.fn()}
        onDelete={vi.fn()}
        page={1}
        pageSize={8}
        total={0}
        pageCount={0}
        onColumnsChange={vi.fn()}
        onPageChange={vi.fn()}
      />,
    );

    expect(screen.getByText("千问办公")).toBeInTheDocument();
    expect(screen.getByText("平台内置")).toBeInTheDocument();
  });
});
