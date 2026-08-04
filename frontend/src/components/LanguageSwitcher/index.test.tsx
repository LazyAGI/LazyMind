import { beforeEach, describe, expect, it, vi } from "vitest";
import { fireEvent, renderWithProviders, screen } from "@/test/testUtils";
import LanguageSwitcher from "./index";

vi.mock("./index.scss", () => ({}));

const changeLanguageMock = vi.hoisted(() => vi.fn());
vi.mock("react-i18next", async (importOriginal) => {
  const actual = await importOriginal<typeof import("react-i18next")>();
  return {
    ...actual,
    useTranslation: () => ({
      i18n: { language: "zh-CN", changeLanguage: changeLanguageMock },
    }),
  };
});

vi.mock("@/i18n", () => ({
  LANGUAGES: [
    { value: "zh-CN", label: "中文" },
    { value: "en-US", label: "English" },
  ],
}));

beforeEach(() => {
  changeLanguageMock.mockReset();
  localStorage.clear();
});

describe("LanguageSwitcher", () => {
  it("renders the current language as the selected value", () => {
    renderWithProviders(<LanguageSwitcher />);
    expect(screen.getByText("中文")).toBeInTheDocument();
  });

  it("changes the language and persists it to localStorage when a new option is chosen", () => {
    renderWithProviders(<LanguageSwitcher />);
    const combobox = screen.getByRole("combobox");
    fireEvent.mouseDown(combobox);

    const option = screen
      .getAllByText("English")
      .find((el) => el.className.includes("ant-select-item-option-content"));
    expect(option).toBeDefined();
    fireEvent.click(option as HTMLElement);

    expect(changeLanguageMock).toHaveBeenCalledWith("en-US");
    expect(localStorage.getItem("i18n_language")).toBe("en-US");
  });
});
