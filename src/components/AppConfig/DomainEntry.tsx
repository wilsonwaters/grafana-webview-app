import React, { ChangeEvent } from 'react';
import { css } from '@emotion/css';
import { GrafanaTheme2 } from '@grafana/data';
import { Alert, Button, Checkbox, Field, Input, useStyles2 } from '@grafana/ui';
import { testIds } from '../testIds';
import { FieldError } from './FieldError';
import { DomainField, DomainRow, validateHostname } from './settingsForm';

export function domainFieldId(rowKey: string, field: DomainField): string {
  return `webview-${rowKey}-${field}`;
}

export interface DomainEntryProps {
  index: number;
  row: DomainRow;
  /** Errors to show. Empty until the first save attempt. */
  errors: Partial<Record<DomainField, string>>;
  /** The per-domain limit that applies when the entry has no override. */
  globalDomainLimit: string;
  onChange: (key: string, patch: Partial<DomainRow>) => void;
  onRemove: (key: string) => void;
}

export function DomainEntry({ index, row, errors, globalDomainLimit, onChange, onRemove }: DomainEntryProps) {
  const styles = useStyles2(getStyles);
  const number = index + 1;
  const titleId = `webview-${row.key}-title`;
  const domainId = domainFieldId(row.key, 'domain');
  const rateId = domainFieldId(row.key, 'rateLimitPerMin');
  const portsId = domainFieldId(row.key, 'allowedPorts');
  const trimmed = row.domain.trim();
  const host = validateHostname(row.domain);
  const isName = host.ok && !/^[\d.]+$|:/.test(host.canonical);
  const exampleSubdomain = `api.${isName ? trimmed.replace(/\.$/, '') : 'example.com'}`;

  const text = (field: DomainField) => (e: ChangeEvent<HTMLInputElement>) =>
    onChange(row.key, { [field]: e.currentTarget.value });

  return (
    <div role="group" aria-labelledby={titleId} className={styles.entry} data-testid={testIds.appConfig.domainEntry}>
      <div className={styles.header}>
        <span id={titleId} className={styles.title}>
          Entry {number}
          {trimmed !== '' && (
            <>
              {' '}
              <span className={styles.titleDomain}>{trimmed}</span>
            </>
          )}
        </span>
        <Button
          type="button"
          variant="secondary"
          fill="text"
          size="sm"
          icon="trash-alt"
          aria-label={trimmed ? `Remove entry ${number}: ${trimmed}` : `Remove entry ${number}`}
          onClick={() => onRemove(row.key)}
        >
          Remove
        </Button>
      </div>

      <div className={styles.fields}>
        <Field
          label="Domain"
          description="Host name only, without a scheme, port or path."
          invalid={Boolean(errors.domain)}
          error={<FieldError id={`${domainId}-error`} message={errors.domain} />}
          className={styles.field}
        >
          <Input
            id={domainId}
            value={row.domain}
            placeholder="status.example.com"
            autoComplete="off"
            spellCheck={false}
            aria-invalid={Boolean(errors.domain)}
            aria-describedby={errors.domain ? `${domainId}-error` : undefined}
            onChange={text('domain')}
          />
        </Field>
        <Field
          label="Rate limit override"
          description={`Requests per minute. Empty or 0 uses the global per-domain limit (${globalDomainLimit}).`}
          invalid={Boolean(errors.rateLimitPerMin)}
          error={<FieldError id={`${rateId}-error`} message={errors.rateLimitPerMin} />}
          className={styles.field}
        >
          <Input
            id={rateId}
            value={row.rateLimitPerMin}
            inputMode="numeric"
            placeholder={`Global limit (${globalDomainLimit})`}
            autoComplete="off"
            aria-invalid={Boolean(errors.rateLimitPerMin)}
            aria-describedby={errors.rateLimitPerMin ? `${rateId}-error` : undefined}
            onChange={text('rateLimitPerMin')}
          />
        </Field>
        <Field
          label="Additional ports"
          description="Ports 80 and 443 are always allowed. Separate ports with commas."
          invalid={Boolean(errors.allowedPorts)}
          error={<FieldError id={`${portsId}-error`} message={errors.allowedPorts} />}
          className={styles.field}
        >
          <Input
            id={portsId}
            value={row.allowedPorts}
            placeholder="None"
            autoComplete="off"
            aria-invalid={Boolean(errors.allowedPorts)}
            aria-describedby={errors.allowedPorts ? `${portsId}-error` : undefined}
            onChange={text('allowedPorts')}
          />
        </Field>
      </div>

      <div className={styles.options}>
        <Checkbox
          label="Allow subdomains"
          description={`Also allow names under this domain, for example ${exampleSubdomain}.`}
          value={row.allowSubdomains}
          onChange={(e) => onChange(row.key, { allowSubdomains: e.currentTarget.checked })}
        />
        <Checkbox
          label="Allow private IP addresses"
          description="Relaxes SSRF protection for this domain. Use only for internal hosts that you trust."
          value={row.allowPrivateIP}
          onChange={(e) => onChange(row.key, { allowPrivateIP: e.currentTarget.checked })}
        />
      </div>

      {row.allowPrivateIP && (
        <Alert
          severity="warning"
          title="Private IP addresses are allowed for this domain"
          className={styles.warning}
          data-testid={testIds.appConfig.privateIpWarning}
        >
          The proxy can connect to this domain when it resolves to an RFC 1918 private address (10.0.0.0/8,
          172.16.0.0/12 or 192.168.0.0/16). This relaxes the SSRF protection for this domain only. Loopback, link-local
          and cloud metadata addresses stay blocked. Each permitted connection is written to the audit log.
        </Alert>
      )}
    </div>
  );
}

const getStyles = (theme: GrafanaTheme2) => ({
  entry: css({
    border: `1px solid ${theme.colors.border.weak}`,
    borderRadius: theme.shape.radius.default,
    background: theme.colors.background.secondary,
    padding: theme.spacing(1.5, 2, 2),
    marginBottom: theme.spacing(2),
    containerType: 'inline-size',
  }),
  header: css({
    display: 'flex',
    alignItems: 'center',
    justifyContent: 'space-between',
    gap: theme.spacing(1),
    marginBottom: theme.spacing(1),
  }),
  title: css({
    fontWeight: theme.typography.fontWeightMedium,
    minWidth: 0,
    overflow: 'hidden',
    textOverflow: 'ellipsis',
    whiteSpace: 'nowrap',
  }),
  titleDomain: css({
    color: theme.colors.text.secondary,
    fontWeight: theme.typography.fontWeightRegular,
  }),
  // Each Field spans two rows of a subgrid (label, then input), so the inputs line up
  // even when the descriptions wrap to a different number of lines.
  fields: css({
    display: 'grid',
    gridTemplateColumns: 'minmax(0, 2fr) minmax(0, 1fr) minmax(0, 1fr)',
    columnGap: theme.spacing(2),
    '@container (max-width: 640px)': {
      gridTemplateColumns: 'minmax(0, 1fr)',
    },
  }),
  field: css({
    display: 'grid',
    gridTemplateRows: 'subgrid',
    gridRow: 'span 2',
    minWidth: 0,
  }),
  options: css({
    display: 'flex',
    flexWrap: 'wrap',
    gap: theme.spacing(1, 4),
  }),
  warning: css({
    marginTop: theme.spacing(2),
    marginBottom: 0,
  }),
});
