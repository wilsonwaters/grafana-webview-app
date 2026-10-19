import React from 'react';

/**
 * The text of a field error, with an id that the input references through
 * aria-describedby. Pass it as the `error` of a @grafana/ui Field: Field
 * renders the message box (role="alert"). Grafana 12.4 does not link the
 * message to the input itself, so this id does it on every supported version.
 */
export function FieldError({ id, message }: { id: string; message?: string }) {
  if (!message) {
    return null;
  }
  return <span id={id}>{message}</span>;
}
