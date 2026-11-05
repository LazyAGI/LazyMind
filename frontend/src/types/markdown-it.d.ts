declare module 'markdown-it' {
  export interface MarkdownItOptions {
    html?: boolean;
    linkify?: boolean;
    typographer?: boolean;
    [key: string]: unknown;
  }

  export default class MarkdownIt {
    constructor(options?: MarkdownItOptions);
    parse(source: string, environment: Record<string, unknown>): Array<{ type: string; content: string; info: string; level: number; map: [number, number] | null; attrGet(name: string): string | null }>;
    parseInline(source: string, environment: Record<string, unknown>): Array<{ children?: Array<{ type: string; content: string; attrGet(name: string): string | null }> }>;
    normalizeLink(url: string): string;
    render(source: string): string;
  }
}
