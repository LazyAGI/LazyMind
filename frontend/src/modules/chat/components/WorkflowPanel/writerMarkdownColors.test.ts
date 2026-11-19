import { expect, it } from 'vitest';
import { markdownColorMatches, parseMarkdownColor, replaceMarkdownColor } from './writerMarkdownColors';

it.each([
  ['#abc', '#aabbcc', 1], ['#abcd', '#aabbcc', 221 / 255], ['#123456', '#123456', 1],
  ['#12345680', '#123456', 128 / 255], ['rgb(0, 229, 255)', '#00e5ff', 1],
  ['rgba(0,229,255,.06)', '#00e5ff', .06], ['rgba(255,255,255,1.0)', '#ffffff', 1],
])('parses %s without losing opacity', (value, hex, alpha) => {
  expect(parseMarkdownColor(value as string)).toEqual({ hex, alpha });
});

it.each(['#12', '#12345', '#1234567', '#123456789', '#gggggg', 'red', 'rgb(256,0,0)',
  'rgba(0,0,0,1.1)', 'rgba(0,0,0,-1)', 'rgba(0,0,0)', 'rgb(0,0,0,1)', 'rgb(1,2,3);color:red',
  'url(https://example.com)', 'var(--color)', 'rgb(1,2,3)\n', '#fff\n'])('does not interpret %s as an editable color', value => {
  expect(parseMarkdownColor(value)).toBeUndefined();
});

it('matches only complete standalone color values', () => {
  expect(markdownColorMatches('中文#fff，#AABBCC; id#abcdef /#123456 foo-rgb(1,2,3) #fffffff rgba(0, 1, 2, .3)').map(match => match[0]))
    .toEqual(['#fff', '#AABBCC', 'rgba(0, 1, 2, .3)']);
});

it('preserves RGB spelling and alpha when replacing channels', () => {
  expect(replaceMarkdownColor('RGBA(0, 229, 255, .060)', '#aabbcc')).toBe('RGBA(170, 187, 204, .060)');
  expect(replaceMarkdownColor('#ABCD', '#123456')).toBe('#123456DD');
});
