import type { PanelOptions } from '../../types';

/**
 * Panel option migrations, registered with `setMigrationHandler` in module.tsx.
 *
 * `detectedForUrl`: panels saved before this field existed have a Test URL
 * verdict (`detectedMode`) but no record of the URL it was measured for. The
 * verdict is assumed to belong to the URL saved with it, which is the URL the
 * author tested in almost every case. Recording that URL on load keeps the
 * panel rendering as before, and a later URL edit then makes the verdict stale
 * (see `detectedModeForUrl` in loadMode.ts). Treating such panels as not tested
 * would switch every saved Auto panel on a framing-blocked site to Direct, and
 * the site would refuse to load.
 */

/** True when `options` has a verdict but no `detectedForUrl` (a panel saved before the field existed). */
export function needsDetectedForUrl(options: unknown): boolean {
  if (typeof options !== 'object' || options === null) {
    return false;
  }
  const o = options as Record<string, unknown>;
  return (o.detectedMode === 'direct' || o.detectedMode === 'proxy') && typeof o.detectedForUrl !== 'string';
}

/**
 * Returns the stored options with the migrations applied. Other keys and
 * values are kept unchanged. Safe to run more than once.
 */
export function migratePanelOptions(options: Partial<PanelOptions> | undefined): Partial<PanelOptions> {
  const next: Partial<PanelOptions> = { ...(options ?? {}) };
  if (needsDetectedForUrl(next)) {
    next.detectedForUrl = typeof next.url === 'string' ? next.url.trim() : '';
  }
  return next;
}
