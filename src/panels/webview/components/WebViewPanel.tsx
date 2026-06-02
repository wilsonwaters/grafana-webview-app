import React from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2, PanelProps } from '@grafana/data';
import { useStyles2 } from '@grafana/ui';
import { normalizeOptions, type PanelOptions } from '../../../types';
import { webViewPanelTestIds } from './testIds';

type Props = PanelProps<PanelOptions>;

/**
 * Placeholder Web View panel component.
 *
 * Renders a minimal placeholder that confirms the nested panel is wired to the
 * canonical F2 `PanelOptions` schema. When a URL is configured it is echoed
 * back; otherwise an "not yet implemented" hint is shown. No iframe, viewport
 * or proxy logic is present yet — those arrive in the panel-core stream.
 */
export function WebViewPanel({ options, width, height }: Props) {
  const styles = useStyles2(getStyles);
  // Normalise so the component is robust against partial/legacy saved options.
  const opts = normalizeOptions(options);

  return (
    <div className={styles.container} style={{ width, height }} data-testid={webViewPanelTestIds.container}>
      <div className={styles.title}>Web View panel</div>
      {opts.url ? (
        <div className={styles.url} data-testid={webViewPanelTestIds.url}>
          {opts.url}
        </div>
      ) : (
        <div className={styles.placeholder} data-testid={webViewPanelTestIds.placeholder}>
          Web View panel — not yet implemented. Set a URL in the panel options.
        </div>
      )}
    </div>
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  container: css`
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: ${theme.spacing(1)};
    overflow: hidden;
    padding: ${theme.spacing(2)};
    text-align: center;
  `,
  title: css`
    font-weight: ${theme.typography.fontWeightMedium};
    color: ${theme.colors.text.secondary};
  `,
  url: css`
    font-family: ${theme.typography.fontFamilyMonospace};
    word-break: break-all;
    color: ${theme.colors.text.primary};
  `,
  placeholder: css`
    color: ${theme.colors.text.secondary};
  `,
});
