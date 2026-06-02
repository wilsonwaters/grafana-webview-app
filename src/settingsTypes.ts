/**
 * Admin-configurable plugin settings schema for the Web View plugin.
 *
 * These types mirror the Go PluginSettings / DomainOptions / AllowedDomain
 * structs defined in pkg/plugin/config.go and are consumed by the plugin
 * configuration UI (AppConfig — a later task) and any TypeScript code that
 * needs to read or write jsonData for this plugin.
 *
 * All values live in non-secret jsonData. Nothing here belongs in
 * secureJsonData because these are operational configuration fields that
 * admins should be able to inspect and audit.
 *
 * FAIL-CLOSED: a fresh install has an empty allowedDomains list, which means
 * the backend proxy refuses every request. An admin must explicitly add
 * domains before the proxy will serve anything.
 */

/**
 * Per-domain configuration that refines the security controls applied to a
 * specific entry in the allowlist. Matches the Go DomainOptions struct.
 */
export interface DomainOptions {
  /**
   * When true, the allowlist entry also covers all subdomains of the
   * configured domain (e.g. "example.com" also covers "api.example.com").
   * @default false (exact-match only)
   */
  allowSubdomains: boolean;

  /**
   * Explicit opt-in that permits the resolved IP address of this domain to
   * fall within RFC 1918 / link-local / loopback ranges. Must remain false
   * in almost all deployments; exists only for internal-status-board
   * use cases where the admin has accepted the SSRF risk.
   * @default false
   */
  allowPrivateIP: boolean;

  /**
   * Per-domain rate limit override in requests per minute. 0 means "use the
   * global rateLimitPerDomainPerMin default".
   * @default 0
   */
  rateLimitPerMin: number;

  /**
   * Additional TCP ports permitted for this domain beyond the default 80 and
   * 443. An empty array means only standard ports are allowed.
   * @default []
   */
  allowedPorts: number[];
}

/**
 * A single entry in the domain allowlist. Pairs a hostname pattern with its
 * per-domain options. Matches the Go AllowedDomain struct.
 *
 * The domain field is the bare hostname (e.g. "example.com") without scheme,
 * port, or path. Subdomain matching is governed by options.allowSubdomains.
 */
export interface AllowedDomain {
  /**
   * The hostname (or hostname pattern) that is permitted.
   * Must not include a scheme, port, or path component.
   */
  domain: string;

  /** Per-domain overrides for security controls. */
  options: DomainOptions;
}

/**
 * All admin-configurable plugin settings for the Web View plugin.
 * Stored in Grafana's non-secret jsonData for the plugin instance.
 *
 * Matches the Go PluginSettings struct (pkg/plugin/config.go).
 */
export interface PluginSettings {
  /**
   * Explicit allowlist of domains the proxy may fetch on behalf of dashboard
   * viewers. An empty list is the default — the proxy refuses all requests
   * until the admin explicitly adds domains.
   *
   * FAIL-CLOSED: missing or empty => proxy denies all requests (403).
   * @default []
   */
  allowedDomains: AllowedDomain[];

  /**
   * Maximum response body size in bytes. Responses larger than this limit are
   * rejected with 413 Payload Too Large.
   * @default 5242880 (5 MiB)
   */
  maxResponseBytes: number;

  /**
   * Per-request timeout in seconds, covering both TCP connection establishment
   * and the full response transfer. Requests that exceed this are cancelled
   * with 504.
   * @default 10
   */
  requestTimeoutSec: number;

  /**
   * Maximum number of HTTP redirects followed for a single proxy request.
   * Each redirect target is independently re-validated against the allowlist
   * and IP blocklist. 0 disables redirect following entirely.
   * @default 3
   */
  maxRedirects: number;

  /**
   * Maximum total proxy requests per minute across the entire Grafana instance
   * (token-bucket limiter). Requests that exceed the limit receive 429.
   * @default 60
   */
  rateLimitPerInstancePerMin: number;

  /**
   * Maximum proxy requests per minute directed at any single target domain.
   * @default 30
   */
  rateLimitPerDomainPerMin: number;

  /**
   * Maximum number of proxy requests in-flight simultaneously. Requests that
   * arrive when the cap is reached receive 429.
   * @default 10
   */
  maxConcurrentRequests: number;
}

/**
 * Safe defaults for all PluginSettings fields.
 *
 * These match the Go constants in pkg/plugin/config.go exactly:
 * - DefaultMaxResponseBytes       = 5 * 1024 * 1024
 * - DefaultRequestTimeoutSec      = 10
 * - DefaultMaxRedirects           = 3
 * - DefaultRateLimitPerInstancePerMin = 60
 * - DefaultRateLimitPerDomainPerMin   = 30
 * - DefaultMaxConcurrentRequests  = 10
 *
 * allowedDomains defaults to [] — fail-closed (proxy denies all by default).
 */
export const DEFAULT_PLUGIN_SETTINGS: Readonly<PluginSettings> = {
  allowedDomains: [],
  maxResponseBytes: 5 * 1024 * 1024,
  requestTimeoutSec: 10,
  maxRedirects: 3,
  rateLimitPerInstancePerMin: 60,
  rateLimitPerDomainPerMin: 30,
  maxConcurrentRequests: 10,
};

/**
 * Safe defaults for a DomainOptions entry.
 * All security-tightening flags are false/empty by default.
 */
export const DEFAULT_DOMAIN_OPTIONS: Readonly<DomainOptions> = {
  allowSubdomains: false,
  allowPrivateIP: false,
  rateLimitPerMin: 0,
  allowedPorts: [],
};
