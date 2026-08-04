import type { ReactNode } from "react";
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import MemberList from "./index";
import { MemberType } from "@/modules/knowledge/constants/common";
import { DatasetAclEnum, type Dataset } from "@/api/generated/knowledge-client";

const listMembersMock = vi.fn();
const updateMemberMock = vi.fn();
const deleteMemberMock = vi.fn();
const getDatasetMock = vi.fn();
const navigateMock = vi.fn();

vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>(
    "react-router-dom",
  );
  return { ...actual, useNavigate: () => navigateMock };
});

vi.mock("@/modules/knowledge/utils/request", () => ({
  MemberServiceApi: () => ({
    datasetMemberServiceListDatasetMembers: (...args: unknown[]) =>
      listMembersMock(...args),
    datasetMemberServiceUpdateDatasetMember: (...args: unknown[]) =>
      updateMemberMock(...args),
    datasetMemberServiceDeleteDatasetMember: (...args: unknown[]) =>
      deleteMemberMock(...args),
  }),
  KnowledgeBaseServiceApi: () => ({
    datasetServiceGetDataset: (...args: unknown[]) => getDatasetMock(...args),
  }),
}));

vi.mock("../AddUserModal", () => ({
  default: () => null,
}));

// `@/components/ui`'s barrel file re-exports RenderPdf, which pulls in
// pdfjs-dist and crashes in jsdom (no DOMMatrix). MemberList only needs
// ListPageTable, so stub the barrel with a minimal table implementation.
vi.mock("@/components/ui", () => ({
  ListPageTable: (props: {
    dataSource: Array<Record<string, unknown>>;
    columns: Array<{
      title?: string;
      dataIndex?: string;
      key?: string;
      render?: (value: unknown, record: Record<string, unknown>) => unknown;
    }>;
  }) => (
    <table>
      <tbody>
        {props.dataSource.map((record, rowIndex) => (
          <tr key={rowIndex}>
            {props.columns.map((col, colIndex) => (
              <td key={col.key || col.dataIndex || colIndex}>
                {col.render
                  ? (col.render(
                      col.dataIndex ? record[col.dataIndex] : record,
                      record,
                    ) as ReactNode)
                  : col.dataIndex
                    ? (record[col.dataIndex] as ReactNode)
                    : null}
              </td>
            ))}
          </tr>
        ))}
      </tbody>
    </table>
  ),
}));

const writableDetail: Dataset = {
  dataset_id: "ds-1",
  display_name: "My KB",
  acl: [DatasetAclEnum.DatasetWrite],
};

describe("MemberList", () => {
  beforeEach(() => {
    listMembersMock.mockReset().mockResolvedValue({
      data: {
        dataset_members: [
          {
            dataset_id: "ds-1",
            user_id: "u1",
            user: "Alice",
            role: { role: "dataset_maintainer" },
            is_creator: false,
          },
        ],
      },
    });
    updateMemberMock.mockReset().mockResolvedValue({});
    deleteMemberMock.mockReset().mockResolvedValue({});
    getDatasetMock.mockReset().mockResolvedValue({ data: writableDetail });
    navigateMock.mockReset();
  });

  it("loads and renders members for the given memberType", async () => {
    renderWithProviders(<MemberList memberType={MemberType.USER} detail={writableDetail} />);

    await waitFor(() => {
      expect(screen.getByText("Alice")).toBeInTheDocument();
    });
    expect(listMembersMock).toHaveBeenCalledWith(
      expect.objectContaining({ dataset: "ds-1" }),
    );
  });

  it("redirects to the knowledge base list when the viewer lacks write access", () => {
    renderWithProviders(
      <MemberList memberType={MemberType.USER} detail={{ ...writableDetail, acl: [] }} />,
    );

    expect(navigateMock).toHaveBeenCalledWith({ pathname: "/lib/knowledge/list" });
    expect(listMembersMock).not.toHaveBeenCalled();
  });

  it("filters the member list by the search input", async () => {
    listMembersMock.mockResolvedValue({
      data: {
        dataset_members: [
          { dataset_id: "ds-1", user_id: "u1", user: "Alice", role: { role: "dataset_maintainer" } },
          { dataset_id: "ds-1", user_id: "u2", user: "Bob", role: { role: "dataset_user" } },
        ],
      },
    });

    renderWithProviders(<MemberList memberType={MemberType.USER} detail={writableDetail} />);

    await waitFor(() => {
      expect(screen.getByText("Alice")).toBeInTheDocument();
      expect(screen.getByText("Bob")).toBeInTheDocument();
    });

    const search = screen.getByPlaceholderText("knowledge.userName");
    fireEvent.change(search, { target: { value: "Bob" } });
    fireEvent.keyDown(search, { key: "Enter", code: "Enter" });

    await waitFor(() => {
      expect(screen.queryByText("Alice")).not.toBeInTheDocument();
      expect(screen.getByText("Bob")).toBeInTheDocument();
    });
  });

  it("deletes a non-creator member after confirming", async () => {
    renderWithProviders(<MemberList memberType={MemberType.USER} detail={writableDetail} />);

    await waitFor(() => {
      expect(screen.getByText("Alice")).toBeInTheDocument();
    });

    fireEvent.click(screen.getByRole("button", { name: "common.delete" }));

    await waitFor(() => {
      expect(screen.getAllByText("knowledge.deletePermissionTitle").length).toBeGreaterThan(0);
    });
    fireEvent.click(screen.getByRole("button", { name: /common\.ok|ok/i }));

    await waitFor(() => {
      expect(deleteMemberMock).toHaveBeenCalledWith(
        expect.objectContaining({ dataset: "ds-1", userId: "u1" }),
      );
    });
  });
});
