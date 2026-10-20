/**
 * Reloads the page so that Grafana reads the saved plugin settings again.
 * Grafana has no supported API to refresh plugin meta in place. A separate
 * module lets tests replace the reload.
 */
export function reloadPage(): void {
  window.location.reload();
}
