import { DEFAULT_PLUGIN_SETTINGS } from '../../settingsTypes';
import {
  buildJsonData,
  hasErrors,
  LIMIT_KEYS,
  LimitKey,
  parsePorts,
  SettingsFormState,
  toFormState,
  validateForm,
  validateHostname,
} from './settingsForm';

function emptyLimits(): Record<LimitKey, string> {
  return Object.fromEntries(LIMIT_KEYS.map((k) => [k, ''])) as Record<LimitKey, string>;
}

function row(domain: string, extra: Partial<SettingsFormState['domains'][number]> = {}) {
  return {
    key: `k-${domain}-${Math.random()}`,
    domain,
    allowSubdomains: false,
    allowPrivateIP: false,
    rateLimitPerMin: '',
    allowedPorts: '',
    ...extra,
  };
}

describe('validateHostname', () => {
  // The expected canonical values match pkg/security NormalizeHostname.
  test.each([
    ['example.com', 'example.com'],
    ['  Example.COM  ', 'example.com'],
    ['example.com.', 'example.com'],
    ['status.example.co.uk', 'status.example.co.uk'],
    ['localhost', 'localhost'],
    ['bücher.de', 'xn--bcher-kva.de'],
    ['xn--bcher-kva.de', 'xn--bcher-kva.de'],
    ['192.168.1.10', '192.168.1.10'],
    ['fd00::1', 'fd00::1'],
    [`${'a'.repeat(63)}.com`, `${'a'.repeat(63)}.com`],
  ])('accepts %j', (input, canonical) => {
    expect(validateHostname(input)).toEqual({ ok: true, canonical });
  });

  test.each([
    ['', /enter a domain name/i],
    ['   ', /enter a domain name/i],
    ['https://example.com', /remove the scheme/i],
    ['example.com/path', /remove the path/i],
    ['example.com?q=1', /remove the path/i],
    ['example.com:8080', /remove the port/i],
    ['[::1]:8443', /remove the port/i],
    ['[::1]', /remove the brackets/i],
    ['*.example.com', /wildcards are not supported/i],
    ['user@example.com', /remove the user name/i],
    ['exa mple.com', /cannot contain spaces/i],
    ['a_b.com', /only letters, digits/i],
    ['ex%61mple.com', /only letters, digits/i],
    ['-a.com', /start or end with a hyphen/i],
    ['ab--c.com', /third and fourth positions/i],
    ['a..b', /empty part/i],
    ['example.com..', /extra dots/i],
    [`${'a'.repeat(64)}.com`, /63 characters/i],
    ['127.1', /four decimal numbers/i],
    ['xn--a.com', /valid domain name/i],
  ])('rejects %j', (input, message) => {
    const result = validateHostname(input);
    expect(result.ok).toBe(false);
    expect(result.ok ? '' : result.error).toMatch(message);
  });
});

describe('parsePorts', () => {
  test('parses a comma-separated list and removes duplicates', () => {
    expect(parsePorts(' 8080, 8443,,8080 ')).toEqual({ ok: true, ports: [8080, 8443] });
  });

  test('treats empty text as no extra ports', () => {
    expect(parsePorts('')).toEqual({ ok: true, ports: [] });
  });

  test.each(['0', '65536', 'http', '80.5', '-1'])('rejects %j', (text) => {
    expect(parsePorts(text).ok).toBe(false);
  });

  test('accepts the port range limits', () => {
    expect(parsePorts('1, 65535')).toEqual({ ok: true, ports: [1, 65535] });
  });
});

describe('validateForm', () => {
  const base = (): SettingsFormState => ({ domains: [], limits: emptyLimits() });

  test('an empty allowlist with default limits is valid', () => {
    expect(hasErrors(validateForm(base()))).toBe(false);
  });

  test('allows 0 redirects but rejects 0 for limits where the backend would apply the default', () => {
    const state = base();
    state.limits.maxRedirects = '0';
    state.limits.requestTimeoutSec = '0';
    const errors = validateForm(state);
    expect(errors.limits.maxRedirects).toBeUndefined();
    expect(errors.limits.requestTimeoutSec).toMatch(/leave the field empty/i);
  });

  test.each([
    ['requestTimeoutSec', '-1', /negative/i],
    ['maxConcurrentRequests', '1.5', /whole number/i],
    ['rateLimitPerInstancePerMin', 'ten', /whole number/i],
    ['rateLimitPerDomainPerMin', '2147483648', /2147483647 or less/i],
  ] as Array<[LimitKey, string, RegExp]>)('rejects %s = %j', (key, value, message) => {
    const state = base();
    state.limits[key] = value;
    expect(validateForm(state).limits[key]).toMatch(message);
  });

  test('accepts a response size above the 32-bit range (int64 on the backend)', () => {
    const state = base();
    state.limits.maxResponseBytes = '4294967296';
    expect(hasErrors(validateForm(state))).toBe(false);
  });

  test('flags duplicate domains after canonicalisation', () => {
    const state = base();
    state.domains = [row('example.com'), row('EXAMPLE.com.')];
    const errors = validateForm(state);
    expect(errors.domains[state.domains[0].key]).toBeUndefined();
    expect(errors.domains[state.domains[1].key]?.domain).toMatch(/already in entry 1/i);
  });

  test('validates the per-domain rate limit and ports', () => {
    const state = base();
    state.domains = [row('example.com', { rateLimitPerMin: '-5', allowedPorts: '8080, 70000' })];
    const errors = validateForm(state).domains[state.domains[0].key];
    expect(errors?.rateLimitPerMin).toMatch(/negative/i);
    expect(errors?.allowedPorts).toMatch(/out of range/i);
  });
});

describe('toFormState', () => {
  test('reads the provisioned shape and fills option defaults', () => {
    const state = toFormState({
      allowedDomains: [
        { domain: 'example.com', options: { allowSubdomains: true } },
        { domain: 'intranet.local', options: { allowPrivateIP: true, rateLimitPerMin: 5, allowedPorts: [8443, 9000] } },
      ],
      requestTimeoutSec: 20,
      maxRedirects: 0,
    });
    expect(state.domains).toHaveLength(2);
    expect(state.domains[0]).toMatchObject({
      domain: 'example.com',
      allowSubdomains: true,
      allowPrivateIP: false,
      rateLimitPerMin: '',
      allowedPorts: '',
    });
    expect(state.domains[1]).toMatchObject({
      allowPrivateIP: true,
      rateLimitPerMin: '5',
      allowedPorts: '8443, 9000',
    });
    expect(state.limits).toMatchObject({ requestTimeoutSec: '20', maxRedirects: '0', maxResponseBytes: '' });
  });

  test('handles missing jsonData', () => {
    expect(toFormState(undefined)).toEqual({ domains: [], limits: emptyLimits() });
  });

  test('shows stored values of the wrong type so that validation flags them', () => {
    const state = toFormState({
      maxRedirects: -1,
      allowedDomains: [{ domain: 'x.com', options: { allowedPorts: 'abc' } }],
    });
    expect(state.limits.maxRedirects).toBe('-1');
    expect(hasErrors(validateForm(state))).toBe(true);
  });
});

describe('buildJsonData', () => {
  test('keeps unknown keys, removes the scaffold apiUrl and writes typed values', () => {
    const existing = {
      apiUrl: 'http://default-url.com',
      someOtherKey: { nested: true },
      maxResponseBytes: 1000,
      allowedDomains: [{ domain: 'old.example.com', note: 'kept', options: { allowSubdomains: false, future: 1 } }],
    };
    const state = toFormState(existing);
    state.domains[0].allowPrivateIP = true;
    state.domains[0].allowedPorts = '8443';
    state.domains[0].rateLimitPerMin = '12';
    state.limits.maxResponseBytes = '';
    state.limits.maxRedirects = '0';

    const next = buildJsonData(existing, state);

    expect(next).toEqual({
      someOtherKey: { nested: true },
      maxRedirects: 0,
      allowedDomains: [
        {
          domain: 'old.example.com',
          note: 'kept',
          options: {
            allowSubdomains: false,
            allowPrivateIP: true,
            rateLimitPerMin: 12,
            allowedPorts: [8443],
            future: 1,
          },
        },
      ],
    });
    expect(next).not.toHaveProperty('apiUrl');
    expect(next).not.toHaveProperty('apiKey');
    expect(next).not.toHaveProperty('maxResponseBytes');
  });

  test('writes an empty allowlist when every entry is removed', () => {
    const existing = { allowedDomains: [{ domain: 'example.com' }] };
    const state = toFormState(existing);
    state.domains = [];
    expect(buildJsonData(existing, state)).toEqual({ allowedDomains: [] });
  });

  test('stores the default rate-limit override as 0 and trims the domain', () => {
    const next = buildJsonData({}, { domains: [row('  example.net ')], limits: emptyLimits() });
    expect(next.allowedDomains).toEqual([
      {
        domain: 'example.net',
        options: { allowSubdomains: false, allowPrivateIP: false, rateLimitPerMin: 0, allowedPorts: [] },
      },
    ]);
  });

  test('the defaults shown in the form match the backend defaults', () => {
    expect(DEFAULT_PLUGIN_SETTINGS).toMatchObject({
      maxResponseBytes: 5242880,
      requestTimeoutSec: 10,
      maxRedirects: 3,
      rateLimitPerInstancePerMin: 60,
      rateLimitPerDomainPerMin: 30,
      maxConcurrentRequests: 10,
    });
  });
});
