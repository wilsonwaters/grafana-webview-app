import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { StandardEditorProps } from '@grafana/data';
import { HideSelectorsEditor, type HideSelectorsEditorSettings } from './HideSelectorsEditor';
import { DEFAULT_PANEL_OPTIONS, type PanelOptions } from '../../../types';
import { hideSelectorsEditorTestIds } from './hideSelectorsEditorTestIds';

type Props = StandardEditorProps<string, HideSelectorsEditorSettings, PanelOptions>;

function props(value: string, onChange: jest.Mock = jest.fn()): Props {
  return {
    value,
    onChange,
    id: 'hide-selectors-input',
    context: { data: [], options: { ...DEFAULT_PANEL_OPTIONS, hideSelectors: value } },
    item: { id: 'hideSelectors', name: 'Hide elements (proxy mode)', settings: { rows: 3, placeholder: '.ad' } },
  } as unknown as Props;
}

function input(): HTMLTextAreaElement {
  return screen.getByTestId(hideSelectorsEditorTestIds.input) as HTMLTextAreaElement;
}

describe('panels/webview/HideSelectorsEditor', () => {
  test('shows the saved selectors and no note when every selector is sent', () => {
    render(<HideSelectorsEditor {...props('.ad\n#banner')} />);

    expect(input()).toHaveValue('.ad\n#banner');
    expect(input()).toHaveAttribute('id', 'hide-selectors-input');
    expect(input()).toHaveAttribute('placeholder', '.ad');
    expect(screen.queryByTestId(hideSelectorsEditorTestIds.problems)).not.toBeInTheDocument();
  });

  test('names each refused selector with its line and the reason', () => {
    render(<HideSelectorsEditor {...props('.ad\ndiv:has(p)\n\np:matches(x)')} />);

    const problems = screen.getByTestId(hideSelectorsEditorTestIds.problems);
    expect(problems).toHaveTextContent('Not sent to the proxy:');
    expect(problems).toHaveTextContent('Line 2: div:has(p) uses :has(), which the proxy does not support.');
    expect(problems).toHaveTextContent('Line 4: p:matches(x) uses :matches(), which the proxy does not support.');
    expect(problems).not.toHaveTextContent('.ad');
  });

  test('names the selectors over the limit of 20', () => {
    const lines = Array.from({ length: 21 }, (_, i) => `.s${i + 1}`).join('\n');
    render(<HideSelectorsEditor {...props(lines)} />);

    expect(screen.getByTestId(hideSelectorsEditorTestIds.problems)).toHaveTextContent(
      'Line 21: .s21 is over the limit of 20 selectors and is not used.'
    );
  });

  test('checks the text while it is typed, and commits it only when the input loses focus', () => {
    const onChange = jest.fn();
    render(<HideSelectorsEditor {...props('.ad', onChange)} />);

    fireEvent.change(input(), { target: { value: '.ad\np:contains(Sponsored)' } });
    expect(onChange).not.toHaveBeenCalled();
    expect(screen.getByTestId(hideSelectorsEditorTestIds.problems)).toHaveTextContent(
      'Line 2: p:contains(Sponsored) uses :contains(), which the proxy does not support.'
    );

    fireEvent.blur(input());
    expect(onChange).toHaveBeenCalledTimes(1);
    expect(onChange).toHaveBeenCalledWith('.ad\np:contains(Sponsored)');
  });

  test('does not commit when the text is unchanged', () => {
    const onChange = jest.fn();
    render(<HideSelectorsEditor {...props('.ad', onChange)} />);

    fireEvent.blur(input());
    expect(onChange).not.toHaveBeenCalled();
  });

  test('a new saved value replaces the text being edited', () => {
    const { rerender } = render(<HideSelectorsEditor {...props('.ad')} />);
    fireEvent.change(input(), { target: { value: '.draft' } });

    rerender(<HideSelectorsEditor {...props('#other')} />);
    expect(input()).toHaveValue('#other');
  });

  test('an undefined value is an empty input', () => {
    render(<HideSelectorsEditor {...props(undefined as unknown as string)} />);

    expect(input()).toHaveValue('');
    expect(screen.queryByTestId(hideSelectorsEditorTestIds.problems)).not.toBeInTheDocument();
  });

  test('selector text is shown as text, never as markup', () => {
    const hostile = '<img src=x onerror=alert(1)>:has(p)';
    const { container } = render(<HideSelectorsEditor {...props(hostile)} />);

    expect(screen.getByTestId(hideSelectorsEditorTestIds.problems)).toHaveTextContent(hostile);
    expect(container.querySelector('img')).toBeNull();
  });
});
