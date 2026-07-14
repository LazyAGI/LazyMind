import MarkdownViewer from '@/modules/chat/components/MarkdownViewer';
import i18n from '@/i18n';
import { useTranslation } from 'react-i18next';

function tr(key: string, options?: Record<string, unknown>): string {
  return i18n.t(key, options);
}

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
    query: 'query',
    task_type: 'taskType',
    length_target: 'lengthTarget',
    constraints: 'constraints',
    context_id: 'contextId',
    document_summary: 'documentSummary',
    key_points: 'keyPoints',
    style_profile: 'styleProfile',
    audience: 'audience',
    formality: 'formality',
    tone: 'tone',
    min_words: 'minWords',
    max_words: 'maxWords',
    min_length: 'minLength',
    max_length: 'maxLength',
    word_count: 'wordCount',
    language: 'language',
    genre: 'genre',
    style: 'style',
    topic: 'topic',
    deadline: 'deadline',
    format: 'format',
    outline_id: 'outlineId',
    node_id: 'nodeId',
    title: 'title',
    instruction: 'instruction',
    author_notes: 'authorNotes',
    level: 'level',
    section_id: 'sectionId',
    draft_id: 'draftId',
    output_id: 'outputId',
    section_goal: 'sectionGoal',
    required_points: 'requiredPoints',
    outline_node_id: 'outlineNodeId',
    blocks: 'blocks',
    sections: 'sections',
    content: 'content',
    content_type: 'contentType',
    output_format: 'outputFormat',
    result: 'result',
    is_passed: 'isPassed',
    score: 'score',
    summary: 'summary',
    issues: 'issues',
    severity: 'severity',
    category: 'category',
    description: 'description',
    resource_id: 'resourceId',
    resource_type: 'resourceType',
    mime_type: 'mimeType',
    uri: 'uri',
    meta: 'meta',
  };
  return labels[key] ? tr(`chat.writer.fields.${labels[key]}`) : key.replace(/_/g, ' ');
}

/** Flatten a value into human-readable text without producing [object Object]. */
function formatDisplayValue(value: unknown): string {
  if (isEmptyValue(value)) return '';
  if (typeof value === 'string') return value.trim();
  if (typeof value === 'number' || typeof value === 'boolean') return String(value);
  if (Array.isArray(value)) {
    return value.map((item) => formatDisplayValue(item)).filter(Boolean).join(tr('chat.writer.listSeparator'));
  }
  const record = asRecord(value);
  if (record) {
    return Object.entries(record)
      .filter(([, item]) => !isEmptyValue(item))
      .map(([key, item]) => {
        const formatted = formatDisplayValue(item);
        return formatted ? tr('chat.writer.keyValue', { key: humanizeKey(key), value: formatted }) : '';
      })
      .filter(Boolean)
      .join(tr('chat.writer.fieldSeparator'));
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
        const title = asString(item.title) || asString(item.section_title) || tr('chat.writer.sectionNumber', { index: index + 1 });
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
  title = tr('chat.writer.moreInformation'),
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
        <span>{tr('chat.writer.contentStructure')}</span>
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
        <MetaRow label={tr('chat.writer.fields.taskType')} value={asString(record.task_type)} />
        <MetaRow label={tr('chat.writer.fields.lengthTarget')} value={asString(record.length_target)} />
        <MetaRow label={tr('chat.writer.writingLanguage')} value={asString(record.language)} />
        <MetaRow label={tr('chat.writer.fields.genre')} value={asString(record.genre)} />
      </div>
      {!isEmptyValue(record.constraints) && (
        <div className='writer-artifact__section'>
          <div className='writer-artifact__section-title'>{tr('chat.writer.fields.constraints')}</div>
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
        const title = asString(record.title) || asString(record.resource_id) || tr('chat.writer.resourceNumber', { index: index + 1 });
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
              title={tr('chat.writer.resourceDetails')}
            />
          </div>
        );
      })}
      {root && (
        <RemainingDetails
          data={root}
          omit={{ profiles: true, resources: true, resource_profiles: true }}
          title={tr('chat.writer.resourceCollectionInformation')}
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
          <div className='writer-artifact__section-title'>{tr('chat.writer.fields.documentSummary')}</div>
          <p className='writer-artifact__paragraph'>{asString(summary?.summary)}</p>
        </div>
      )}
      {keyPoints.length > 0 && (
        <div className='writer-artifact__section'>
          <div className='writer-artifact__section-title'>{tr('chat.writer.fields.keyPoints')}</div>
          <ul className='writer-artifact__list'>
            {keyPoints.map((point) => (
              <li key={point}>{point}</li>
            ))}
          </ul>
        </div>
      )}
      {style && (
        <div className='writer-artifact__section'>
          <div className='writer-artifact__section-title'>{tr('chat.writer.fields.styleProfile')}</div>
          <div className='writer-artifact__meta-grid'>
            <MetaRow label={tr('chat.writer.fields.audience')} value={asString(style.audience)} />
            <MetaRow label={tr('chat.writer.fields.formality')} value={asString(style.formality)} />
            <MetaRow label={tr('chat.writer.fields.tone')} value={asString(style.tone)} />
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
    return <div className='writer-artifact__empty'>{tr('chat.writer.noOutline')}</div>;
  }
  return (
    <div className='writer-artifact writer-artifact--outline'>
      {asString(record?.outline_id) && (
        <MetaRow label={tr('chat.writer.fields.outlineId')} value={asString(record?.outline_id)} />
      )}
      <OutlineNodesList nodes={nodes} />
      <RemainingDetails data={record} omit={{ nodes: true, outline_id: true }} title={tr('chat.writer.outlineInformation')} />
    </div>
  );
}

function SectionInstructionsView({ data }: { data: unknown }) {
  const record = asRecord(data);
  const instructions = asArray(record?.instructions ?? data);
  if (!instructions.length) {
    return <div className='writer-artifact__empty'>{tr('chat.writer.noSectionPlan')}</div>;
  }
  return (
    <div className='writer-artifact writer-artifact--instructions'>
      {instructions.map((item, index) => {
        const recordItem = asRecord(item);
        if (!recordItem) return null;
        const title = asString(recordItem.section_title) || tr('chat.writer.sectionNumber', { index: index + 1 });
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
              title={tr('chat.writer.writingDetails')}
            />
          </div>
        );
      })}
      <RemainingDetails data={record} omit={{ instructions: true }} title={tr('chat.writer.planInformation')} />
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
          <div className='writer-artifact__section-title'>{tr('chat.writer.relatedOutline')}</div>
          <MetaRow label={tr('chat.writer.fields.outlineId')} value={asString(record.outline_id)} />
        </div>
        <div className='writer-artifact__section'>
          <div className='writer-artifact__section-title'>{tr('chat.writer.sectionStructure')}</div>
          <OutlineNodesList nodes={record.nodes} />
        </div>
        {!isEmptyValue(record.meta) && (
          <div className='writer-artifact__section'>
            <div className='writer-artifact__section-title'>{tr('chat.writer.creationNotes')}</div>
            <StructuredValue value={record.meta} />
          </div>
        )}
        <RemainingDetails
          data={record}
          omit={{ outline_id: true, nodes: true, meta: true }}
          title={tr('chat.writer.otherInformation')}
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
        <div className='writer-artifact__empty'>{tr('chat.writer.sectionGenerating')}</div>
      )}
      <BlockDetails blocks={record.blocks} />
      <RemainingDetails
        data={record}
        omit={{ title: true, content: true, blocks: true, section_id: true }}
        title={tr('chat.writer.sectionInformation')}
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
            title={tr('chat.writer.documentInformation')}
          />
        </div>
      );
    }
    return <div className='writer-artifact__empty'>{tr('chat.writer.noDraftContent')}</div>;
  }
  return (
    <div className='writer-artifact writer-artifact--draft-document'>
      {asString(record?.title) && (
        <div className='writer-artifact__document-title'>{asString(record?.title)}</div>
      )}
      {sections.map((section, index) => {
        const item = asRecord(section);
        if (!item) return null;
        const title = asString(item.title) || tr('chat.writer.sectionNumber', { index: index + 1 });
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
              title={tr('chat.writer.sectionInformation')}
            />
          </section>
        );
      })}
      <RemainingDetails
        data={record}
        omit={{ title: true, content: true, blocks: true, sections: true }}
        title={tr('chat.writer.documentInformation')}
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
            {passed ? tr('chat.writer.passed') : tr('chat.writer.failed')}
          </span>
        )}
        {typeof score === 'number' && (
          <span className='writer-artifact__score'>{tr('chat.writer.scoreValue', { score })}</span>
        )}
      </div>
      {summary && <p className='writer-artifact__paragraph'>{summary}</p>}
      {issues.length > 0 && (
        <div className='writer-artifact__section'>
          <div className='writer-artifact__section-title'>{tr('chat.writer.fields.issues')}</div>
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
                    title={tr('chat.writer.issueDetails')}
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
        title={tr('chat.writer.reviewDetails')}
      />
      {record && record !== result && (
        <RemainingDetails data={record} omit={{ result: true }} title={tr('chat.writer.reportInformation')} />
      )}
    </div>
  );
}

function WritingOutputView({ data, hideDownload = false }: { data: unknown; hideDownload?: boolean }) {
  const record = asRecord(data);
  const content = asString(record?.content);
  if (!content) {
    return <div className='writer-artifact__empty'>{tr('chat.writer.noFinalContent')}</div>;
  }
  return (
    <div className='writer-artifact writer-artifact--output'>
      {!hideDownload ? (
        <div className='writer-artifact__output-toolbar'>
          <ArtifactDownloadButton
            label={tr('chat.writer.downloadMarkdown')}
            filename='writing_output.md'
            content={content}
          />
        </div>
      ) : null}
      <MarkdownBlock content={content} />
      <RemainingDetails data={record} omit={{ content: true }} title={tr('chat.writer.finalInformation')} />
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
  useTranslation();
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
