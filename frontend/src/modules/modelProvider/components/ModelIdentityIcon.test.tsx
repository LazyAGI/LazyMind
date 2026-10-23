import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ModelIdentityIcon } from "./ModelIdentityIcon";

describe("model identity icon", () => {
  it("is decorative beside a model name and keeps a square layout slot", () => {
    const { container } = render(
      <ModelIdentityIcon
        modelName="deepseek-r1"
        fallbackVendor="OpenAI"
        size={28}
      />,
    );
    expect(container.firstChild).toHaveAttribute("aria-hidden", "true");
    expect(container.firstChild).toHaveStyle({ width: "28px", height: "28px" });
    expect(container.querySelector("img")).toHaveAttribute(
      "src",
      "/provider-icons/deepseek.svg",
    );
    expect(container.querySelector("img")).toHaveAttribute("alt", "");
  });

  it("keeps custom models neutral without inheriting their supplier brand", () => {
    const { container } = render(
      <ModelIdentityIcon
        modelName="private-model"
        fallbackVendor="OpenAI"
        className="fixture"
      />,
    );
    expect(container.firstChild).toHaveClass("model-identity-icon", "fixture");
    expect(container.querySelector("img")).toHaveAttribute(
      "src",
      "/provider-icons/custom.svg",
    );
  });
});
