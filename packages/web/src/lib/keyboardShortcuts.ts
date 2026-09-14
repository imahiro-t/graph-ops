import type { KeyboardEvent } from 'react';

// Cmd+Enter on Mac, Ctrl+Enter on Windows/Linux submits a prompt/form from
// inside a textarea or input without needing to reach for the mouse, while
// plain Enter is left alone (a textarea still inserts a newline; a
// single-line input inside a <form> still submits natively). Checking both
// modifiers covers each OS's chord without sniffing the platform -- a given
// keydown only ever sets one of metaKey (Mac Cmd)/ctrlKey (Windows/Linux
// Ctrl), never both.
export function isSubmitShortcut(e: KeyboardEvent): boolean {
  return (e.metaKey || e.ctrlKey) && e.key === 'Enter';
}
