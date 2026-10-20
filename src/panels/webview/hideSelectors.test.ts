import fs from 'fs';
import path from 'path';
import {
  MAX_HIDE_COMPOUNDS,
  MAX_HIDE_SELECTOR_BYTES,
  MAX_HIDE_SELECTORS,
  hideSelectorProblem,
  parseHideSelectors,
} from './hideSelectors';

interface HideSelectorCase {
  selector: string;
  allowed: boolean;
  reason?: string;
}

/**
 * The table the backend (pkg/plugin/hide_selectors_test.go) is tested against
 * too, so the panel never sends a selector the proxy refuses.
 */
const sharedCases: HideSelectorCase[] = JSON.parse(
  fs.readFileSync(path.join(__dirname, '../../../pkg/plugin/testdata/hide_selectors.json'), 'utf8')
).cases;

describe('panels/webview/hideSelectors', () => {
  test('the shared table has accepted and refused cases', () => {
    expect(sharedCases.some((c) => c.allowed)).toBe(true);
    expect(sharedCases.some((c) => !c.allowed)).toBe(true);
  });

  test.each(sharedCases.filter((c) => c.allowed))('accepts $selector (shared with the backend)', ({ selector }) => {
    expect(hideSelectorProblem(selector)).toBeUndefined();
  });

  test.each(sharedCases.filter((c) => !c.allowed))(
    'refuses $selector with a message naming $reason (shared with the backend)',
    ({ selector, reason }) => {
      expect(hideSelectorProblem(selector)).toContain(reason);
    }
  );

  test.each([
    ['a pseudo-element', 'p::before', 'pseudo-element'],
    ['an unknown pseudo-class', 'a:visited', ':visited'],
    ['an escaped :not( with a combinator inside', ':\\6e ot(a b)', ':not()'],
    ['a comment hiding a regular expression operator', '[a/*"*/#=x]', '#='],
    ['a pseudo-class without a name', 'a:', 'invalid pseudo-class'],
  ])('refuses %s (stricter than the backend where unsure)', (_name, selector, reason) => {
    expect(hideSelectorProblem(selector)).toContain(reason);
  });

  test.each([
    ['a combinator inside an nth argument of :not()', 'li:not(:nth-child(2n + 1))'],
    ['a colon inside an attribute value', '[title="a:has(b)"]'],
    ['an escaped colon', '.md\\:hidden'],
    ['whitespace around a :not() list', 'p:not( .a , .b )'],
    ['an unterminated comment', 'div /* x'],
  ])('accepts %s', (_name, selector) => {
    expect(hideSelectorProblem(selector)).toBeUndefined();
  });

  test('limits the length to 256 bytes of UTF-8', () => {
    const longest = '.' + 'a'.repeat(MAX_HIDE_SELECTOR_BYTES - 1);
    expect(hideSelectorProblem(longest)).toBeUndefined();
    expect(hideSelectorProblem(longest + 'a')).toBe('is longer than 256 bytes');
    // 'é' is two bytes of UTF-8: 128 of them are 256 bytes.
    expect(hideSelectorProblem('.' + 'é'.repeat(127))).toBeUndefined();
    expect(hideSelectorProblem('.' + 'é'.repeat(128))).toBe('is longer than 256 bytes');
  });

  test('limits a selector to 32 compound selectors, counting every part of a list', () => {
    const compounds = (n: number) => Array(n).fill('a').join(' ');
    expect(hideSelectorProblem(compounds(MAX_HIDE_COMPOUNDS))).toBeUndefined();
    expect(hideSelectorProblem(compounds(MAX_HIDE_COMPOUNDS + 1))).toBe('has more than 32 compound selectors');
    expect(
      hideSelectorProblem(
        Array(MAX_HIDE_COMPOUNDS + 1)
          .fill('a')
          .join(', ')
      )
    ).toBe('has more than 32 compound selectors');
    expect(
      hideSelectorProblem(
        Array(MAX_HIDE_COMPOUNDS + 1)
          .fill('a')
          .join('>')
      )
    ).toBe('has more than 32 compound selectors');
  });

  describe('parseHideSelectors', () => {
    test('trims lines, drops blank ones and leaves out refused selectors with their line number', () => {
      expect(parseHideSelectors('  .ad  \n\n div:has(p)\n#banner\r\n')).toEqual({
        selectors: ['.ad', '#banner'],
        problems: [{ line: 3, selector: 'div:has(p)', message: 'uses :has(), which the proxy does not support' }],
      });
    });

    test('sends at most 20 selectors and names the lines over the limit', () => {
      const lines = Array.from({ length: MAX_HIDE_SELECTORS + 2 }, (_, i) => `.s${i + 1}`);
      // A refused selector does not count towards the limit.
      lines.splice(1, 0, 'p:contains(x)');
      const { selectors, problems } = parseHideSelectors(lines.join('\n'));

      expect(selectors).toHaveLength(MAX_HIDE_SELECTORS);
      expect(selectors[MAX_HIDE_SELECTORS - 1]).toBe('.s20');
      expect(problems).toEqual([
        { line: 2, selector: 'p:contains(x)', message: 'uses :contains(), which the proxy does not support' },
        { line: 22, selector: '.s21', message: 'is over the limit of 20 selectors and is not used' },
        { line: 23, selector: '.s22', message: 'is over the limit of 20 selectors and is not used' },
      ]);
    });

    test('an empty option has no selectors and no problems', () => {
      expect(parseHideSelectors('')).toEqual({ selectors: [], problems: [] });
    });
  });
});
