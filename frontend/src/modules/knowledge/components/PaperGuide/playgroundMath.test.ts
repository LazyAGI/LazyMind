import { expect, it } from "vitest";
import { playgroundDocument } from "./PaperGuidePlayground";

it("typesets scene prose on load and after interaction using offline native MathML", async () => {
  const html = String.raw`<p>查询 $q_i$ 与键 \(k_j\)</p><code>$raw$</code>
    <p class="legacy">绿色为 L_simple</p><select><option value="L_simple">L_simple</option></select>
    <button>Change</button><output>waiting</output>
    <script>document.querySelector('button').onclick = () => {
      document.querySelector('output').firstChild.data = '$$x^2$$';
      const label = document.createElement('span');
      label.textContent = '$y_j$';
      document.querySelector('output').appendChild(label);
    };</script>`;
  const frame = document.createElement("iframe");
  document.body.append(frame);
  const doc = frame.contentDocument!;
  try {
    doc.open();
    doc.write(playgroundDocument("<script>window.ResizeObserver = class { observe() {} }</script>" + html));
    doc.close();
    await new Promise(resolve => setTimeout(resolve, 0));
    expect(doc.querySelectorAll("p:first-child math msub")).toHaveLength(2);
    expect(doc.querySelector("code")?.textContent).toBe("$raw$");
    expect(doc.querySelector(".legacy math msub")?.textContent).toBe("Lsimple");
    expect(doc.querySelector("option")!.value).toBe("L_simple");
    doc.querySelector("button")!.click();
    await new Promise(resolve => setTimeout(resolve, 0));
    expect(doc.querySelector("output math[display=block] msup")).toBeTruthy();
    expect(doc.querySelector("output math msub")).toBeTruthy();
    expect(doc.querySelectorAll("math math, script[src], link")).toHaveLength(0);
  } finally {
    frame.remove();
  }
});
