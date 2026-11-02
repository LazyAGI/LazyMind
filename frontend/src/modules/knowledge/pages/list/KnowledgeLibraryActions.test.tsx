import type { ComponentProps } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { ConfigProvider } from "antd";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DatasetAclEnum, DatasetTypeEnum, type Dataset } from "@/api/generated/knowledge-client";
import KnowledgeLibraryActions from "./KnowledgeLibraryActions";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    t: (key: string) => ({
      "common.edit": "编辑",
      "common.delete": "删除",
      "knowledge.authorize": "授权",
    } as Record<string, string>)[key] || key,
  }),
}));

const writable: Dataset = { dataset_id: "library-one", display_name: "研究资料", type: DatasetTypeEnum.DatasetTypeText, acl: [DatasetAclEnum.DatasetRead, DatasetAclEnum.DatasetWrite] };
const readonly: Dataset = { ...writable, acl: [DatasetAclEnum.DatasetRead] };

function mount(overrides: Partial<ComponentProps<typeof KnowledgeLibraryActions>> = {}) {
  const onEdit = vi.fn();
  const onAuthorize = vi.fn();
  const onDelete = vi.fn();
  const onRowClick = vi.fn();
  const props = { dataset: writable, onEdit, onAuthorize, onDelete, ...overrides };
  render(
    <ConfigProvider button={{ autoInsertSpace: false }} theme={{ token: { motion: false } }}>
      <div onClick={onRowClick}>
        <KnowledgeLibraryActions {...props} />
      </div>
    </ConfigProvider>,
  );
  return { ...props, onRowClick };
}

async function openActions() {
  fireEvent.click(screen.getByRole("button", { name: "研究资料 的操作" }));
  return screen.findByRole("menu");
}

afterEach(cleanup);

describe("knowledge library actions", () => {
  it.each([
    ["编辑", "onEdit"],
    ["授权", "onAuthorize"],
    ["删除", "onDelete"],
  ] as const)("runs %s without navigating the library row", async (label, callback) => {
    const props = mount();
    await openActions();
    expect(props.onRowClick).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("menuitem", { name: new RegExp(label) }));
    expect(props[callback]).toHaveBeenCalledTimes(1);
    expect(props.onRowClick).not.toHaveBeenCalled();
    for (const key of ["onEdit", "onAuthorize", "onDelete"] as const) {
      if (key !== callback) expect(props[key]).not.toHaveBeenCalled();
    }
  });

  it("omits authorization when that surface is unavailable", async () => {
    mount({ onAuthorize: undefined });
    await openActions();
    expect(screen.queryByRole("menuitem", { name: /授权/ })).not.toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: /编辑/ })).toBeInTheDocument();
    expect(screen.getByRole("menuitem", { name: /删除/ })).toBeInTheDocument();
  });

  it.each(["bib", "ris", "csv"] as const)("allows read-only citation export as %s without management actions", async (format) => {
    const onExport = vi.fn();
    const props = mount({ dataset: readonly, onExport });
    await openActions();
    expect(screen.queryByRole("menuitem", { name: /编辑/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: /授权/ })).not.toBeInTheDocument();
    expect(screen.queryByRole("menuitem", { name: /删除/ })).not.toBeInTheDocument();
    const exportMenu = screen.getByRole("menuitem", { name: /导出文献/ });
    fireEvent.mouseEnter(exportMenu);
    const label = { bib: "BibTeX", ris: "RIS", csv: "CSV" }[format];
    fireEvent.click(await screen.findByRole("menuitem", { name: label }));
    expect(onExport).toHaveBeenCalledTimes(1);
    expect(onExport).toHaveBeenCalledWith(format);
    expect(props.onRowClick).not.toHaveBeenCalled();
  });

  it("renders no action trigger when the user has no available actions", () => {
    mount({ dataset: readonly, onExport: undefined });
    expect(screen.queryByRole("button", { name: "研究资料 的操作" })).not.toBeInTheDocument();
  });

  it("does not expose library exports to upload-only members", () => {
    mount({ dataset: { ...writable, acl: [DatasetAclEnum.DatasetUpload] }, onExport: vi.fn() });
    expect(screen.queryByRole("button", { name: "研究资料 的操作" })).not.toBeInTheDocument();
  });
});
