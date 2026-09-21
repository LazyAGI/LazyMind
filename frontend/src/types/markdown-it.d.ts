declare module 'markdown-it' {
  export interface MarkdownItOptions {
    html?: boolean;
    linkify?: boolean;
    typographer?: boolean;
    [key: string]: unknown;
  }

  export default class MarkdownIt {
    constructor(options?: MarkdownItOptions);
    parse(source: string, environment: Record<string, unknown>): unknown[];
    parseInline(source: string, environment: Record<string, unknown>): Array<{ children?: Array<{ type: string; content: string; attrGet(name: string): string | null }> }>;
    render(source: string): string;
  }
}
