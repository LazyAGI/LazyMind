import { render } from "@testing-library/react";
import { expect, it } from "vitest";
import GuideSectionIcon, { sectionIconKind } from "./GuideSectionIcon";

it("keeps fixed chapter meanings regardless of mentioned topics", () => {
  expect(sectionIconKind({role:"results",title:"实验结果：噪声与训练"})).toBe("results");
  expect(sectionIconKind({role:"overview"})).toBe("summary");
});
it.each([
  ["noise-objective", "噪声预测与损失权重", "learning"],
  ["progressive-coding", "渐进生成与有损压缩", "compression"],
  ["selective-scan", "选择性扫描与 GPU 内存", "scan"],
  ["mamba-block", "从选择性层到 Mamba 网络块", "layers"],
  ["multi-head-attention", "多头注意力：并行观察不同线索", "attention"],
  ["position-and-masking", "位置与因果遮罩", "mask"],
  ["implicit-reward", "隐含奖励与等价类", "reward"],
  ["backward-recomputation", "反向传播：保存什么，重算什么", "backward"],
  ["custom-a", "Parameter budget", "parameters"],
  ["subspace-similarity", "低秩为何可能够用：寻找共同方向", "geometry"],
])("matches %s to its learning topic", (role,title,kind) => {
  expect(sectionIconKind({role,title})).toBe(kind);
});
it("uses plan context for generic titles, without overriding a specific title", () => {
  expect(sectionIconKind({role:"how-it-works",title:"如何工作",description:"图像逐步加噪与去噪"})).toBe("noise");
  expect(sectionIconKind({role:"method-a",title:"参数预算",description:"训练目标和损失"})).toBe("parameters");
  expect(sectionIconKind({role:"unfamiliar-chapter"})).toBe("process");
});
it("renders decorative consistent SVG without adding an accessible name", () => {
  const {container} = render(<GuideSectionIcon role="overview" />);
  expect(container.firstElementChild).toHaveAttribute("aria-hidden","true");
  expect(container.querySelector("svg")).toHaveAttribute("viewBox","0 0 24 24");
  expect(container.querySelector("svg")).toHaveAttribute("focusable","false");
});
