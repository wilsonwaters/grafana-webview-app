import React, { SyntheticEvent, useEffect, useMemo, useRef, useState } from 'react';
import { lastValueFrom } from 'rxjs';
import { css } from '@emotion/css';
import { AppPluginMeta, GrafanaTheme2, PluginConfigPageProps } from '@grafana/data';
import { getBackendSrv } from '@grafana/runtime';
import { Alert, Button, FieldSet, useStyles2 } from '@grafana/ui';
import { DEFAULT_PLUGIN_SETTINGS } from '../../settingsTypes';
import { testIds } from '../testIds';
import { DomainEntry, domainFieldId } from './DomainEntry';
import { GlobalLimits, limitFieldId } from './GlobalLimits';
import { reloadPage } from './reloadPage';
import {
  buildJsonData,
  DomainField,
  DomainRow,
  emptyDomainRow,
  hasErrors,
  JsonData,
  LIMIT_KEYS,
  LimitKey,
  SettingsFormErrors,
  SettingsFormState,
  toFormState,
  validateForm,
} from './settingsForm';

export interface AppConfigProps extends PluginConfigPageProps<AppPluginMeta<JsonData>> {}

const DOMAIN_FIELDS: DomainField[] = ['domain', 'rateLimitPerMin', 'allowedPorts'];

/** Element ids of all invalid fields, in page order. */
function invalidFieldIds(state: SettingsFormState, errors: SettingsFormErrors): string[] {
  const ids: string[] = [];
  for (const row of state.domains) {
    for (const field of DOMAIN_FIELDS) {
      if (errors.domains[row.key]?.[field]) {
        ids.push(domainFieldId(row.key, field));
      }
    }
  }
  for (const key of LIMIT_KEYS) {
    if (errors.limits[key]) {
      ids.push(limitFieldId(key));
    }
  }
  return ids;
}

function errorMessage(err: unknown): string {
  const e = err as { data?: { message?: string }; message?: string; statusText?: string };
  return e?.data?.message || e?.message || e?.statusText || 'Unknown error.';
}

const AppConfig = ({ plugin }: AppConfigProps) => {
  const styles = useStyles2(getStyles);
  const { id: pluginId, enabled, pinned, jsonData } = plugin.meta;

  const [form, setForm] = useState<SettingsFormState>(() => toFormState(jsonData));
  // Errors show after the first save attempt and then update as the admin types. They do not
  // show on blur: a message that appears on blur moves the page under the pointer, and the
  // click on Save (or Add domain) that caused the blur is lost.
  const [submitAttempted, setSubmitAttempted] = useState(false);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<string | undefined>();
  const addButtonRef = useRef<HTMLButtonElement>(null);
  // Id of an element to focus after the next render (a row that was just added).
  const pendingFocus = useRef<string | undefined>(undefined);

  const errors = useMemo(() => validateForm(form), [form]);
  const invalidIds = useMemo(() => invalidFieldIds(form, errors), [form, errors]);

  useEffect(() => {
    if (pendingFocus.current) {
      document.getElementById(pendingFocus.current)?.focus();
      pendingFocus.current = undefined;
    }
  });

  const updateRow = (key: string, patch: Partial<DomainRow>) =>
    setForm((prev) => ({
      ...prev,
      domains: prev.domains.map((row) => (row.key === key ? { ...row, ...patch } : row)),
    }));

  const addRow = () => {
    const row = emptyDomainRow();
    setForm((prev) => ({ ...prev, domains: [...prev.domains, row] }));
    pendingFocus.current = domainFieldId(row.key, 'domain');
  };

  const removeRow = (key: string) => {
    const index = form.domains.findIndex((row) => row.key === key);
    const next = form.domains[index + 1] ?? form.domains[index - 1];
    setForm((prev) => ({ ...prev, domains: prev.domains.filter((row) => row.key !== key) }));
    // Keep keyboard focus on the page: move it to a neighbouring entry, or to "Add domain".
    const target = next ? document.getElementById(domainFieldId(next.key, 'domain')) : addButtonRef.current;
    target?.focus();
  };

  const updateLimit = (key: LimitKey, value: string) =>
    setForm((prev) => ({ ...prev, limits: { ...prev.limits, [key]: value } }));

  const onSubmit = async (event: SyntheticEvent<HTMLFormElement>) => {
    event.preventDefault();
    setSubmitAttempted(true);
    setSaveError(undefined);
    if (hasErrors(errors)) {
      document.getElementById(invalidIds[0])?.focus();
      return;
    }

    setSaving(true);
    try {
      await lastValueFrom(
        getBackendSrv().fetch({
          url: `/api/plugins/${pluginId}/settings`,
          method: 'POST',
          // secureJsonData is left out on purpose: Grafana then keeps the stored secrets unchanged.
          data: { enabled, pinned, jsonData: buildJsonData(jsonData, form) },
          showErrorAlert: false,
        })
      );
      // Grafana has no supported way to refresh plugin meta in place.
      reloadPage();
    } catch (err) {
      setSaveError(errorMessage(err));
      setSaving(false);
    }
  };

  const typedDomainLimit = form.limits.rateLimitPerDomainPerMin.trim();
  const globalDomainLimit =
    typedDomainLimit && !errors.limits.rateLimitPerDomainPerMin
      ? typedDomainLimit
      : String(DEFAULT_PLUGIN_SETTINGS.rateLimitPerDomainPerMin);
  const showSummary = submitAttempted && invalidIds.length > 0;

  return (
    <form onSubmit={onSubmit} noValidate className={styles.form} data-testid={testIds.appConfig.form}>
      <FieldSet label="Domain allowlist">
        <p className={styles.intro}>
          The backend proxy and the Test URL check connect only to the domains in this list. Direct (iframe) mode does
          not use the allowlist.
        </p>

        {form.domains.length === 0 && (
          <Alert severity="info" title="The allowlist is empty" data-testid={testIds.appConfig.emptyAllowlist}>
            The proxy denies every request until you add a domain. An empty allowlist is the safe default for a new
            installation.
          </Alert>
        )}

        {form.domains.map((row, index) => (
          <DomainEntry
            key={row.key}
            index={index}
            row={row}
            errors={submitAttempted ? (errors.domains[row.key] ?? {}) : {}}
            globalDomainLimit={globalDomainLimit}
            onChange={updateRow}
            onRemove={removeRow}
          />
        ))}

        <Button
          ref={addButtonRef}
          type="button"
          variant="secondary"
          icon="plus"
          onClick={addRow}
          data-testid={testIds.appConfig.addDomain}
        >
          Add domain
        </Button>
      </FieldSet>

      <FieldSet label="Global limits">
        <p className={styles.intro}>
          These limits protect Grafana and the sites that it connects to. Leave a field empty to use the default.
        </p>
        <GlobalLimits values={form.limits} errors={submitAttempted ? errors.limits : {}} onChange={updateLimit} />
      </FieldSet>

      {showSummary && (
        <Alert severity="error" title="The settings were not saved">
          {invalidIds.length === 1 ? 'One field has an error.' : `${invalidIds.length} fields have errors.`} Correct the
          fields marked in red, then save again.
        </Alert>
      )}
      {saveError && (
        <Alert severity="error" title="The settings could not be saved">
          {saveError}
        </Alert>
      )}

      <div className={styles.actions}>
        <Button
          type="submit"
          disabled={saving}
          icon={saving ? 'spinner' : undefined}
          data-testid={testIds.appConfig.submit}
        >
          {saving ? 'Saving settings' : 'Save settings'}
        </Button>
        <span className={styles.hint}>
          The backend uses the saved settings for the next request. You do not need to restart Grafana.
        </span>
      </div>
    </form>
  );
};

export default AppConfig;

const getStyles = (theme: GrafanaTheme2) => ({
  form: css({
    maxWidth: theme.breakpoints.values.xl,
  }),
  intro: css({
    color: theme.colors.text.secondary,
    marginBottom: theme.spacing(2),
  }),
  actions: css({
    display: 'flex',
    flexWrap: 'wrap',
    alignItems: 'center',
    gap: theme.spacing(2),
    marginTop: theme.spacing(1),
  }),
  hint: css({
    color: theme.colors.text.secondary,
    fontSize: theme.typography.bodySmall.fontSize,
  }),
});
