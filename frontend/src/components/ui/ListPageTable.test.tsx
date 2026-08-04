import { describe, expect, it } from "vitest";
import { renderWithProviders, screen } from "@/test/testUtils";
import ListPageTable from "./ListPageTable";

const columns = [{ title: "Name", dataIndex: "name", key: "name" }];
const dataSource = [{ key: "1", name: "Item A" }, { key: "2", name: "Item B" }];

describe("ListPageTable", () => {
  it("renders rows and columns without crashing", () => {
    renderWithProviders(<ListPageTable dataSource={dataSource} columns={columns} />);
    expect(screen.getByText("Name")).toBeTruthy();
    expect(screen.getByText("Item A")).toBeTruthy();
    expect(screen.getByText("Item B")).toBeTruthy();
  });

  it("renders the optional title node when provided", () => {
    renderWithProviders(
      <ListPageTable dataSource={dataSource} columns={columns} title="My Table" />,
    );
    expect(screen.getByText("My Table")).toBeTruthy();
  });

  it("does not render a title wrapper when title is omitted", () => {
    const { container } = renderWithProviders(
      <ListPageTable dataSource={dataSource} columns={columns} />,
    );
    expect(container.querySelector(".list-page-table-title")).toBeNull();
  });

  it("applies a min-width to the inner wrapper when scroll.x is set", () => {
    const { container } = renderWithProviders(
      <ListPageTable dataSource={dataSource} columns={columns} scroll={{ x: 800 }} />,
    );
    const inner = container.querySelector(".list-page-table-inner") as HTMLElement;
    expect(inner.style.minWidth).toBe("800px");
  });
});
