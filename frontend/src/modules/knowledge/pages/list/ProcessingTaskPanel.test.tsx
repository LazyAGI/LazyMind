import { fireEvent, render, screen, within, cleanup } from "@testing-library/react";
import { afterEach, beforeAll, expect, it, vi } from "vitest";
import { installDesktopTestDOM } from "@/test/desktopResourceFixtures";
import ProcessingTaskPanel from "./ProcessingTaskPanel";
import type { ProcessingTasksQuery } from "./useProcessingTasks";

beforeAll(installDesktopTestDOM);
afterEach(cleanup);
function query(overrides: Partial<ProcessingTasksQuery> = {}): ProcessingTasksQuery {
 return { data: {items:[{id:"task-1",dataset_id:"library",dataset_name:"论文集",document_id:"doc",document_name:"paper.pdf",level:"parsed",status:"failed",created_at:"2026-11-12T00:00:00Z",error:"解析失败，请重试",stages:[{name:"parse",status:"failed"},{name:"chunk",status:"not_requested"},{name:"index",status:"not_requested"}]}],counts:{active:0,failed:1},unfinished_count:0,total:1,page:1,page_size:20}, filters:{status:"all",level:"",q:"",page:1},setFilters:vi.fn(),loading:false,queryFailed:false,refresh:vi.fn(),...overrides };
}
it("shows errors and requested stages without inventing progress", () => {
 render(<ProcessingTaskPanel query={query()} />);
 expect(screen.getByText("解析失败，请重试")).toBeVisible();
 fireEvent.click(screen.getByRole("button",{name:"展开 paper.pdf"}));
 expect(screen.getAllByText("未请求")).toHaveLength(2);
 expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
 expect(screen.getByRole("link",{name:"前往资料库处理"})).toHaveAttribute("href","/lib/knowledge/detail/library");
});
it("distinguishes failed query from empty state and retains old rows", () => {
 const current=query({queryFailed:true}); render(<ProcessingTaskPanel query={current} />);
 expect(screen.getByText(/以下为上次读取的结果/)).toBeVisible();
 expect(screen.getByText("paper.pdf")).toBeVisible();
 expect(screen.getByText("状态待更新")).toBeVisible();
 fireEvent.click(screen.getByRole("button",{name:/重\s*试/}));expect(current.refresh).toHaveBeenCalledOnce();
});
it("changes status filters and resets pagination", () => {
 const current=query();render(<ProcessingTaskPanel query={current} />);
 fireEvent.click(within(screen.getByRole("tablist",{name:"按任务状态筛选"})).getByRole("tab",{name:/进行中/}));
 const updater=vi.mocked(current.setFilters).mock.calls[0][0] as (value:typeof current.filters)=>typeof current.filters;
 expect(updater({...current.filters,page:4})).toEqual({...current.filters,status:"active",page:1});
});
it("shows loading before an empty result", () => {
 render(<ProcessingTaskPanel query={query({data:undefined,loading:true})} />);
 expect(screen.queryByText("暂无文档处理任务")).not.toBeInTheDocument();
});
it("restores a retained search when switching back to the document tab", () => {
 render(<ProcessingTaskPanel query={query({filters:{status:"all",level:"",q:"paper",page:1}})} />);
 expect(screen.getByRole("searchbox",{name:"搜索文档或资料库"})).toHaveValue("paper");
});
