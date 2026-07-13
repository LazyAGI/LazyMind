import MarkdownViewer from '@/modules/chat/components/MarkdownViewer';

/** Slot ids rendered by WriterArtifactContent in the writer-plugin panel. */
export const WRITER_ARTIFACT_SLOT_IDS = new Set([
  'writing_task',
  'resource_profiles',
  'writing_context',
  'outline',
  'section_instructions',
  'draft_sections',
  'draft_document',
  'review_report',
  'review_summary',
  'writing_output',
]);

export function unwrapArtifactPayload(raw: unknown): unknown {
  if (raw && typeof raw === 'object' && 'data' in raw) {
    return (raw as { data: unknown }).data;
  }
  return raw;
}

function asRecord(value: unknown): Record<string, unknown> | null {
  return value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : [];
}

function isEmptyValue(value: unknown): boolean {
  if (value === null || value === undefined) return true;
  if (typeof value === 'string') return value.trim() === '';
  if (Array.isArray(value)) return value.length === 0;
  if (typeof value === 'object') return Object.keys(value as object).length === 0;
  return false;
}

function humanizeKey(key: string): string {
  const labels: Record<string, string> = {
    query: '写作需求',
    task_type: '任务类型',
    length_target: '目标篇幅',
    constraints: '约束条件',
    context_id: '上下文 ID',
    document_summary: '文档摘要',
    key_points: '关键要点',
    style_profile: '风格画像',
    audience: '受众',
    formality: '正式程度',
    tone: '语气',
    min_words: '最少字数',
    max_words: '最多字数',
    min_length: '最少字数',
    max_length: '最多字数',
    word_count: '字数',
    language: '语言',
    genre: '体裁',
    style: '风格',
    topic: '主题',
    deadline: '截止时间',
    format: '格式',
    outline_id: '大纲 ID',
    node_id: '章节 ID',
    title: '标题',
    instruction: '写作指引',
    author_notes: '作者备注',
    level: '层级',
    section_id: '章节 ID',
    draft_id: '初稿 ID',
    output_id: '成稿 ID',
    section_goal: '章节目标',
    required_points: '必写要点',
    outline_node_id: '关联大纲章节',
    blocks: '正文内容',
    sections: '章节',
    content: '内容',
    content_type: '内容类型',
    output_format: '输出格式',
    result: '审阅结果',
    is_passed: '是否通过',
    score: '评分',
    summary: '摘要',
    issues: '问题列表',
    severity: '严重程度',
    category: '问题分类',
    description: '问题说明',
    resource_id: '资源 ID',
    resource_type: '资源类型',
    mime_type: '文件类型',
    uri: '资源地址',
    meta: '扩展信息',
  };
  return labels[key] ?? key.replace(/_/g, ' ');
}

/** Flatten a value into human-readable text without producing [object Object]. */
function formatDisplayValue(value: unknown): string {
  if (isEmptyValue(value)) return '';
  if (typeof value === 'string') return value.trim();
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  if (Array.isArray(value)) {
    return value.map((item) => formatDisplayValue(item)).filter(Boolean).join('、');
  }
  const record = asRecord(value);
  if (record) {
    return Object.entries(record)
      .filter(([, item]) => !isEmptyValue(item))
      .map(([key, item]) => {
        const formatted = formatDisplayValue(item);
        return formatted ? `${humanizeKey(key)}：${formatted}` : '';
      })
      .filter(Boolean)
      .join('；');
  }
  return '';
}

function asString(value: unknown): string {
  return formatDisplayValue(value);
}

function joinBlockContent(blocks: unknown): string {
  return asArray(blocks)
    .map((block) => {
      const record = asRecord(block);
      return record ? asString(record.content) : '';
    })
    .filter(Boolean)
    .join('\n\n');
}

function MetaRow({ label, value }: { label: string; value: string }) {
  if (!value) return null;
  return (
    <div className='writer-artifact__meta-row'>
      <span className='writer-artifact__meta-label'>{label}</span>
      <span className='writer-artifact__meta-value'>{value}</span>
    </div>
  );
}

function ChipList({ items }: { items: string[] }) {
  const visible = items.filter(Boolean);
  if (!visible.length) return null;
  return (
    <div className='writer-artifact__chips'>
      {visible.map((item) => (
        <span key={item} className='writer-artifact__chip'>{item}</span>
      ))}
    </div>
  );
}

function MarkdownBlock({ content }: { content: string }) {
  if (!content.trim()) return null;
  return (
    <div className='writer-artifact__markdown'>
      <MarkdownViewer>{content}</MarkdownViewer>
    </div>
  );
}

function downloadTextFile(content: string, filename: string, mimeType = 'text/plain;charset=utf-8') {
  const blob = new Blob([content], { type: mimeType });
  const objectUrl = URL.createObjectURL(blob);
  const anchor = document.createElement('a');
  anchor.href = objectUrl;
  anchor.download = filename;
  anchor.click();
  URL.revokeObjectURL(objectUrl);
}

function ArtifactDownloadButton({
  label,
  filename,
  content,
  href,
}: {
  label: string;
  filename: string;
  content?: string;
  href?: string;
}) {
  const handleDownload = () => {
    if (content?.trim()) {
      const isMarkdown = filename.toLowerCase().endsWith('.md');
      downloadTextFile(
        content,
        filename,
        isMarkdown ? 'text/markdown;charset=utf-8' : 'text/plain;charset=utf-8',
      );
      return;
    }
    if (href) {
      const anchor = document.createElement('a');
      anchor.href = href;
      anchor.download = filename;
      anchor.click();
    }
  };

  return (
    <button
      type='button'
      className='plugin-slot__file-action-btn writer-artifact__download-btn'
      onClick={handleDownload}
      disabled={!content?.trim() && !href}
    >
      {label}
    </button>
  );
}

function StructuredValue({ value }: { value: unknown }) {
  if (isEmptyValue(value)) return null;

  if (typeof value === 'string') {
    return <p className='writer-artifact__paragraph'>{value.trim()}</p>;
  }

  if (typeof value === 'number' || typeof value === 'boolean') {
    return <p className='writer-artifact__paragraph'>{String(value)}</p>;
  }

  if (Array.isArray(value)) {
    const items = value.map((item) => formatDisplayValue(item)).filter(Boolean);
    if (!items.length) return null;
    return (
      <ul className='writer-artifact__list writer-artifact__list--compact'>
        {items.map((item, index) => (
          <li key={`${item}-${index}`}>{item}</li>
        ))}
      </ul>
    );
  }

  const record = asRecord(value);
  if (!record) return null;

  const entries = Object.entries(record).filter(([, item]) => !isEmptyValue(item));
  if (!entries.length) return null;

  return (
    <div className='writer-artifact__meta-grid'>
      {entries.map(([key, item]) => (
        <MetaRow key={key} label={humanizeKey(key)} value={formatDisplayValue(item)} />
      ))}
    </div>
  );
}

type OmitSpec = Record<string, true | OmitSpec>;

function omitConsumedFields(value: unknown, spec: OmitSpec): unknown {
  const record = asRecord(value);
  if (!record) return value;

  return Object.fromEntries(
    Object.entries(record).flatMap(([key, item]) => {
      const rule = spec[key];
      if (rule === true) return [];
      const nextValue = rule && typeof rule === 'object'
        ? omitConsumedFields(item, rule)
        : item;
      return isEmptyValue(nextValue) ? [] : [[key, nextValue]];
    }),
  );
}

function DetailValue({ value, depth = 0 }: { value: unknown; depth?: number }) {
  if (isEmptyValue(value)) return null;

  if (typeof value === 'string') {
    const text = value.trim();
    const looksLikeMarkdown = text.includes('\n') || /^#{1,6}\s/m.test(text);
    return looksLikeMarkdown
      ? <MarkdownBlock content={text} />
      : <p className='writer-artifact__paragraph'>{text}</p>;
  }

  if (typeof value === 'number' || typeof value === 'boolean') {
    return <span className='writer-artifact__detail-scalar'>{String(value)}</span>;
  }

  if (Array.isArray(value)) {
    return (
      <div className='writer-artifact__detail-list'>
        {value.map((item, index) => (
          <div key={index} className='writer-artifact__detail-list-item'>
            <span className='writer-artifact__detail-index'>{index + 1}</span>
            <DetailValue value={item} depth={depth + 1} />
          </div>
        ))}
      </div>
    );
  }

  const record = asRecord(value);
  if (!record) return null;
  const entries = Object.entries(record).filter(([, item]) => !isEmptyValue(item));

  return (
    <div className='writer-artifact__detail-fields'>
      {entries.map(([key, item]) => (
        <div key={key} className='writer-artifact__detail-field'>
          <div className='writer-artifact__detail-label'>{humanizeKey(key)}</div>
          <div className='writer-artifact__detail-value'>
            <DetailValue value={item} depth={depth + 1} />
          </div>
        </div>
      ))}
    </div>
  );
}

function OutlineNodesList({ nodes }: { nodes: unknown }) {
  const items = asArray(nodes);
  if (!items.length) return null;

  return (
    <div className='writer-artifact__node-list'>
      {items.map((node, index) => {
        const item = asRecord(node);
        if (!item) return null;
        const title = asString(item.title) || asString(item.section_title) || `章节 ${index + 1}`;
        return (
          <div key={`${asString(item.node_id) || asString(item.section_id) || title}-${index}`} className='writer-artifact__card'>
            <div className='writer-artifact__card-header'>
              <span className='writer-artifact__step-badge'>{index + 1}</span>
              <span className='writer-artifact__card-title'>{title}</span>
              {asString(item.level) && (
                <span className='writer-artifact__chip'>L{asString(item.level)}</span>
              )}
            </div>
            {asString(item.instruction) && (
              <p className='writer-artifact__paragraph'>{asString(item.instruction)}</p>
            )}
            {!isEmptyValue(item.constraints) && (
              <StructuredValue value={item.constraints} />
            )}
          </div>
        );
      })}
    </div>
  );
}

function RemainingDetails({
  data,
  omit,
  title = '更多信息',
}: {
  data: unknown;
  omit: OmitSpec;
  title?: string;
}) {
  const remaining = omitConsumedFields(data, omit);
  if (isEmptyValue(remaining)) return null;

  return (
    <section className='writer-artifact__details-section'>
      <div className='writer-artifact__section-heading'>
        <span className='writer-artifact__section-heading-mark' />
        <span>{title}</span>
      </div>
      <DetailValue value={remaining} />
    </section>
  );
}

function BlockDetails({ blocks }: { blocks: unknown }) {
  const metadata = asArray(blocks)
    .map((block) => omitConsumedFields(block, { content: true }))
    .filter((block) => !isEmptyValue(block));

  if (!metadata.length) return null;
  return (
    <section className='writer-artifact__details-section'>
      <div className='writer-artifact__section-heading'>
        <span className='writer-artifact__section-heading-mark' />
        <span>内容结构</span>
      </div>
      <DetailValue value={metadata} />
    </section>
  );
}

function WritingTaskView({ data }: { data: unknown }) {
  const record = asRecord(data);
  if (!record) return null;
  const query = asString(record.query);
  return (
    <div className='writer-artifact writer-artifact--task'>
      {query && <p className='writer-artifact__lead'>{query}</p>}
      <div className='writer-artifact__meta-grid'>
        <MetaRow label='任务类型' value={asString(record.task_type)} />
        <MetaRow label='目标篇幅' value={asString(record.length_target)} />
        <MetaRow label='写作语言' value={asString(record.language)} />
        <MetaRow label='体裁' value={asString(record.genre)} />
      </div>
      {!isEmptyValue(record.constraints) && (
        <div className='writer-artifact__section'>
          <div className='writer-artifact__section-title'>约束条件</div>
          <StructuredValue value={record.constraints} />
        </div>
      )}
      <RemainingDetails
        data={record}
        omit={{
          query: true,
          task_type: true,
          length_target: true,
          language: true,
          genre: true,
          constraints: true,
        }}
      />
    </div>
  );
}

function ResourceProfilesView({ data }: { data: unknown }) {
  const root = asRecord(data);
  const profiles = asArray(
    Array.isArray(data)
      ? data
      : root?.profiles ?? root?.resources ?? root?.resource_profiles,
  );
  if (!profiles.length) {
    return <GenericStructuredView data={data} />;
  }
  return (
    <div className='writer-artifact writer-artifact--profiles'>
      {profiles.map((item, index) => {
        const record = asRecord(item);
        if (!record) return null;
        const title = asString(record.title) || asString(record.resource_id) || `资源 ${index + 1}`;
        return (
          <div key={`${title}-${index}`} className='writer-artifact__card'>
            <div className='writer-artifact__card-header'>
              <span className='writer-artifact__card-title'>{title}</span>
              <ChipList items={[asString(record.resource_type), asString(record.mime_type)]} />
            </div>
            {asString(record.summary) && (
              <p className='writer-artifact__paragraph'>{asString(record.summary)}</p>
            )}
            {asString(record.uri) && (
              <div className='writer-artifact__muted'>{asString(record.uri)}</div>
            )}
            <RemainingDetails
              data={record}
              omit={{
                title: true,
                resource_id: true,
                resource_type: true,
                mime_type: true,
                summary: true,
                uri: true,
              }}
              title='资源详情'
            />
          </div>
        );
      })}
      {root && (
        <RemainingDetails
          data={root}
          omit={{ profiles: true, resources: true, resource_profiles: true }}
          title='资源集合信息'
        />
      )}
    </div>
  );
}

function WritingContextView({ data }: { data: unknown }) {
  const record = asRecord(data);
  if (!record) return null;
  const summary = asRecord(record.document_summary);
  const style = asRecord(record.style_profile);
  const keyPoints = asArray(summary?.key_points).map(asString).filter(Boolean);

  return (
    <div className='writer-artifact writer-artifact--context'>
      {asString(summary?.summary) && (
        <div className='writer-artifact__section'>
          <div className='writer-artifact__section-title'>文档摘要</div>
          <p className='writer-artifact__paragraph'>{asString(summary?.summary)}</p>
        </div>
      )}
      {keyPoints.length > 0 && (
        <div className='writer-artifact__section'>
          <div className='writer-artifact__section-title'>关键要点</div>
          <ul className='writer-artifact__list'>
            {keyPoints.map((point) => (
              <li key={point}>{point}</li>
            ))}
          </ul>
        </div>
      )}
      {style && (
        <div className='writer-artifact__section'>
          <div className='writer-artifact__section-title'>风格画像</div>
          <div className='writer-artifact__meta-grid'>
            <MetaRow label='受众' value={asString(style.audience)} />
            <MetaRow label='正式程度' value={asString(style.formality)} />
            <MetaRow label='语气' value={asString(style.tone)} />
          </div>
        </div>
      )}
      <RemainingDetails
        data={record}
        omit={{
          document_summary: { summary: true, key_points: true },
          style_profile: { audience: true, formality: true, tone: true },
        }}
      />
    </div>
  );
}

function OutlineView({ data }: { data: unknown }) {
  const record = asRecord(data);
  const nodes = asArray(record?.nodes);
  if (!nodes.length) {
    return <div className='writer-artifact__empty'>暂无大纲</div>;
  }
  return (
    <div className='writer-artifact writer-artifact--outline'>
      {asString(record?.outline_id) && (
        <MetaRow label='大纲 ID' value={asString(record?.outline_id)} />
      )}
      <OutlineNodesList nodes={nodes} />
      <RemainingDetails data={record} omit={{ nodes: true, outline_id: true }} title='大纲信息' />
    </div>
  );
}

function SectionInstructionsView({ data }: { data: unknown }) {
  const record = asRecord(data);
  const instructions = asArray(record?.instructions ?? data);
  if (!instructions.length) {
    return <div className='writer-artifact__empty'>暂无章节规划</div>;
  }
  return (
    <div className='writer-artifact writer-artifact--instructions'>
      {instructions.map((item, index) => {
        const recordItem = asRecord(item);
        if (!recordItem) return null;
        const title = asString(recordItem.section_title) || `章节 ${index + 1}`;
        const requiredPoints = asArray(recordItem.required_points).map(asString).filter(Boolean);
        return (
          <div key={`${asString(recordItem.outline_node_id) || title}-${index}`} className='writer-artifact__card'>
            <div className='writer-artifact__card-header'>
              <span className='writer-artifact__step-badge'>{index + 1}</span>
              <span className='writer-artifact__card-title'>{title}</span>
            </div>
            {asString(recordItem.section_goal) && (
              <p className='writer-artifact__paragraph'>{asString(recordItem.section_goal)}</p>
            )}
            {requiredPoints.length > 0 && (
              <ul className='writer-artifact__list writer-artifact__list--compact'>
                {requiredPoints.map((point) => (
                  <li key={point}>{point}</li>
                ))}
              </ul>
            )}
            <RemainingDetails
              data={recordItem}
              omit={{
                outline_node_id: true,
                section_title: true,
                section_goal: true,
                required_points: true,
              }}
              title='写作细节'
            />
          </div>
        );
      })}
      <RemainingDetails data={record} omit={{ instructions: true }} title='规划信息' />
    </div>
  );
}

function DraftSectionView({ data }: { data: unknown }) {
  const record = asRecord(data);
  if (!record) return null;

  const title = asString(record.title);
  const sectionId = asString(record.section_id);
  const content = asString(record.content) || joinBlockContent(record.blocks);
  const hasDraftBody = Boolean(title || sectionId || content);
  const isOutlineShape = !isEmptyValue(record.outline_id) && !isEmptyValue(record.nodes);

  if (isOutlineShape && !hasDraftBody) {
    return (
      <div className='writer-artifact writer-artifact--draft-section'>
        <div className='writer-artifact__section'>
          <div className='writer-artifact__section-title'>关联大纲</div>
          <MetaRow label='大纲 ID' value={asString(record.outline_id)} />
        </div>
        <div className='writer-artifact__section'>
          <div className='writer-artifact__section-title'>章节结构</div>
          <OutlineNodesList nodes={record.nodes} />
        </div>
        {!isEmptyValue(record.meta) && (
          <div className='writer-artifact__section'>
            <div className='writer-artifact__section-title'>创作说明</div>
            <StructuredValue value={record.meta} />
          </div>
        )}
        <RemainingDetails
          data={record}
          omit={{ outline_id: true, nodes: true, meta: true }}
          title='其他信息'
        />
      </div>
    );
  }

  return (
    <div className='writer-artifact writer-artifact--draft-section'>
      {(title || sectionId) && (
        <div className='writer-artifact__document-title'>{title || sectionId}</div>
      )}
      {content ? (
        <MarkdownBlock content={content} />
      ) : (
        <div className='writer-artifact__empty'>章节正文生成中…</div>
      )}
      <BlockDetails blocks={record.blocks} />
      <RemainingDetails
        data={record}
        omit={{ title: true, content: true, blocks: true, section_id: true }}
        title='章节信息'
      />
    </div>
  );
}

function DraftDocumentView({ data }: { data: unknown }) {
  const record = asRecord(data);
  const sections = asArray(record?.sections);
  if (!sections.length) {
    const fallback = joinBlockContent(record?.blocks);
    if (fallback) {
      return (
        <div className='writer-artifact writer-artifact--draft-document'>
          {asString(record?.title) && (
            <div className='writer-artifact__document-title'>{asString(record?.title)}</div>
          )}
          <MarkdownBlock content={fallback} />
          <BlockDetails blocks={record?.blocks} />
          <RemainingDetails
            data={record}
            omit={{ title: true, content: true, blocks: true, sections: true }}
            title='文档信息'
          />
        </div>
      );
    }
    return <div className='writer-artifact__empty'>暂无初稿内容</div>;
  }
  return (
    <div className='writer-artifact writer-artifact--draft-document'>
      {asString(record?.title) && (
        <div className='writer-artifact__document-title'>{asString(record?.title)}</div>
      )}
      {sections.map((section, index) => {
        const item = asRecord(section);
        if (!item) return null;
        const title = asString(item.title) || `章节 ${index + 1}`;
        const content = asString(item.content) || joinBlockContent(item.blocks);
        return (
          <section key={`${title}-${index}`} className='writer-artifact__document-section'>
            <div className='writer-artifact__document-section-heading'>
              <span className='writer-artifact__step-badge'>{index + 1}</span>
              <span>{title}</span>
            </div>
            <MarkdownBlock content={content} />
            <BlockDetails blocks={item.blocks} />
            <RemainingDetails
              data={item}
              omit={{ title: true, content: true, blocks: true }}
              title='章节信息'
            />
          </section>
        );
      })}
      <RemainingDetails
        data={record}
        omit={{ title: true, content: true, blocks: true, sections: true }}
        title='文档信息'
      />
    </div>
  );
}

function ReviewReportView({ data }: { data: unknown }) {
  const record = asRecord(data);
  const result = asRecord(record?.result) ?? record;
  if (!result) return null;

  const passed = result.is_passed;
  const score = result.score;
  const summary = asString(result.summary);
  const issues = asArray(result.issues);

  return (
    <div className='writer-artifact writer-artifact--review'>
      <div className='writer-artifact__review-header'>
        {typeof passed === 'boolean' && (
          <span className={`writer-artifact__status-badge writer-artifact__status-badge--${passed ? 'pass' : 'fail'}`}>
            {passed ? '通过' : '未通过'}
          </span>
        )}
        {typeof score === 'number' && (
          <span className='writer-artifact__score'>评分 {score}</span>
        )}
      </div>
      {summary && <p className='writer-artifact__paragraph'>{summary}</p>}
      {issues.length > 0 && (
        <div className='writer-artifact__section'>
          <div className='writer-artifact__section-title'>问题列表</div>
          <div className='writer-artifact__issues'>
            {issues.map((issue, index) => {
              const item = asRecord(issue);
              if (!item) return null;
              const severity = asString(item.severity) || 'medium';
              return (
                <div key={`${asString(item.category)}-${index}`} className='writer-artifact__issue'>
                  <div className='writer-artifact__issue-meta'>
                    <span className={`writer-artifact__severity writer-artifact__severity--${severity}`}>
                      {severity}
                    </span>
                    <span className='writer-artifact__issue-category'>{asString(item.category)}</span>
                  </div>
                  <p className='writer-artifact__paragraph'>{asString(item.description)}</p>
                  <RemainingDetails
                    data={item}
                    omit={{ severity: true, category: true, description: true }}
                    title='问题详情'
                  />
                </div>
              );
            })}
          </div>
        </div>
      )}
      <RemainingDetails
        data={result}
        omit={{ is_passed: true, score: true, summary: true, issues: true }}
        title='审阅详情'
      />
      {record && record !== result && (
        <RemainingDetails data={record} omit={{ result: true }} title='报告信息' />
      )}
    </div>
  );
}

function WritingOutputView({ data, hideDownload = false }: { data: unknown; hideDownload?: boolean }) {
  const record = asRecord(data);
  const content = asString(record?.content);
  if (!content) {
    return <div className='writer-artifact__empty'>暂无最终成稿</div>;
  }
  return (
    <div className='writer-artifact writer-artifact--output'>
      {!hideDownload ? (
        <div className='writer-artifact__output-toolbar'>
          <ArtifactDownloadButton
            label='下载 Markdown'
            filename='writing_output.md'
            content={content}
          />
        </div>
      ) : null}
      <MarkdownBlock content={content} />
      <RemainingDetails data={record} omit={{ content: true }} title='成稿信息' />
    </div>
  );
}

function GenericStructuredView({ data }: { data: unknown }) {
  return (
    <div className='writer-artifact writer-artifact--generic'>
      <DetailValue value={data} />
    </div>
  );
}

export function WriterArtifactContent({
  slotId,
  data,
  hideDownload = false,
}: {
  slotId: string;
  data: unknown;
  hideDownload?: boolean;
}) {
  const payload = unwrapArtifactPayload(data);

  switch (slotId) {
    case 'writing_task':
      return <WritingTaskView data={payload} />;
    case 'resource_profiles':
      return <ResourceProfilesView data={payload} />;
    case 'writing_context':
      return <WritingContextView data={payload} />;
    case 'outline':
      return <OutlineView data={payload} />;
    case 'section_instructions':
      return <SectionInstructionsView data={payload} />;
    case 'draft_sections':
      return <DraftSectionView data={payload} />;
    case 'draft_document':
      return <DraftDocumentView data={payload} />;
    case 'review_report':
      return <ReviewReportView data={payload} />;
    case 'writing_output':
      return <WritingOutputView data={payload} hideDownload={hideDownload} />;
    default:
      return <GenericStructuredView data={payload} />;
  }
}
