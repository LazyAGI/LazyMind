import { describe, expect, it } from "vitest";
import { renderWithProviders, screen } from "@/test/testUtils";
import InitialCard from "./index";

describe("InitialCard", () => {
  it("renders the fallback title translation key when no env title is set", () => {
    renderWithProviders(<InitialCard />);
    expect(screen.getByText("chat.initialTitle")).toBeInTheDocument();
  });

  it("renders every feature info item's title and text keys", () => {
    renderWithProviders(<InitialCard />);
    expect(screen.getByText("chat.feature1Title")).toBeInTheDocument();
    expect(screen.getByText("chat.feature1Text")).toBeInTheDocument();
    expect(screen.getByText("chat.feature2Title")).toBeInTheDocument();
    expect(screen.getByText("chat.feature3Title")).toBeInTheDocument();
    expect(screen.getByText("chat.feature5Title")).toBeInTheDocument();
  });

  it("does not render the commented-out feature4 item", () => {
    renderWithProviders(<InitialCard />);
    expect(screen.queryByText("chat.feature4Title")).not.toBeInTheDocument();
  });
});
