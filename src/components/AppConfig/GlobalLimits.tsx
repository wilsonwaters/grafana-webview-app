import React from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2 } from '@grafana/data';
import { Field, Input, useStyles2 } from '@grafana/ui';
import { DEFAULT_PLUGIN_SETTINGS } from '../../settingsTypes';
import { FieldError } from './FieldError';
import { LIMIT_SPECS, LimitKey, LimitSpec } from './settingsForm';

export function limitFieldId(key: LimitKey): string {
  return `webview-limit-${key}`;
}

function formatDefault(spec: LimitSpec): string {
  const value = DEFAULT_PLUGIN_SETTINGS[spec.key];
  if (spec.key === 'maxResponseBytes') {
    return `${value} bytes (${value / (1024 * 1024)} MiB)`;
  }
  return `${value} ${spec.unit}`;
}

export interface GlobalLimitsProps {
  values: Record<LimitKey, string>;
  /** Errors to show. Empty until the first save attempt. */
  errors: Partial<Record<LimitKey, string>>;
  onChange: (key: LimitKey, value: string) => void;
}

export function GlobalLimits({ values, errors, onChange }: GlobalLimitsProps) {
  const styles = useStyles2(getStyles);
  return (
    <div className={styles.grid}>
      {LIMIT_SPECS.map((spec) => {
        const id = limitFieldId(spec.key);
        const error = errors[spec.key];
        return (
          <Field
            key={spec.key}
            label={spec.label}
            description={`${spec.description} Default: ${formatDefault(spec)}.`}
            invalid={Boolean(error)}
            error={<FieldError id={`${id}-error`} message={error} />}
            className={styles.field}
          >
            <Input
              id={id}
              value={values[spec.key]}
              inputMode="numeric"
              placeholder={`${DEFAULT_PLUGIN_SETTINGS[spec.key]} (default)`}
              suffix={spec.unit}
              autoComplete="off"
              aria-invalid={Boolean(error)}
              aria-describedby={error ? `${id}-error` : undefined}
              onChange={(e) => onChange(spec.key, e.currentTarget.value)}
            />
          </Field>
        );
      })}
    </div>
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  grid: css({
    display: 'grid',
    gridTemplateColumns: 'repeat(auto-fill, minmax(min(100%, 340px), 1fr))',
    columnGap: theme.spacing(3),
  }),
  // Label and input rows line up across columns (see DomainEntry).
  field: css({
    display: 'grid',
    gridTemplateRows: 'subgrid',
    gridRow: 'span 2',
    minWidth: 0,
  }),
});
