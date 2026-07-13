import { Doc } from "@/api/generated/core-client";

const PreviewTab = (props: { detail: Doc }) => {
  const { detail } = props;

  console.log("detail:", detail);

  return <div>PreviewTab</div>;
};

export default PreviewTab;
