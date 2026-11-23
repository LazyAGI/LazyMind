import { fireEvent, render, screen } from "@testing-library/react";
import { Modal, message } from "antd";
import { afterEach, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { showBuiltinSkillInstallError } from "./skillInstallFeedback";

afterEach(() => vi.restoreAllMocks());

it("shows one short error and opens the source details on request", async () => {
  await i18n.changeLanguage("zh-CN");
  const closeToast = vi.fn();
  const showToast = vi.spyOn(message, "error").mockReturnValue(closeToast as never);
  const showDetails = vi.spyOn(Modal, "info").mockReturnValue({} as never);
  showBuiltinSkillInstallError({
    response: {
      status: 502,
      data: {
        code: 2003147,
        data: {
          source_failures: [
            { source: "origin", reason: "unreachable" },
            { source: "modelscope", reason: "missing" },
            { source: "hugging_face", reason: "invalid_package" },
          ],
        },
      },
    },
  }, "bsk_demo");

  expect(showToast).toHaveBeenCalledTimes(1);
  const toast = showToast.mock.calls[0][0];
  const { unmount } = render(toast && typeof toast === "object" && "content" in toast ? toast.content : null);
  expect(screen.getByText("技能包下载失败")).toBeTruthy();
  expect(screen.queryByText(/ModelScope/)).toBeNull();
  expect(showDetails).not.toHaveBeenCalled();

  fireEvent.click(screen.getByRole("button", { name: "查看详情" }));
  expect(closeToast).toHaveBeenCalledTimes(1);
  expect(showDetails).toHaveBeenCalledTimes(1);
  unmount();
  render(showDetails.mock.calls[0][0].content);
  expect(screen.getAllByRole("listitem").map((item) => item.textContent)).toEqual([
    "原下载源：无法连接下载源",
    "ModelScope：安装包不存在",
    "Hugging Face：安装包校验不通过",
  ]);
});
