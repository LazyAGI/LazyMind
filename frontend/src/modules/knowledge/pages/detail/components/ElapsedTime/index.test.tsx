import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import moment from "moment";
import ElapsedTime from "./index";

describe("ElapsedTime", () => {
  it("renders 00:00:00 when startTime is missing", () => {
    const { container } = render(<ElapsedTime />);
    expect(container.textContent).toBe("00:00:00");
  });

  it("formats the elapsed time between startTime and endTime", () => {
    const start = moment("2024-01-01T00:00:00Z");
    const end = moment(start).add(1, "hours").add(2, "minutes").add(3, "seconds");

    const { container } = render(
      <ElapsedTime
        startTime={start.valueOf()}
        endTime={end.valueOf()}
      />,
    );

    expect(container.textContent).toBe("01:02:03");
  });

  it("pads single-digit hours/minutes/seconds with a leading zero", () => {
    const start = moment("2024-01-01T00:00:00Z");
    const end = moment(start).add(1, "minutes").add(5, "seconds");

    const { container } = render(
      <ElapsedTime startTime={start.valueOf()} endTime={end.valueOf()} />,
    );

    expect(container.textContent).toBe("00:01:05");
  });
});
