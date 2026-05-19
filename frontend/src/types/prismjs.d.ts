declare module "prismjs" {
  export type Grammar = Record<string, unknown>;

  export interface PrismStatic {
    languages: Record<string, Grammar>;
    highlight(code: string, grammar: Grammar, language: string): string;
  }

  const Prism: PrismStatic;
  export default Prism;
}

declare module "prismjs/components/*";

