import { useState } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createMemoryRouter, RouterProvider, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it } from "vitest";
import { getKnowledgeListOrigin, useKnowledgeListNavigation, useKnowledgeListReturn } from "./useKnowledgeListNavigation";

const listUrl = "/lib/knowledge/list?source=official&keyword=法律";

function List() {
  const [ready, setReady] = useState(false);
  const [, setRevision] = useState(0);
  const { rootRef, openDetail } = useKnowledgeListNavigation(ready);
  const location = useLocation();
  return <div ref={rootRef}>
    <span>{location.search}</span>
    <div className="list-page-table-scroll" />
    <button onClick={() => openDetail("a")}>打开 A</button>
    <button onClick={() => openDetail("b")}>打开 B</button>
    <button onClick={() => setReady(true)}>列表已加载</button>
    <button onClick={() => setRevision((value) => value + 1)}>后台刷新</button>
  </div>;
}

function Detail() {
  const { returnToList } = useKnowledgeListReturn();
  return <>
    <button onClick={() => returnToList()}>返回列表</button>
    <button onClick={() => returnToList(true)}>删除后返回</button>
  </>;
}

function mount(initialEntries: NonNullable<Parameters<typeof createMemoryRouter>[1]>["initialEntries"] = [listUrl]) {
  const router = createMemoryRouter([
    { path: "/lib/knowledge/list", element: <List /> },
    { path: "/lib/knowledge/detail/:id", element: <Detail /> },
    { path: "/outside", element: <div>其他页面</div> },
  ], { initialEntries });
  const rendered = render(<div className="sub-app-container"><RouterProvider router={router} /></div>);
  const container = rendered.container.firstElementChild as HTMLElement;
  Object.defineProperties(container, {
    scrollHeight: { configurable: true, value: 1500 },
    clientHeight: { configurable: true, value: 500 },
  });
  return { router, container };
}

afterEach(cleanup);

describe("knowledge list history navigation", () => {
  it.each(["页面按钮", "浏览器后退"])("restores the original history entry via %s after loading", async (method) => {
    const { router, container } = mount();
    container.scrollTop = 480;
    const table = document.querySelector(".list-page-table-scroll") as HTMLElement;
    table.scrollLeft = 120;
    fireEvent.click(screen.getByText("打开 A"));
    await waitFor(() => expect(router.state.location.pathname).toBe("/lib/knowledge/detail/a"));
    expect(router.state.location.state.knowledgeListOrigin.url).toBe(listUrl);
    container.scrollTop = 0;
    if (method === "页面按钮") fireEvent.click(screen.getByText("返回列表"));
    else await act(async () => { await router.navigate(-1); });
    await screen.findByText("打开 A");
    expect(router.state.location.pathname + router.state.location.search).toBe(listUrl);
    expect(container.scrollTop).toBe(0);
    const restoredTable = document.querySelector(".list-page-table-scroll") as HTMLElement;
    Object.defineProperties(restoredTable, {
      scrollWidth: { value: 1200 }, clientWidth: { value: 600 },
    });
    fireEvent.click(screen.getByText("列表已加载"));
    expect(container.scrollTop).toBe(480);
    expect(restoredTable.scrollLeft).toBe(120);
    // A background render must not move the user back to the saved position.
    container.scrollTop = 700;
    fireEvent.click(screen.getByText("后台刷新"));
    expect(container.scrollTop).toBe(700);
  });

  it("keeps repeated A/B opens, Back and Forward on the original source list", async () => {
    const { router } = mount();
    for (const name of ["A", "B", "A"]) {
      fireEvent.click(await screen.findByText(`打开 ${name}`));
      await screen.findByText("返回列表");
      await act(async () => { await router.navigate(-1); });
      await screen.findByText("打开 A");
      expect(router.state.location.pathname + router.state.location.search).toBe(listUrl);
      await act(async () => { await router.navigate(1); });
      expect(router.state.location.pathname).toBe(`/lib/knowledge/detail/${name.toLowerCase()}`);
      fireEvent.click(await screen.findByText("返回列表"));
    }
  });

  it("replaces a deleted detail with its source and scroll snapshot", async () => {
    const { router, container } = mount();
    container.scrollTop = 900;
    fireEvent.click(screen.getByText("打开 A"));
    fireEvent.click(await screen.findByText("删除后返回"));
    await screen.findByText("打开 A");
    expect(router.state.location.pathname + router.state.location.search).toBe(listUrl);
    expect(router.state.location.state.knowledgeListScroll.top).toBe(900);
    expect(router.state.historyAction).toBe("REPLACE");
  });

  it("falls back to the list for direct detail entry instead of leaving the app", async () => {
    const { router } = mount(["/outside", "/lib/knowledge/detail/a?from=chat"]);
    fireEvent.click(await screen.findByText("返回列表"));
    await screen.findByText("打开 A");
    expect(router.state.location.pathname).toBe("/lib/knowledge/list");
  });

  it("clamps a saved scroll offset to the new content height", async () => {
    const { container } = mount([{ pathname: "/lib/knowledge/list", state: {
      knowledgeListScroll: { url: "/lib/knowledge/list", top: 5000, left: 0 },
    } }]);
    fireEvent.click(screen.getByText("列表已加载"));
    expect(container.scrollTop).toBe(1000);
  });

  it.each(["https://evil.test/", "//evil.test", "/lib/knowledge/listing", "/other"])("rejects return target %s", (url) => {
    expect(getKnowledgeListOrigin({ knowledgeListOrigin: { url, scroll: { url, top: 0, left: 0 } } })).toBeUndefined();
  });
});
