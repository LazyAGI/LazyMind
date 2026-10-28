import { paperFixture } from "./__tests__/fixtures";
import { describe, expect, it } from "vitest";
import { parseBibTeX, parseCSV, parseRIS, toBibTeX, toCSV, toRIS } from "./model";
describe("bibliography import validation", () => {
    const validBib = '@article{one,\n title = {Valid paper}\n}';
    const validRis = 'TY  - JOUR\nTI  - Valid paper\nER  -';
    it.each([
        validBib + '\n@article{broken, title = {Unclosed}',
        validBib + '\n@article{two,\n author = {Author}\n}',
        '@article{one, title = {Paper}, broken field}',
    ])("rejects invalid BibTeX without silently importing a subset", (text) => {
        expect(() => parseBibTeX(text, "local")).toThrow(/BibTeX/);
    });
    it("supports balanced, single-line BibTeX and nested title braces", () => {
        expect(parseBibTeX('@article{one, title = {A {Nested} Title}, year = 2024}', "local")[0]).toMatchObject({metadata:{ title: 'A {Nested} Title', year: 2024 }});
    });
    it("accepts valid BibTeX comments and built-in month constants", () => {
        expect(parseBibTeX('@comment{Exported {from Zotero}}\n@article{x, title={Valid}, year=2024, month=jan}', "local")[0]).toMatchObject({metadata:{ title: "Valid", year: 2024 }});
    });
    it.each([
        validRis + '\nTY  - JOUR\nAU  - Missing title\nER  -',
        validRis + '\nTY  - JOUR\nTI  - Unclosed record',
        'garbage\n' + validRis,
    ])("rejects invalid RIS records without dropping them", (text) => {
        expect(() => parseRIS(text, "local")).toThrow(/RIS/);
    });
    it.each([
        'title,authors\nValid,A\n,B',
        'title,authors\n"Unclosed,A',
        'authors,year\nAuthor,2024',
        'title,authors\nValid,A,unexpected',
    ])("rejects invalid CSV rather than skipping or truncating rows", (text) => {
        expect(() => parseCSV(text)).toThrow(/CSV/);
    });
    it("round-trips multiline quoted CSV and all supported formats", () => {
        const items = [paperFixture({ metadata: {
                    title: 'First line\nSecond "quoted" line'
                } })];
        expect(parseCSV(toCSV(items, [])).papers[0].metadata.title).toBe(items[0].metadata.title);
        expect(parseBibTeX(toBibTeX(items), "local")).toHaveLength(1);
        expect(parseRIS(toRIS([paperFixture()]), "local")).toHaveLength(1);
    });
});
