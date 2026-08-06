import { describe, expect, it } from "vitest";
import { renderWithProviders, screen } from "@/test/testUtils";
import Layout from "./index";

describe("Layout (knowledge)", () => {
  it("renders children inside the micro-knowledge-page wrapper", () => {
    renderWithProviders(
      <Layout>
        <div>child content</div>
      </Layout>,
    );
    expect(screen.getByText("child content")).toBeInTheDocument();
    expect(document.querySelector(".micro-knowledge-page")).toBeInTheDocument();
  });

  it("renders without children", () => {
    renderWithProviders(<Layout />);
    expect(document.querySelector(".micro-knowledge-page")).toBeInTheDocument();
  });

  it("accepts a custom theme token without crashing", () => {
    renderWithProviders(
      <Layout token={{ colorPrimary: "#ff0000" }}>
        <div>themed</div>
      </Layout>,
    );
    expect(screen.getByText("themed")).toBeInTheDocument();
  });
});
