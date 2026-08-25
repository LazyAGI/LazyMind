import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import LanguageSwitcher from "./index";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({
    i18n: {
      language: "zh-CN",
      changeLanguage: vi.fn(),
    },
    t: (key: string) => key,
  }),
}));

vi.mock("@/i18n", () => ({
  LANGUAGES: [
    { label: "中文", value: "zh-CN" },
    { label: "English", value: "en-US" },
  ],
}));

describe("LanguageSwitcher", () => {
  it("exposes an accessible name for the language control", () => {
    render(<LanguageSwitcher />);

    expect(
      screen.getByRole("combobox", { name: "layout.language" }),
    ).toBeInTheDocument();
  });
});
