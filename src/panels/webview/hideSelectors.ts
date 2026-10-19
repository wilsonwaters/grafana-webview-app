/**
 * Hide selectors (CR5): the checks the panel applies before it sends the
 * `hide` parameters to the backend proxy.
 *
 * The backend refuses a request whose hide selectors break its limits (see
 * `pkg/plugin/hide_selectors.go`), so the panel checks the same rules and
 * never sends a selector the backend would refuse:
 *
 * - at most {@link MAX_HIDE_SELECTORS} selectors, each at most
 *   {@link MAX_HIDE_SELECTOR_BYTES} bytes of UTF-8;
 * - at most {@link MAX_HIDE_COMPOUNDS} compound selectors (the parts between
 *   combinators and commas) in one selector;
 * - only the pseudo-classes in {@link SUPPORTED_PSEUDO_CLASSES}. The proxy's
 *   selector engine also has `:has()`, `:contains()`, `:matches()` and others
 *   whose matching cost on a large page is not bounded; they are refused;
 * - `:not()` may contain only simple (compound) selectors, without
 *   combinators;
 * - no `#=` (regular expression) attribute operator.
 *
 * `pkg/plugin/testdata/hide_selectors.json` is the table both sides are
 * tested against. Where this check is unsure it refuses: a selector the panel
 * does not send only stays visible, but one the backend refuses fails the
 * whole panel.
 *
 * The check does not validate the rest of the CSS syntax. The backend skips a
 * selector it cannot parse.
 */

/** Most hide selectors one panel sends. */
export const MAX_HIDE_SELECTORS = 20;

/** Longest hide selector, in bytes of UTF-8 after trimming. */
export const MAX_HIDE_SELECTOR_BYTES = 256;

/** Most compound selectors (the parts between combinators and commas) in one hide selector. */
export const MAX_HIDE_COMPOUNDS = 32;

/** The pseudo-classes a hide selector may use. */
export const SUPPORTED_PSEUDO_CLASSES: ReadonlySet<string> = new Set([
  'not',
  'first-child',
  'last-child',
  'only-child',
  'first-of-type',
  'last-of-type',
  'only-of-type',
  'nth-child',
  'nth-last-child',
  'nth-of-type',
  'nth-last-of-type',
  'empty',
  'root',
  'link',
  'checked',
]);

/**
 * Pseudo-classes of the proxy's selector engine that are refused because they
 * can be slow on a large page, with the name the message uses.
 */
const EXPENSIVE_PSEUDO_CLASSES: ReadonlyMap<string, string> = new Map([
  ['has', ':has()'],
  ['haschild', ':haschild()'],
  ['contains', ':contains()'],
  ['containsown', ':containsOwn()'],
  ['matches', ':matches()'],
  ['matchesown', ':matchesOwn()'],
]);

/** Pseudo-classes whose argument is not a selector (it is skipped). */
const NTH_PSEUDO_CLASSES: ReadonlySet<string> = new Set([
  'nth-child',
  'nth-last-child',
  'nth-of-type',
  'nth-last-of-type',
]);

const NOT_COMBINATOR_MESSAGE = 'uses a combinator inside :not(), which may contain only simple selectors';

/** One hide selector the panel does not send, and why. */
export interface HideSelectorProblem {
  /** The 1-based line of the option text, or undefined for a problem with the whole list. */
  line?: number;
  /** The trimmed selector. */
  selector: string;
  /** Completes the sentence "The selector …", for example "uses :has(), which …". */
  message: string;
}

/** The hide selectors to send, and the problems with the others. */
export interface HideSelectorList {
  selectors: string[];
  problems: HideSelectorProblem[];
}

/**
 * Splits the newline-separated `hideSelectors` option into the selectors the
 * panel sends (trimmed, blank lines dropped, refused selectors left out, at
 * most {@link MAX_HIDE_SELECTORS}) and a problem for each line it leaves out.
 */
export function parseHideSelectors(text: string): HideSelectorList {
  const selectors: string[] = [];
  const problems: HideSelectorProblem[] = [];
  text.split('\n').forEach((raw, index) => {
    const selector = raw.trim();
    if (selector.length === 0) {
      return;
    }
    const line = index + 1;
    const message = hideSelectorProblem(selector);
    if (message !== undefined) {
      problems.push({ line, selector, message });
    } else if (selectors.length >= MAX_HIDE_SELECTORS) {
      problems.push({
        line,
        selector,
        message: `is over the limit of ${MAX_HIDE_SELECTORS} selectors and is not used`,
      });
    } else {
      selectors.push(selector);
    }
  });
  return { selectors, problems };
}

/**
 * Returns why the backend proxy would refuse this (trimmed) hide selector, or
 * undefined when the panel may send it.
 */
export function hideSelectorProblem(selector: string): string | undefined {
  if (utf8Length(selector) > MAX_HIDE_SELECTOR_BYTES) {
    return `is longer than ${MAX_HIDE_SELECTOR_BYTES} bytes`;
  }
  return new SelectorScanner(selector).scan();
}

/** The UTF-8 length of `s`, in bytes. */
function utf8Length(s: string): number {
  let bytes = 0;
  for (const ch of s) {
    const cp = ch.codePointAt(0) ?? 0;
    bytes += cp < 0x80 ? 1 : cp < 0x800 ? 2 : cp < 0x10000 ? 3 : 4;
  }
  return bytes;
}

const isSpace = (c: string | undefined) => c === ' ' || c === '\t' || c === '\r' || c === '\n' || c === '\f';
const isHex = (c: string | undefined) => c !== undefined && /^[0-9a-fA-F]$/.test(c);
const isNameChar = (c: string | undefined) => c !== undefined && (/^[a-zA-Z0-9_-]$/.test(c) || c.charCodeAt(0) > 127);
const toLowerAscii = (s: string) => s.replace(/[A-Z]/g, (c) => c.toLowerCase());

/** The context a character is in: the top level, a `:not(` argument, an nth argument, other parentheses, or brackets. */
type ScanContext = 'top' | 'not' | 'nth' | 'paren' | 'bracket';

/**
 * Reads a selector the way the proxy's selector engine (cascadia) tokenises
 * it: quoted strings, backslash escapes (decoded in pseudo-class names),
 * comments as whitespace, and nested brackets and parentheses.
 */
class SelectorScanner {
  private i = 0;
  private readonly stack: ScanContext[] = [];
  private compounds = 1;

  constructor(private readonly s: string) {}

  scan(): string | undefined {
    const s = this.s;
    this.i = this.skipSpace(0);
    while (this.i < s.length) {
      const problem = this.step();
      if (problem !== undefined) {
        return problem;
      }
    }
    if (this.compounds > MAX_HIDE_COMPOUNDS) {
      return `has more than ${MAX_HIDE_COMPOUNDS} compound selectors`;
    }
    return undefined;
  }

  private get context(): ScanContext {
    return this.stack.length === 0 ? 'top' : this.stack[this.stack.length - 1];
  }

  /** Reads one token at `this.i` and returns a problem, if any. */
  private step(): string | undefined {
    const s = this.s;
    const c = s[this.i];
    const context = this.context;

    if (c === '\\') {
      this.i = this.readEscape(this.i)[1];
      return undefined;
    }
    if (this.isComment(this.i)) {
      return this.space(context);
    }
    if (c === '"' || c === "'") {
      this.i = this.skipString(this.i);
      return undefined;
    }
    if (context === 'bracket') {
      if (c === '#' && s[this.i + 1] === '=') {
        return 'uses the #= (regular expression) attribute operator, which the proxy does not support';
      }
      if (c === ']') {
        this.stack.pop();
      }
      this.i++;
      return undefined;
    }
    if (context === 'nth' || context === 'paren') {
      if (c === '(') {
        this.stack.push('paren');
      } else if (c === ')') {
        this.stack.pop();
      }
      this.i++;
      return undefined;
    }

    // The top level or a :not() argument.
    switch (c) {
      case '[':
        this.stack.push('bracket');
        this.i++;
        return undefined;
      case '(':
        this.stack.push('paren');
        this.i++;
        return undefined;
      case ')':
        this.stack.pop();
        this.i++;
        return undefined;
      case ':':
        return this.pseudoClass();
      case ',':
        if (context === 'top') {
          this.compounds++;
        }
        this.i = this.skipSpace(this.i + 1);
        return undefined;
      case '>':
      case '+':
      case '~':
        if (context === 'not') {
          return NOT_COMBINATOR_MESSAGE;
        }
        this.compounds++;
        this.i = this.skipSpace(this.i + 1);
        return undefined;
    }
    if (isSpace(c)) {
      return this.space(context);
    }
    this.i++;
    return undefined;
  }

  /** Whitespace or a comment: a descendant combinator unless it is next to a comma, a combinator, `)` or the end. */
  private space(context: ScanContext): string | undefined {
    const j = this.skipSpace(this.i);
    this.i = j;
    if (j >= this.s.length || [',', ')', '>', '+', '~'].includes(this.s[j])) {
      return undefined;
    }
    if (context === 'not') {
      return NOT_COMBINATOR_MESSAGE;
    }
    if (context === 'top') {
      this.compounds++;
    }
    return undefined;
  }

  /** A pseudo-class at `this.i` (the colon). */
  private pseudoClass(): string | undefined {
    const s = this.s;
    this.i++;
    if (s[this.i] === ':') {
      return 'uses a pseudo-element, which the proxy does not support';
    }
    const name = toLowerAscii(this.readName());
    const expensive = EXPENSIVE_PSEUDO_CLASSES.get(name);
    if (expensive !== undefined) {
      return `uses ${expensive}, which the proxy does not support`;
    }
    if (!SUPPORTED_PSEUDO_CLASSES.has(name)) {
      const parens = s[this.i] === '(' ? '()' : '';
      return name === '' ? 'uses an invalid pseudo-class' : `uses :${name}${parens}, which the proxy does not support`;
    }
    if (s[this.i] === '(') {
      this.stack.push(name === 'not' ? 'not' : NTH_PSEUDO_CLASSES.has(name) ? 'nth' : 'paren');
      this.i = this.skipSpace(this.i + 1);
    }
    return undefined;
  }

  /** Reads a name at `this.i`, decoding escapes. */
  private readName(): string {
    const s = this.s;
    let name = '';
    while (this.i < s.length) {
      const c = s[this.i];
      if (c === '\\') {
        const [decoded, next] = this.readEscape(this.i);
        name += decoded;
        this.i = next;
      } else if (isNameChar(c)) {
        name += c;
        this.i++;
      } else {
        break;
      }
    }
    return name;
  }

  /** Decodes the backslash escape at `i`: up to six hex digits and one optional whitespace character, or one other character. */
  private readEscape(i: number): [string, number] {
    const s = this.s;
    if (i + 1 >= s.length) {
      return ['', s.length];
    }
    if (!isHex(s[i + 1])) {
      const cp = s.codePointAt(i + 1) ?? 0;
      const ch = String.fromCodePoint(cp);
      return [ch, i + 1 + ch.length];
    }
    let j = i + 1;
    while (j < s.length && j < i + 7 && isHex(s[j])) {
      j++;
    }
    const cp = parseInt(s.slice(i + 1, j), 16);
    if (s[j] === '\r') {
      j++;
      if (s[j] === '\n') {
        j++;
      }
    } else if (s[j] === ' ' || s[j] === '\t' || s[j] === '\n' || s[j] === '\f') {
      j++;
    }
    const decoded = cp > 0x10ffff || (cp >= 0xd800 && cp <= 0xdfff) ? '�' : String.fromCodePoint(cp);
    return [decoded, j];
  }

  /** True when a terminated comment starts at `i`. */
  private isComment(i: number): boolean {
    return this.s.startsWith('/*', i) && this.s.indexOf('*/', i + 2) !== -1;
  }

  /** The index after the whitespace and comments at `i`. */
  private skipSpace(i: number): number {
    const s = this.s;
    while (i < s.length) {
      if (isSpace(s[i])) {
        i++;
      } else if (this.isComment(i)) {
        i = s.indexOf('*/', i + 2) + 2;
      } else {
        break;
      }
    }
    return i;
  }

  /** The index after the quoted string at `i`. */
  private skipString(i: number): number {
    const s = this.s;
    const quote = s[i];
    for (let j = i + 1; j < s.length; j++) {
      if (s[j] === '\\') {
        j++;
      } else if (s[j] === quote) {
        return j + 1;
      }
    }
    return s.length;
  }
}
