/**
 * URL validation for the Web View panel (security review VR-1).
 *
 * The panel `url` option is set by a dashboard editor and is shown to every
 * viewer, so it must never reach an iframe unless it is an absolute `http:` or
 * `https:` URL. A `javascript:` URL in an iframe `src` runs in the Grafana
 * origin, and a `data:` URL renders HTML that the editor controls.
 *
 * The check parses the value with the WHATWG URL parser, the same parser the
 * browser uses for the iframe `src`, so the URL that is checked is the URL that
 * loads. These pure functions have no React or Grafana dependency.
 */

/** Longest URL the panel accepts, in characters (after surrounding whitespace is trimmed). */
export const MAX_WEBVIEW_URL_LENGTH = 8192;

/** The only URL schemes the panel loads. The parser always returns them in lower case. */
const ALLOWED_PROTOCOLS: ReadonlySet<string> = new Set(['http:', 'https:']);

/**
 * The URL must start with `http://` or `https://` (any case). This rejects
 * forms that the URL parser accepts for special schemes but that are not a
 * plain absolute URL, for example `http:example.com` or `https:\\example.com`.
 */
const ABSOLUTE_HTTP_PREFIX = /^https?:\/\//i;

/** A scheme prefix such as `javascript:` or `data:`. Used only to choose an error message. */
const SCHEME_PREFIX = /^[a-z][a-z0-9+.-]*:/i;

/**
 * C0 controls (including tab, line feed and carriage return), DEL, C1 controls
 * and the Unicode line and paragraph separators. The URL parser silently removes
 * tabs and line breaks (`java\tscript:` parses as `javascript:`), so the panel
 * rejects every control character instead of relying on the parser.
 */
const CONTROL_CHARACTERS = /[\u0000-\u001F\u007F-\u009F\u2028\u2029]/;

/** Why a URL was rejected. */
export type WebViewUrlRejection =
  'empty' | 'too-long' | 'control-characters' | 'not-absolute' | 'scheme' | 'credentials' | 'malformed';

/** Result of {@link validateWebViewUrl}. */
export type WebViewUrlCheck =
  | {
      valid: true;
      /** The URL to load: the input with surrounding whitespace removed. */
      url: string;
      /** The serialised origin of the URL, for example `https://example.com`. */
      origin: string;
    }
  | {
      valid: false;
      reason: WebViewUrlRejection;
      /** A message for the panel author. It never contains the rejected URL. */
      message: string;
    };

const MESSAGES: Record<WebViewUrlRejection, string> = {
  empty: 'No URL configured. Set a URL in the panel options.',
  'too-long': `The URL is too long. The maximum is ${MAX_WEBVIEW_URL_LENGTH} characters.`,
  'control-characters': 'The URL contains control characters, such as tabs or line breaks. Remove them.',
  'not-absolute': 'Enter a full URL that starts with http:// or https://.',
  scheme: 'Only http:// and https:// URLs can be displayed.',
  credentials: 'URLs that contain a user name or password (user:password@host) are not supported. Remove them.',
  malformed: 'The URL is not valid. Enter a full URL that starts with http:// or https://.',
};

function reject(reason: WebViewUrlRejection): WebViewUrlCheck {
  return { valid: false, reason, message: MESSAGES[reason] };
}

/**
 * Validates a panel URL. Accepts only an absolute `http:` or `https:` URL with a
 * host and no credentials.
 *
 * - Surrounding whitespace is trimmed (a common paste artefact). The checks run
 *   on the trimmed value, and the trimmed value is the URL that loads.
 * - Rejected: empty input, `javascript:`, `data:`, `blob:`, `vbscript:`,
 *   `file:` and every other scheme (in any letter case), relative and
 *   protocol-relative URLs, URLs with control characters anywhere, URLs with a
 *   user name or password, and URLs longer than {@link MAX_WEBVIEW_URL_LENGTH}.
 * - International (IDN) host names are accepted. The browser converts them to
 *   punycode in the same way as the parser here.
 */
export function validateWebViewUrl(raw: unknown): WebViewUrlCheck {
  if (typeof raw !== 'string') {
    return reject('empty');
  }

  const trimmed = raw.trim();
  if (trimmed.length === 0) {
    return reject('empty');
  }
  if (trimmed.length > MAX_WEBVIEW_URL_LENGTH) {
    return reject('too-long');
  }
  if (CONTROL_CHARACTERS.test(trimmed)) {
    return reject('control-characters');
  }

  let parsed: URL;
  try {
    // No base URL: a relative or protocol-relative URL throws here.
    parsed = new URL(trimmed);
  } catch {
    return reject(SCHEME_PREFIX.test(trimmed) ? 'malformed' : 'not-absolute');
  }

  if (!ALLOWED_PROTOCOLS.has(parsed.protocol)) {
    return reject('scheme');
  }
  if (!ABSOLUTE_HTTP_PREFIX.test(trimmed)) {
    return reject('not-absolute');
  }
  if (parsed.username !== '' || parsed.password !== '') {
    return reject('credentials');
  }
  if (parsed.hostname === '') {
    return reject('malformed');
  }

  return { valid: true, url: trimmed, origin: parsed.origin };
}
