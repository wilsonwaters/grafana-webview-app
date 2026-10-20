import { MAX_WEBVIEW_URL_LENGTH, validateWebViewUrl, type WebViewUrlRejection } from './url';

describe('panels/webview/url validateWebViewUrl (VR-1)', () => {
  describe('accepts absolute http(s) URLs', () => {
    test.each([
      ['https://example.com', 'https://example.com', 'https://example.com'],
      ['http://example.com', 'http://example.com', 'http://example.com'],
      ['https://example.com/path?a=1&b=2#frag', 'https://example.com/path?a=1&b=2#frag', 'https://example.com'],
      ['https://example.com:8443/x', 'https://example.com:8443/x', 'https://example.com:8443'],
      ['https://example.com:443/', 'https://example.com:443/', 'https://example.com'],
      ['HTTPS://EXAMPLE.COM/Path', 'HTTPS://EXAMPLE.COM/Path', 'https://example.com'],
      ['HtTp://example.com', 'HtTp://example.com', 'http://example.com'],
      ['http://192.0.2.10/', 'http://192.0.2.10/', 'http://192.0.2.10'],
      ['http://[2001:db8::1]:8080/', 'http://[2001:db8::1]:8080/', 'http://[2001:db8::1]:8080'],
      // IDN host: accepted, the origin is the punycode form.
      ['https://bücher.example/', 'https://bücher.example/', 'https://xn--bcher-kva.example'],
      ['https://xn--bcher-kva.example/', 'https://xn--bcher-kva.example/', 'https://xn--bcher-kva.example'],
    ])('%j', (input, url, origin) => {
      expect(validateWebViewUrl(input)).toEqual({ valid: true, url, origin });
    });

    test('surrounding whitespace is trimmed and the trimmed URL is returned', () => {
      expect(validateWebViewUrl('  https://example.com/  ')).toEqual({
        valid: true,
        url: 'https://example.com/',
        origin: 'https://example.com',
      });
      expect(validateWebViewUrl('https://example.com\n')).toMatchObject({ valid: true, url: 'https://example.com' });
    });

    test('a URL of exactly the maximum length is accepted', () => {
      const prefix = 'https://example.com/';
      const url = prefix + 'a'.repeat(MAX_WEBVIEW_URL_LENGTH - prefix.length);
      expect(url).toHaveLength(MAX_WEBVIEW_URL_LENGTH);
      expect(validateWebViewUrl(url)).toMatchObject({ valid: true, url });
    });
  });

  describe('rejects unsafe or unsupported values', () => {
    const cases: Array<[string, unknown, WebViewUrlRejection]> = [
      // Empty
      ['empty string', '', 'empty'],
      ['whitespace only', '   \n\t ', 'empty'],
      ['undefined', undefined, 'empty'],
      ['null', null, 'empty'],
      ['number', 42, 'empty'],
      // Dangerous schemes
      ['javascript:', 'javascript:alert(1)', 'scheme'],
      ['javascript: with a comment payload', 'javascript://example.com/%0Aalert(1)', 'scheme'],
      ['data: HTML', 'data:text/html,<script>alert(1)</script>', 'scheme'],
      ['data: base64', 'data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==', 'scheme'],
      ['blob:', 'blob:https://example.com/0b8f1c1e-6a8b-4c4e-9a43-1f2d3e4f5a6b', 'scheme'],
      ['vbscript:', 'vbscript:msgbox(1)', 'scheme'],
      ['file:', 'file:///etc/passwd', 'scheme'],
      ['ftp:', 'ftp://example.com/', 'scheme'],
      ['about:blank', 'about:blank', 'scheme'],
      ['ws:', 'ws://example.com/', 'scheme'],
      // Letter-case variants
      ['upper case JAVASCRIPT:', 'JAVASCRIPT:alert(1)', 'scheme'],
      ['mixed case JaVaScRiPt:', 'JaVaScRiPt:alert(1)', 'scheme'],
      ['mixed case DaTa:', 'DaTa:text/html,x', 'scheme'],
      ['mixed case VbScRiPt:', 'VbScRiPt:msgbox(1)', 'scheme'],
      // Whitespace variants (trimmed, then rejected by scheme)
      ['leading space javascript:', '   javascript:alert(1)', 'scheme'],
      ['leading newline javascript:', '\njavascript:alert(1)', 'scheme'],
      ['leading NBSP javascript:', '\u00A0javascript:alert(1)', 'scheme'],
      ['leading BOM data:', '\uFEFFdata:text/html,x', 'scheme'],
      // Control-character variants
      ['leading C0 control javascript:', '\u0001javascript:alert(1)', 'control-characters'],
      ['tab inside the scheme', 'java\tscript:alert(1)', 'control-characters'],
      ['newline inside the scheme', 'java\nscript:alert(1)', 'control-characters'],
      ['carriage return inside the scheme', 'java\rscript:alert(1)', 'control-characters'],
      ['NUL inside an http URL', 'https://exa\u0000mple.com/', 'control-characters'],
      ['DEL inside an http URL', 'https://example.com/\u007F', 'control-characters'],
      ['C1 control inside an http URL', 'https://example.com/\u0085', 'control-characters'],
      ['line separator inside an http URL', 'https://example.com/a\u2028b', 'control-characters'],
      ['tab inside an http URL', 'https://exam\tple.com/', 'control-characters'],
      // Relative and protocol-relative URLs
      ['root-relative path', '/api/user', 'not-absolute'],
      ['relative path', 'dashboards/home', 'not-absolute'],
      ['bare host', 'example.com', 'not-absolute'],
      ['protocol-relative', '//example.com/', 'not-absolute'],
      ['query only', '?url=https://example.com', 'not-absolute'],
      ['markup probe', '"><img src=x onerror=alert(1)>', 'not-absolute'],
      // Not a plain absolute http(s) URL
      ['http: without slashes', 'http:example.com', 'not-absolute'],
      ['https: with one slash', 'https:/example.com', 'not-absolute'],
      ['https: with backslashes', 'https:\\\\example.com', 'not-absolute'],
      // Malformed
      ['http with no host', 'http://', 'malformed'],
      ['https with an invalid host', 'https://exa mple.com/', 'malformed'],
      ['https with an invalid port', 'https://example.com:99999/', 'malformed'],
      // Credentials
      ['user and password', 'https://user:secret@example.com/', 'credentials'],
      ['user only', 'https://user@example.com/', 'credentials'],
      ['empty user with password', 'https://:secret@example.com/', 'credentials'],
    ];

    test.each(cases)('%s', (_name, input, reason) => {
      const result = validateWebViewUrl(input);
      expect(result.valid).toBe(false);
      if (!result.valid) {
        expect(result.reason).toBe(reason);
        expect(result.message.length).toBeGreaterThan(0);
      }
    });

    test('a URL longer than the maximum is rejected', () => {
      const prefix = 'https://example.com/';
      const url = prefix + 'a'.repeat(MAX_WEBVIEW_URL_LENGTH - prefix.length + 1);
      expect(validateWebViewUrl(url)).toMatchObject({ valid: false, reason: 'too-long' });
    });

    test('a very long javascript: URL is rejected', () => {
      expect(validateWebViewUrl('javascript:' + 'a'.repeat(100_000))).toMatchObject({ valid: false });
    });

    test('the error message never echoes the rejected URL', () => {
      const payload = 'javascript:alert(document.cookie)';
      const result = validateWebViewUrl(payload);
      expect(result.valid).toBe(false);
      if (!result.valid) {
        expect(result.message).not.toContain('alert');
        expect(result.message).not.toContain('javascript');
      }
    });
  });
});
