import MarkdownViewer from "@/modules/knowledge/components/MarkdownViewer";
import { Segment } from "@/api/generated/knowledge-client";

import CustomImage from "../CustomImage";
import { useEffect, useMemo, useRef } from "react";
import { SegmentServiceApi } from "@/modules/knowledge/utils/request";
import MdxEditor from "@/modules/knowledge/components/mdxeditor";
import { replaceImagesWithKeys } from "@/modules/knowledge/components/mdxeditor/util";
import {
  expandImagesInMarkdown,
  resolveMarkdownImageUrl,
} from "@/modules/knowledge/utils/imageUrl";

interface IProps {
  segment: Segment;
  group: string;
  editable?: boolean; // Whether the segment is editable.
  contentReadOnly?: boolean; // Whether the segment content is read only.
}

const SegmentContent = (props: IProps) => {
  const { segment, group, editable = false, contentReadOnly = false } = props;
  const debounceTimerRef = useRef<NodeJS.Timeout | null>(null);

  const content = useMemo(() => {
    const raw = segment.display_content || segment.content || "";
    return expandImagesInMarkdown(raw, segment?.image_keys ?? []);
  }, [segment]);

  const debounceUpdate = (data: string) => {
    if (debounceTimerRef.current) {
      clearTimeout(debounceTimerRef.current);
    }

    const resText = replaceImagesWithKeys(data, segment?.image_keys ?? []);

    debounceTimerRef.current = setTimeout(() => {
      SegmentServiceApi().segmentServiceEditSegment({
        dataset: segment.dataset_id || "",
        document: segment.document_id || "",
        segment: segment.segment_id || "",
        editSegmentRequest: { name: "", group: group, content: resText },
      });
    }, 500);
  };

  useEffect(() => {
    return () => {
      if (debounceTimerRef.current) {
        clearTimeout(debounceTimerRef.current);
      }
    };
  }, []);

  function handleEditorChange({ text }: { text: string }) {
    // setContent(text);
    debounceUpdate(text);
  }

  function renderContent() {
    const components = {
      img(props: any) {
        return (
          <CustomImage
            src={resolveMarkdownImageUrl(
              props.src || "",
              segment?.image_keys ?? [],
            )}
            alt={props.alt}
            showErrorImage
            style={{ background: "#F5F5F5" }}
          />
        );
      },
      a(props: any) {
        return (
          <a href={props.href} onClick={(e) => e.preventDefault()}>
            {props.children}
          </a>
        );
      },
    };

    return editable ? (
      <MdxEditor
        value={content || ""}
        onChange={(text: string) => handleEditorChange({ text })}
      />
    ) : (
      <MarkdownViewer components={components}>{content}</MarkdownViewer>
    );
  }

  return renderContent();
};

export default SegmentContent;
