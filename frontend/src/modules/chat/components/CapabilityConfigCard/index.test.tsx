import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import CapabilityConfigCard from "./index";

const mocks = vi.hoisted(() => ({
  navigate: vi.fn(),
}));

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (key: string) => key }),
}));

vi.mock("react-router-dom", () => ({
  useLocation: () => ({
    pathname: "/agent/chat/home/conversation-1",
    search: "",
  }),
  useNavigate: () => mocks.navigate,
}));

const detail = {
  status: "blocked" as const,
  workflow: "CREATE_NEW",
  required: ["image_generator"],
  missing: [{
    id: "image_generator",
    label: "文生图模型",
    available: false as const,
    settings_url: "/settings?section=models",
    reason: "尚未配置文生图模型。",
  }],
  message: "当前任务缺少：文生图模型。",
};

describe("CapabilityConfigCard", () => {
  beforeEach(() => {
    mocks.navigate.mockReset();
  });

  it("opens the exact configuration target and returns only on user action", () => {
    const onContinue = vi.fn();
    render(
      <CapabilityConfigCard detail={detail} onContinue={onContinue} />,
    );

    expect(screen.getByText("文生图模型")).toBeInTheDocument();
    expect(screen.queryByRole("button", {
      name: "chat.configureRequiredCapability",
    })).not.toBeInTheDocument();
    expect(onContinue).not.toHaveBeenCalled();

    fireEvent.click(
      screen.getByRole("button", { name: /chat\.configureThisCapability/ }),
    );
    expect(mocks.navigate).toHaveBeenCalledWith(
      "/settings?section=models&target=image_generator&return_to=%2Fagent%2Fchat%2Fhome%2Fconversation-1",
    );
    expect(onContinue).not.toHaveBeenCalled();

    fireEvent.click(
      screen.getByRole("button", { name: "chat.continueAfterConfiguration" }),
    );
    expect(onContinue).toHaveBeenCalledTimes(1);
  });
});
