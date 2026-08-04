import { describe, it, expect, vi, beforeEach } from "vitest";
import { createRef } from "react";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { renderWithProviders } from "@/test/testUtils";
import AddUserModal, { type IAddUserModalRef } from "./index";
import { MemberType } from "@/modules/knowledge/constants/common";

const batchAddMemberMock = vi.fn();
const listUsersMock = vi.fn();
const axiosGetMock = vi.fn();

vi.mock("@/modules/knowledge/utils/request", () => ({
  MemberServiceApi: () => ({
    datasetMemberServiceBatchAddDatasetMember: (...args: unknown[]) =>
      batchAddMemberMock(...args),
  }),
}));

vi.mock("@/modules/signin/utils/request", () => ({
  createUserApi: () => ({
    listUsersApiAuthserviceUserGet: (...args: unknown[]) => listUsersMock(...args),
  }),
}));

vi.mock("@/components/request", () => ({
  axiosInstance: { get: (...args: unknown[]) => axiosGetMock(...args) },
  BASE_URL: "http://localhost",
}));

describe("AddUserModal", () => {
  beforeEach(() => {
    batchAddMemberMock.mockReset().mockResolvedValue({});
    listUsersMock.mockReset().mockResolvedValue({
      data: { users: [{ user_id: "u1", display_name: "Alice" }] },
    });
    axiosGetMock.mockReset().mockResolvedValue({ data: { groups: [] } });
  });

  it("is hidden until handleOpen is called via the ref", () => {
    const ref = createRef<IAddUserModalRef>();
    renderWithProviders(<AddUserModal ref={ref} onOk={vi.fn()} />);

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("opens with the user title and loads the user list for MemberType.USER", async () => {
    const ref = createRef<IAddUserModalRef>();
    renderWithProviders(<AddUserModal ref={ref} onOk={vi.fn()} />);

    ref.current?.handleOpen({ dataset_id: "ds-1", memberType: MemberType.USER });

    await waitFor(() => {
      expect(screen.getByRole("dialog")).toBeInTheDocument();
    });
    expect(screen.getByText("knowledge.addUser")).toBeInTheDocument();
    await waitFor(() => expect(listUsersMock).toHaveBeenCalled());
  });

  it("opens with the group title and fetches groups for MemberType.GROUP", async () => {
    const ref = createRef<IAddUserModalRef>();
    renderWithProviders(<AddUserModal ref={ref} onOk={vi.fn()} />);

    ref.current?.handleOpen({ dataset_id: "ds-1", memberType: MemberType.GROUP });

    await waitFor(() => {
      expect(screen.getByText("knowledge.addGroup")).toBeInTheDocument();
    });
    await waitFor(() => expect(axiosGetMock).toHaveBeenCalled());
  });

  it("submits the selected member and role, then calls onOk", async () => {
    const onOk = vi.fn();
    const ref = createRef<IAddUserModalRef>();
    renderWithProviders(<AddUserModal ref={ref} onOk={onOk} />);

    ref.current?.handleOpen({ dataset_id: "ds-1", memberType: MemberType.USER });
    await waitFor(() => expect(listUsersMock).toHaveBeenCalled());

    // Select a user in the multiple-select.
    const selects = screen.getAllByRole("combobox");
    fireEvent.mouseDown(selects[0]);
    await waitFor(() => {
      expect(screen.getByText("Alice")).toBeInTheDocument();
    });
    fireEvent.click(screen.getByText("Alice"));

    fireEvent.click(screen.getByRole("button", { name: "common.save" }));

    await waitFor(() => {
      expect(batchAddMemberMock).toHaveBeenCalledWith(
        expect.objectContaining({
          dataset: "ds-1",
          batchAddDatasetMemberRequest: expect.objectContaining({
            user_id_list: ["u1"],
          }),
        }),
      );
    });
    await waitFor(() => expect(onOk).toHaveBeenCalled());
  });
});
