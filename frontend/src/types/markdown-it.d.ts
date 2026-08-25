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
    render(source: string): string;
  }
}
