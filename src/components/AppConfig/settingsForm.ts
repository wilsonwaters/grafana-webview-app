/**
 * Form model, validation and save-payload logic for the app Configuration page.
 *
 * The validation mirrors how the backend reads jsonData (pkg/plugin/config.go)
 * and canonicalises allowlist domains (pkg/security NormalizeHostname). Where
 * the backend silently skips or replaces a value, the form rejects it instead,
 * so that what an admin sees is what the proxy enforces.
 */
import { DEFAULT_DOMAIN_OPTIONS, DEFAULT_PLUGIN_SETTINGS, PluginSettings } from '../../settingsTypes';

export type JsonData = Record<string, unknown>;

export type LimitKey = Exclude<keyof PluginSettings, 'allowedDomains'>;

/** jsonData keys from the create-plugin scaffold. The backend never reads them (VR-13). */
export const LEGACY_JSON_DATA_KEYS: readonly string[] = ['apiUrl'];

/** Largest value a Go `int` can hold on every platform the backend is built for (linux/arm is 32-bit). */
export const GO_INT32_MAX = 2147483647;

export interface LimitSpec {
  key: LimitKey;
  label: string;
  description: string;
  unit: string;
  min: number;
  max: number;
}

export const LIMIT_SPECS: readonly LimitSpec[] = [
  {
    key: 'maxResponseBytes',
    label: 'Maximum response size',
    description: 'Larger upstream responses are rejected with HTTP 413.',
    unit: 'bytes',
    min: 1,
    // Go int64 on the backend; JavaScript can only represent integers exactly up to 2^53 - 1.
    max: Number.MAX_SAFE_INTEGER,
  },
  {
    key: 'requestTimeoutSec',
    label: 'Request timeout',
    description: 'Time budget for one proxied request, including the response body.',
    unit: 'seconds',
    min: 1,
    max: GO_INT32_MAX,
  },
  {
    key: 'maxRedirects',
    label: 'Maximum redirects',
    description: 'Each redirect target is checked against the allowlist again. 0 disables redirects.',
    unit: 'redirects',
    min: 0,
    max: GO_INT32_MAX,
  },
  {
    key: 'rateLimitPerInstancePerMin',
    label: 'Rate limit per Grafana instance',
    description: 'Total proxy requests per minute for this Grafana instance.',
    unit: 'requests/min',
    min: 1,
    max: GO_INT32_MAX,
  },
  {
    key: 'rateLimitPerDomainPerMin',
    label: 'Rate limit per domain',
    description: 'Proxy requests per minute to one domain. An entry can override this value.',
    unit: 'requests/min',
    min: 1,
    max: GO_INT32_MAX,
  },
  {
    key: 'maxConcurrentRequests',
    label: 'Maximum concurrent requests',
    description: 'Proxy requests that can run at the same time.',
    unit: 'requests',
    min: 1,
    max: GO_INT32_MAX,
  },
];

export const LIMIT_KEYS: readonly LimitKey[] = LIMIT_SPECS.map((s) => s.key);

/** One editable allowlist entry. Numeric fields are kept as the text the admin typed. */
export interface DomainRow {
  /** Stable React key and element-id stem. Not saved. */
  key: string;
  domain: string;
  allowSubdomains: boolean;
  allowPrivateIP: boolean;
  /** Empty means 0 (use the global per-domain limit). */
  rateLimitPerMin: string;
  /** Comma-separated list of extra TCP ports. */
  allowedPorts: string;
  /** The stored entry this row came from. Its unknown keys are kept on save. */
  source?: Record<string, unknown>;
}

export interface SettingsFormState {
  domains: DomainRow[];
  /** Empty means "use the default". */
  limits: Record<LimitKey, string>;
}

export type DomainField = 'domain' | 'rateLimitPerMin' | 'allowedPorts';

export interface SettingsFormErrors {
  domains: Record<string, Partial<Record<DomainField, string>>>;
  limits: Partial<Record<LimitKey, string>>;
}

let rowCounter = 0;

export function newRowKey(): string {
  rowCounter += 1;
  return `entry-${rowCounter}`;
}

export function emptyDomainRow(): DomainRow {
  return {
    key: newRowKey(),
    domain: '',
    allowSubdomains: DEFAULT_DOMAIN_OPTIONS.allowSubdomains,
    allowPrivateIP: DEFAULT_DOMAIN_OPTIONS.allowPrivateIP,
    rateLimitPerMin: '',
    allowedPorts: '',
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/** Shows a stored value as editable text. Values of the wrong type are shown so that validation can flag them. */
function toText(value: unknown): string {
  if (value === undefined || value === null) {
    return '';
  }
  if (typeof value === 'number' || typeof value === 'string') {
    return String(value);
  }
  return JSON.stringify(value);
}

function portsToText(value: unknown): string {
  if (Array.isArray(value)) {
    return value.map(toText).join(', ');
  }
  return toText(value);
}

function rowFromStored(entry: unknown): DomainRow {
  const source = isRecord(entry) ? entry : {};
  const options = isRecord(source.options) ? source.options : {};
  const rate = options.rateLimitPerMin;
  return {
    key: newRowKey(),
    domain: typeof source.domain === 'string' ? source.domain : toText(source.domain),
    allowSubdomains: options.allowSubdomains === true,
    allowPrivateIP: options.allowPrivateIP === true,
    rateLimitPerMin: rate === 0 ? '' : toText(rate),
    allowedPorts: portsToText(options.allowedPorts),
    source,
  };
}

/** Builds the form state from the plugin's stored jsonData. */
export function toFormState(jsonData: JsonData | undefined | null): SettingsFormState {
  const data = jsonData ?? {};
  const stored = Array.isArray(data.allowedDomains) ? data.allowedDomains : [];
  const limits = {} as Record<LimitKey, string>;
  for (const key of LIMIT_KEYS) {
    limits[key] = toText(data[key]);
  }
  return { domains: stored.map(rowFromStored), limits };
}

// ---------------------------------------------------------------------------
// Validation
// ---------------------------------------------------------------------------

export type HostnameResult = { ok: true; canonical: string } | { ok: false; error: string };

const IPV4_DOTTED_DECIMAL = /^(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)(\.(25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)){3}$/;
const LDH_LABEL = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/;
// ASCII characters that cannot be part of a domain name (letters, digits, '-' and '.' are allowed).
const FORBIDDEN_ASCII = /[\u0000-,/:-@[-`{-\u007f]/;

function canonicalIPv6(value: string): string | undefined {
  if (!/^[0-9a-f:.]+$/i.test(value)) {
    return undefined;
  }
  try {
    return new URL(`http://[${value}]/`).hostname.slice(1, -1);
  } catch {
    return undefined;
  }
}

/**
 * Validates an allowlist domain the way the backend canonicalises it
 * (NormalizeHostname: one trailing dot removed, lower case, IDNA lookup
 * profile with STD3 rules and hyphen checks; IP literals kept as IPs).
 *
 * The form is stricter than the backend for values the backend accepts but
 * that can never match a request host: empty labels, labels over 63
 * characters, names over 253 characters and non-canonical IPv4 forms.
 */
export function validateHostname(input: string): HostnameResult {
  const value = input.trim();
  if (value === '') {
    return { ok: false, error: 'Enter a domain name, for example status.example.com.' };
  }
  if (/\s/.test(value)) {
    return { ok: false, error: 'A domain name cannot contain spaces.' };
  }
  if (/^[a-z][a-z0-9+.-]*:\/\//i.test(value) || value.includes('://')) {
    return { ok: false, error: 'Remove the scheme (for example https://). Enter the host name only.' };
  }
  if (value.includes('*')) {
    return {
      ok: false,
      error: 'Wildcards are not supported. Enter the parent domain and select "Allow subdomains".',
    };
  }
  if (/[/?#\\]/.test(value)) {
    return { ok: false, error: 'Remove the path. Enter the host name only.' };
  }
  if (value.includes('@')) {
    return { ok: false, error: 'Remove the user name. Enter the host name only.' };
  }
  if (/[:[\]]/.test(value)) {
    const ipv6 = canonicalIPv6(value);
    if (ipv6 !== undefined) {
      return { ok: true, canonical: ipv6 };
    }
    const bracketed = /^\[([^\]]*)\]$/.exec(value);
    if (bracketed && canonicalIPv6(bracketed[1]) !== undefined) {
      return { ok: false, error: 'Remove the brackets around the IPv6 address.' };
    }
    if (/^[^:[\]]+:\d*$/.test(value) || /^\[[^\]]*\]:\d*$/.test(value)) {
      return { ok: false, error: 'Remove the port. Enter extra ports in "Additional ports".' };
    }
    return { ok: false, error: 'Enter a valid domain name or IPv6 address.' };
  }
  // Like the backend, ignore one trailing dot (the DNS root label).
  const host = value.endsWith('.') ? value.slice(0, -1) : value;
  if (IPV4_DOTTED_DECIMAL.test(host)) {
    return { ok: true, canonical: host };
  }
  if (host === '' || host.endsWith('.')) {
    return { ok: false, error: 'Enter a valid domain name. Remove the extra dots at the end.' };
  }
  if (FORBIDDEN_ASCII.test(host)) {
    return { ok: false, error: 'A domain name can contain only letters, digits, hyphens (-) and dots (.).' };
  }

  let canonical: string;
  try {
    canonical = new URL(`http://${host}/`).hostname;
  } catch {
    return { ok: false, error: 'Enter a valid domain name, for example status.example.com.' };
  }
  if (IPV4_DOTTED_DECIMAL.test(canonical)) {
    // The URL parser turned a numeric short form (for example 127.1 or 0x7f.0.0.1) into an IPv4 address.
    return { ok: false, error: 'Enter an IPv4 address as four decimal numbers, for example 192.168.1.10.' };
  }
  if (canonical.length > 253) {
    return { ok: false, error: 'The domain name is too long (maximum 253 characters).' };
  }
  for (const label of canonical.split('.')) {
    if (label === '') {
      return { ok: false, error: 'Enter a valid domain name. Remove the empty part between two dots.' };
    }
    if (label.length > 63) {
      return { ok: false, error: 'Each part of the domain name must be 63 characters or fewer.' };
    }
    if (label.startsWith('-') || label.endsWith('-')) {
      return { ok: false, error: 'A part of the domain name cannot start or end with a hyphen (-).' };
    }
    if (!LDH_LABEL.test(label)) {
      return { ok: false, error: 'A domain name can contain only letters, digits, hyphens (-) and dots (.).' };
    }
    if (label.slice(2, 4) === '--' && !label.startsWith('xn--')) {
      return {
        ok: false,
        error: 'A part of the domain name cannot have hyphens (-) in the third and fourth positions.',
      };
    }
  }
  return { ok: true, canonical };
}

type IntResult = { ok: true; value: number } | { ok: false; error: string };

function parseWholeNumber(text: string, min: number, max: number): IntResult {
  const value = text.trim();
  if (!/^\d+$/.test(value)) {
    if (/^-\d+$/.test(value)) {
      return { ok: false, error: `Enter ${min} or more. Negative values are not allowed.` };
    }
    return { ok: false, error: 'Enter a whole number, for example 10.' };
  }
  const n = Number(value);
  if (n < min) {
    return { ok: false, error: `Enter ${min} or more.` };
  }
  if (n > max) {
    return { ok: false, error: `Enter ${max} or less.` };
  }
  return { ok: true, value: n };
}

function limitError(spec: LimitSpec, text: string): string | undefined {
  if (text.trim() === '') {
    return undefined;
  }
  const result = parseWholeNumber(text, spec.min, spec.max);
  if (result.ok) {
    return undefined;
  }
  if (spec.min === 1 && /^0+$/.test(text.trim())) {
    return `Enter 1 or more. To use the default (${DEFAULT_PLUGIN_SETTINGS[spec.key]}), leave the field empty.`;
  }
  return result.error;
}

type PortsResult = { ok: true; ports: number[] } | { ok: false; error: string };

/** Parses "8080, 8443" into [8080, 8443]. Empty text means no extra ports. Duplicates are removed. */
export function parsePorts(text: string): PortsResult {
  const parts = text
    .split(',')
    .map((p) => p.trim())
    .filter((p) => p !== '');
  const ports: number[] = [];
  for (const part of parts) {
    if (!/^\d+$/.test(part)) {
      return { ok: false, error: `"${part}" is not a port number. Enter ports from 1 to 65535, separated by commas.` };
    }
    const port = Number(part);
    if (port < 1 || port > 65535) {
      return { ok: false, error: `Port ${part} is out of range. Enter ports from 1 to 65535.` };
    }
    if (!ports.includes(port)) {
      ports.push(port);
    }
  }
  return { ok: true, ports };
}

export function validateForm(state: SettingsFormState): SettingsFormErrors {
  const errors: SettingsFormErrors = { domains: {}, limits: {} };
  const seen = new Map<string, number>();

  state.domains.forEach((row, index) => {
    const rowErrors: Partial<Record<DomainField, string>> = {};
    const host = validateHostname(row.domain);
    if (!host.ok) {
      rowErrors.domain = host.error;
    } else if (seen.has(host.canonical)) {
      rowErrors.domain = `This domain is already in entry ${seen.get(host.canonical)}. Remove one of the entries.`;
    } else {
      seen.set(host.canonical, index + 1);
    }

    if (row.rateLimitPerMin.trim() !== '') {
      const rate = parseWholeNumber(row.rateLimitPerMin, 0, GO_INT32_MAX);
      if (!rate.ok) {
        rowErrors.rateLimitPerMin = rate.error;
      }
    }

    const ports = parsePorts(row.allowedPorts);
    if (!ports.ok) {
      rowErrors.allowedPorts = ports.error;
    }

    if (Object.keys(rowErrors).length > 0) {
      errors.domains[row.key] = rowErrors;
    }
  });

  for (const spec of LIMIT_SPECS) {
    const error = limitError(spec, state.limits[spec.key]);
    if (error) {
      errors.limits[spec.key] = error;
    }
  }
  return errors;
}

export function hasErrors(errors: SettingsFormErrors): boolean {
  return Object.keys(errors.domains).length > 0 || Object.keys(errors.limits).length > 0;
}

// ---------------------------------------------------------------------------
// Save payload
// ---------------------------------------------------------------------------

/**
 * Builds the jsonData to save. Starts from the stored jsonData so that keys
 * this page does not manage are kept, removes the unused scaffold keys, and
 * writes the settings this page manages. An empty limit field removes the key
 * so that the backend applies its default.
 *
 * Call only with a state that passes validateForm.
 */
export function buildJsonData(existing: JsonData | undefined | null, state: SettingsFormState): JsonData {
  const next: JsonData = { ...(existing ?? {}) };
  for (const key of LEGACY_JSON_DATA_KEYS) {
    delete next[key];
  }

  next.allowedDomains = state.domains.map((row) => {
    const source = row.source ?? {};
    const sourceOptions = isRecord(source.options) ? source.options : {};
    const ports = parsePorts(row.allowedPorts);
    return {
      ...source,
      domain: row.domain.trim(),
      options: {
        ...sourceOptions,
        allowSubdomains: row.allowSubdomains,
        allowPrivateIP: row.allowPrivateIP,
        rateLimitPerMin: row.rateLimitPerMin.trim() === '' ? 0 : Number(row.rateLimitPerMin.trim()),
        allowedPorts: ports.ok ? ports.ports : [],
      },
    };
  });

  for (const key of LIMIT_KEYS) {
    const text = state.limits[key].trim();
    if (text === '') {
      delete next[key];
    } else {
      next[key] = Number(text);
    }
  }
  return next;
}
