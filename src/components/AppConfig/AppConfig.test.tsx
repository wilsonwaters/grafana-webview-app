import React from 'react';
import { of, throwError } from 'rxjs';
import { act, fireEvent, render, screen, within } from '@testing-library/react';
import { PluginType } from '@grafana/data';
import { getBackendSrv } from '@grafana/runtime';
import AppConfig, { AppConfigProps } from './AppConfig';
import { reloadPage } from './reloadPage';
import { testIds } from 'components/testIds';

jest.mock('@grafana/runtime', () => ({
  getBackendSrv: jest.fn(),
}));

jest.mock('./reloadPage', () => ({
  reloadPage: jest.fn(),
}));

const mockedGetBackendSrv = getBackendSrv as jest.MockedFunction<typeof getBackendSrv>;
const mockedReload = reloadPage as jest.MockedFunction<typeof reloadPage>;
let fetchMock: jest.Mock;

const PROVISIONED_JSON_DATA = {
  apiUrl: 'http://default-url.com',
  customKey: 'keep me',
  requestTimeoutSec: 20,
  allowedDomains: [
    { domain: 'example.com', options: { allowSubdomains: false } },
    { domain: 'intranet.local', options: { allowSubdomains: true, allowedPorts: [8443], rateLimitPerMin: 5 } },
  ],
};

function renderConfig(jsonData: Record<string, unknown> = PROVISIONED_JSON_DATA) {
  const props = {
    plugin: {
      meta: {
        id: 'wilsonwaters-webview-app',
        name: 'Webview',
        type: PluginType.app,
        enabled: true,
        pinned: true,
        jsonData,
        secureJsonFields: { apiKey: true },
      },
    },
    query: {},
  } as unknown as AppConfigProps;
  return render(<AppConfig {...props} />);
}

const entries = () => screen.queryAllByTestId(testIds.appConfig.domainEntry);
const entry = (n: number) => entries()[n - 1];
const domainInput = (n: number) => within(entry(n)).getByRole('textbox', { name: /^domain/i });

async function save() {
  await act(async () => {
    fireEvent.click(screen.getByRole('button', { name: 'Save settings' }));
  });
}

function savedPayload() {
  expect(fetchMock).toHaveBeenCalledTimes(1);
  return fetchMock.mock.calls[0][0];
}

describe('Components/AppConfig', () => {
  beforeEach(() => {
    jest.resetAllMocks();
    fetchMock = jest.fn().mockReturnValue(of({ status: 200, data: {} }));
    mockedGetBackendSrv.mockReturnValue({ fetch: fetchMock } as unknown as ReturnType<typeof getBackendSrv>);
  });

  test('renders the existing allowlist and limits, and no scaffold API fields', () => {
    renderConfig();

    expect(screen.getByRole('group', { name: 'Domain allowlist' })).toBeInTheDocument();
    expect(screen.getByRole('group', { name: 'Global limits' })).toBeInTheDocument();
    expect(entries()).toHaveLength(2);
    expect(domainInput(1)).toHaveValue('example.com');
    expect(domainInput(2)).toHaveValue('intranet.local');
    expect(within(entry(2)).getByRole('checkbox', { name: /allow subdomains/i })).toBeChecked();
    expect(within(entry(2)).getByRole('textbox', { name: /additional ports/i })).toHaveValue('8443');
    expect(within(entry(2)).getByRole('textbox', { name: /rate limit override/i })).toHaveValue('5');
    expect(screen.getByRole('textbox', { name: /request timeout/i })).toHaveValue('20');
    expect(screen.getByRole('textbox', { name: /maximum redirects/i })).toHaveValue('');
    expect(screen.getByRole('textbox', { name: /maximum redirects/i })).toHaveAttribute('placeholder', '3 (default)');
    expect(screen.getByText(/default: 5242880 bytes \(5 mib\)/i)).toBeInTheDocument();

    expect(screen.queryByText(/api key/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/api url/i)).not.toBeInTheDocument();
    expect(screen.queryByTestId(testIds.appConfig.emptyAllowlist)).not.toBeInTheDocument();
  });

  test('says that an empty allowlist denies every request', () => {
    renderConfig({});
    expect(entries()).toHaveLength(0);
    const message = screen.getByTestId(testIds.appConfig.emptyAllowlist);
    expect(message).toHaveTextContent(/allowlist is empty/i);
    expect(message).toHaveTextContent(/denies every request/i);
  });

  test('adds a domain, focuses it and saves it', async () => {
    renderConfig();
    fireEvent.click(screen.getByRole('button', { name: /add domain/i }));

    expect(entries()).toHaveLength(3);
    expect(domainInput(3)).toHaveFocus();
    expect(screen.getByRole('group', { name: /entry 3/i })).toBe(entry(3));

    fireEvent.change(domainInput(3), { target: { value: 'example.net' } });
    fireEvent.click(within(entry(3)).getByRole('checkbox', { name: /allow subdomains/i }));
    fireEvent.change(within(entry(3)).getByRole('textbox', { name: /additional ports/i }), {
      target: { value: '8080, 8443' },
    });
    await save();

    const { jsonData } = savedPayload().data;
    expect(jsonData.allowedDomains).toHaveLength(3);
    expect(jsonData.allowedDomains[2]).toEqual({
      domain: 'example.net',
      options: { allowSubdomains: true, allowPrivateIP: false, rateLimitPerMin: 0, allowedPorts: [8080, 8443] },
    });
    expect(mockedReload).toHaveBeenCalledTimes(1);
  });

  test('edits an existing domain', async () => {
    renderConfig();
    fireEvent.change(domainInput(1), { target: { value: 'www.example.com' } });
    await save();
    expect(savedPayload().data.jsonData.allowedDomains[0].domain).toBe('www.example.com');
  });

  test('removes a domain and shows the empty-allowlist message when none are left', async () => {
    renderConfig();
    fireEvent.click(screen.getByRole('button', { name: 'Remove entry 1: example.com' }));
    expect(entries()).toHaveLength(1);
    expect(domainInput(1)).toHaveValue('intranet.local');
    expect(domainInput(1)).toHaveFocus();

    fireEvent.click(screen.getByRole('button', { name: /remove entry 1/i }));
    expect(entries()).toHaveLength(0);
    expect(screen.getByTestId(testIds.appConfig.emptyAllowlist)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: /add domain/i })).toHaveFocus();

    await save();
    expect(savedPayload().data.jsonData.allowedDomains).toEqual([]);
  });

  test('shows field errors after a save attempt, linked to the input, and updates them as the admin types', async () => {
    renderConfig();
    const input = domainInput(1);
    fireEvent.change(input, { target: { value: 'https://example.com/page' } });
    fireEvent.blur(input);
    // No error before the first save attempt: a message that appears on blur would move the Save button.
    expect(screen.queryByText(/remove the scheme/i)).not.toBeInTheDocument();
    expect(input).toHaveAttribute('aria-invalid', 'false');

    await save();
    const message = screen.getByText(/remove the scheme/i);
    expect(message.closest('[role="alert"]')).not.toBeNull();
    expect(input).toHaveAttribute('aria-invalid', 'true');
    expect(input).toHaveAccessibleDescription(expect.stringMatching(/remove the scheme/i));

    fireEvent.change(input, { target: { value: 'example.com/page' } });
    expect(screen.getByText(/remove the path/i)).toBeInTheDocument();
    fireEvent.change(input, { target: { value: 'example.com' } });
    expect(screen.queryByText(/remove the path/i)).not.toBeInTheDocument();
    expect(input).toHaveAttribute('aria-invalid', 'false');
    expect(screen.queryByRole('alert', { name: 'The settings were not saved' })).not.toBeInTheDocument();
  });

  test('validation errors block saving and move focus to the first invalid field', async () => {
    renderConfig();
    fireEvent.click(screen.getByRole('button', { name: /add domain/i }));
    fireEvent.change(domainInput(3), { target: { value: '*.example.org' } });
    fireEvent.change(screen.getByRole('textbox', { name: /request timeout/i }), { target: { value: '-1' } });

    await save();

    expect(fetchMock).not.toHaveBeenCalled();
    expect(mockedReload).not.toHaveBeenCalled();
    expect(screen.getByText(/wildcards are not supported/i)).toBeInTheDocument();
    expect(screen.getByText(/negative values are not allowed/i)).toBeInTheDocument();
    expect(screen.getByRole('alert', { name: 'The settings were not saved' })).toHaveTextContent(
      /2 fields have errors/
    );
    expect(domainInput(3)).toHaveFocus();

    // Correcting the fields allows the save.
    fireEvent.change(domainInput(3), { target: { value: 'example.org' } });
    fireEvent.change(screen.getByRole('textbox', { name: /request timeout/i }), { target: { value: '' } });
    await save();
    expect(savedPayload().data.jsonData).not.toHaveProperty('requestTimeoutSec');
  });

  test('a new entry with an empty domain blocks saving', async () => {
    renderConfig({});
    fireEvent.click(screen.getByRole('button', { name: /add domain/i }));
    await save();
    expect(fetchMock).not.toHaveBeenCalled();
    expect(screen.getByText(/enter a domain name/i)).toBeInTheDocument();
  });

  test('the save payload keeps unknown jsonData keys, drops apiUrl and sends no secrets', async () => {
    renderConfig();
    await save();

    const request = savedPayload();
    expect(request.url).toBe('/api/plugins/wilsonwaters-webview-app/settings');
    expect(request.method).toBe('POST');
    expect(request.data.enabled).toBe(true);
    expect(request.data.pinned).toBe(true);
    expect(request.data).not.toHaveProperty('secureJsonData');
    expect(request.data.jsonData).toEqual({
      customKey: 'keep me',
      requestTimeoutSec: 20,
      allowedDomains: [
        {
          domain: 'example.com',
          options: { allowSubdomains: false, allowPrivateIP: false, rateLimitPerMin: 0, allowedPorts: [] },
        },
        {
          domain: 'intranet.local',
          options: { allowSubdomains: true, allowPrivateIP: false, rateLimitPerMin: 5, allowedPorts: [8443] },
        },
      ],
    });
    expect(JSON.stringify(request.data)).not.toMatch(/apiKey|apiUrl/);
  });

  test('shows the private-IP warning only for entries that allow private IP addresses', () => {
    renderConfig();
    expect(screen.queryByTestId(testIds.appConfig.privateIpWarning)).not.toBeInTheDocument();

    fireEvent.click(within(entry(2)).getByRole('checkbox', { name: /allow private ip addresses/i }));

    const warning = screen.getByTestId(testIds.appConfig.privateIpWarning);
    expect(entry(2)).toContainElement(warning);
    expect(warning).toHaveTextContent(/RFC 1918/);
    expect(warning).toHaveTextContent(/this domain only/i);
    expect(within(entry(1)).queryByTestId(testIds.appConfig.privateIpWarning)).not.toBeInTheDocument();
  });

  test('shows an error and does not reload when the save request fails', async () => {
    fetchMock.mockReturnValue(throwError(() => ({ status: 403, data: { message: 'Permission denied' } })));
    renderConfig();
    await save();

    expect(screen.getByRole('alert', { name: 'The settings could not be saved' })).toHaveTextContent(
      'Permission denied'
    );
    expect(mockedReload).not.toHaveBeenCalled();
    expect(screen.getByRole('button', { name: 'Save settings' })).toBeEnabled();
  });
});
