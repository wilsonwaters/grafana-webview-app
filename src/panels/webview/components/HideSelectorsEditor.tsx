import React, { useState } from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2, StandardEditorProps } from '@grafana/data';
import { TextArea, useStyles2 } from '@grafana/ui';
import type { PanelOptions } from '../../../types';
import { parseHideSelectors } from '../hideSelectors';
import { hideSelectorsEditorTestIds } from './hideSelectorsEditorTestIds';

/** Settings of the editor, set in `module.tsx`. */
export interface HideSelectorsEditorSettings {
  rows?: number;
  placeholder?: string;
}

/**
 * The "Hide elements" option editor (CR5): a multi-line input with one CSS
 * selector per line, and below it a note for each selector the panel does not
 * send to the proxy, with the reason (see `parseHideSelectors`).
 *
 * Like Grafana's text option editor, it commits the value when the input loses
 * focus, so the panel does not fetch the page again on every key press. The
 * note follows the text as it is typed.
 */
export function HideSelectorsEditor({
  value,
  onChange,
  id,
  item,
}: StandardEditorProps<string, HideSelectorsEditorSettings, PanelOptions>) {
  const styles = useStyles2(getStyles);
  const saved = typeof value === 'string' ? value : '';

  // The text being edited. `base` is the saved value it started from: when the
  // saved value changes elsewhere (undo, a reload), the draft is dropped.
  const [draft, setDraft] = useState({ base: saved, text: saved });
  const text = draft.base === saved ? draft.text : saved;
  const { problems } = parseHideSelectors(text);

  return (
    <div className={styles.wrapper}>
      <TextArea
        id={id}
        value={text}
        rows={item.settings?.rows ?? 3}
        placeholder={item.settings?.placeholder}
        onChange={(e) => setDraft({ base: saved, text: e.currentTarget.value })}
        onBlur={() => {
          if (text !== saved) {
            onChange(text);
          }
        }}
        data-testid={hideSelectorsEditorTestIds.input}
      />
      {problems.length > 0 && (
        <div className={styles.problems} aria-live="polite" data-testid={hideSelectorsEditorTestIds.problems}>
          Not sent to the proxy:
          <ul className={styles.list}>
            {problems.map((problem) => (
              <li key={problem.line}>
                Line {problem.line}: <code className={styles.selector}>{problem.selector}</code> {problem.message}.
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  wrapper: css`
    display: flex;
    flex-direction: column;
    gap: ${theme.spacing(0.5)};
  `,
  problems: css`
    font-size: ${theme.typography.bodySmall.fontSize};
    color: ${theme.colors.warning.text};
  `,
  list: css`
    margin: 0;
    padding-left: ${theme.spacing(2)};
  `,
  selector: css`
    word-break: break-all;
  `,
});
