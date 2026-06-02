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
  test('renders the empty state when no URL is configured', () => {
    render(<WebViewPanel {...buildProps({ url: '' })} />);

    expect(screen.getByTestId(webViewPanelTestIds.container)).toBeInTheDocument();
    expect(screen.getByTestId(webViewPanelTestIds.placeholder)).toBeInTheDocument();
    // No iframe with an empty src should be rendered in the empty state.
    expect(screen.queryByTestId(webViewPanelTestIds.iframe)).not.toBeInTheDocument();
  });

  test('renders an iframe with the configured URL as src in direct mode', () => {
    render(<WebViewPanel {...buildProps({ url: 'https://example.com' })} />);

    const iframe = screen.getByTestId(webViewPanelTestIds.iframe);
    expect(iframe).toHaveAttribute('src', 'https://example.com');
    expect(screen.queryByTestId(webViewPanelTestIds.placeholder)).not.toBeInTheDocument();
  });

  test('iframe uses the exact sandbox attribute and is non-interactive', () => {
    render(<WebViewPanel {...buildProps({ url: 'https://example.com' })} />);

    const iframe = screen.getByTestId(webViewPanelTestIds.iframe);
    expect(iframe).toHaveAttribute('sandbox', 'allow-scripts allow-same-origin');
    expect(iframe).toHaveStyle({ pointerEvents: 'none' });
  });

  test('iframe is sized to the virtual dimensions and clipped by the container', () => {
    render(<WebViewPanel {...buildProps({ url: 'https://example.com' })} />);

    const iframe = screen.getByTestId(webViewPanelTestIds.iframe);
    expect(iframe).toHaveStyle({ width: '1920px', height: '1080px', transformOrigin: 'top left' });
    expect(screen.getByTestId(webViewPanelTestIds.container)).toHaveStyle({ overflow: 'hidden' });
  });

  test('applies the saved viewport via CSS transform', () => {
    render(
      <WebViewPanel
        {...buildProps({ url: 'https://example.com', viewportX: 100, viewportY: 200, viewportZoom: 1.5 })}
      />
    );

    const iframe = screen.getByTestId(webViewPanelTestIds.iframe);
    expect(iframe).toHaveStyle({ transform: 'scale(1.5) translate(-100px, -200px)' });
  });

  test('renders without crashing for partial / legacy options', () => {
    const props = { options: { url: 'https://x.test' }, width: 100, height: 100 } as unknown as PanelProps<PanelOptions>;
    render(<WebViewPanel {...props} />);

    expect(screen.getByTestId(webViewPanelTestIds.container)).toBeInTheDocument();
    expect(screen.getByTestId(webViewPanelTestIds.iframe)).toHaveAttribute('src', 'https://x.test');
  });
});
