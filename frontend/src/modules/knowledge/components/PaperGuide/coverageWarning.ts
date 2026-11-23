const labels: Record<string, string> = {
  "Figure caption association is ambiguous or unavailable":
    "图注与图像的对应关系不明确或不可用。",
  "Caption association is ambiguous or unavailable":
    "题注与内容的对应关系不明确或不可用。",
  "Formula transcription is unavailable; only original text and page are readable.":
    "公式转写不可用；目前只能读取原文文字和所在页。",
  "Figure pixels are not supplied to the guide model; captions and regions do not establish visual observations.":
    "导读模型未收到图像像素；图注和区域定位不能作为直接观察图像的依据。",
  "Parsed table cells are unavailable; do not infer values or cell positions.":
    "表格单元格解析不可用；不能据此推断数值或单元格位置。",
};
export function coverageWarning(warning: string, zh: boolean): string {
  if (labels[warning]) return zh ? labels[warning] : warning;
  return `${zh ? "解析提示：" : "Parser information: "}${warning}`;
}
