import React from 'react';
import { render, screen } from '@testing-library/react';
import { PanelProps } from '@grafana/data';
import { WebViewPanel } from './WebViewPanel';
import { DEFAULT_PANEL_OPTIONS, type PanelOptions } from '../../../types';
import { webViewPanelTestIds } from './testIds';

function buildProps(options: Partial<PanelOptions> = {}): PanelProps<PanelOptions> {
  return {
    options: { ...DEFAULT_PANEL_OPTIONS, ...options },
    width: 400,
    height: 300,
  } as unknown as PanelProps<PanelOptions>;
}

describe('panels/webview/WebViewPanel', () => {
  test('renders the not-implemented placeholder when no URL is configured', () => {
    render(<WebViewPanel {...buildProps({ url: '' })} />);

    expect(screen.getByTestId(webViewPanelTestIds.container)).toBeInTheDocument();
    expect(screen.getByTestId(webViewPanelTestIds.placeholder)).toBeInTheDocument();
    expect(screen.queryByTestId(webViewPanelTestIds.url)).not.toBeInTheDocument();
  });

  test('echoes the configured URL', () => {
    render(<WebViewPanel {...buildProps({ url: 'https://example.com' })} />);

    expect(screen.getByTestId(webViewPanelTestIds.url)).toHaveTextContent('https://example.com');
    expect(screen.queryByTestId(webViewPanelTestIds.placeholder)).not.toBeInTheDocument();
  });

  test('renders without crashing for partial / legacy options', () => {
    // Simulate a saved dashboard with only some fields present.
    const props = { options: { url: 'https://x.test' }, width: 100, height: 100 } as unknown as PanelProps<PanelOptions>;
    render(<WebViewPanel {...props} />);

    expect(screen.getByTestId(webViewPanelTestIds.container)).toBeInTheDocument();
  });
});
