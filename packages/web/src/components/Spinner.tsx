import { Loader2 } from 'lucide-react';

// DFLT-00321: every spinner in the web UI goes through this module, so none
// of them turns for a user who asked the system for reduced motion
// (prefers-reduced-motion: reduce, WCAG 2.3.3). motion-reduce:animate-none
// switches the spin off there, the same way the skeleton rows' animate-pulse
// is switched off. The plain animate-spin class stays on the element (rather
// than a motion-safe variant of it), so tests and styles that look for
// .animate-spin still find a spinner. spinnerSource.test.ts fails if
// animate-spin is written anywhere else in src/.
export const SPIN_CLASS = 'animate-spin motion-reduce:animate-none';

// A Loader2 icon that spins, hidden from assistive tech: the busy state is
// announced by the control or region it sits in (aria-busy, a label, or the
// text next to it), never by the icon. className carries the size, colour
// and layout classes (w-4 h-4 shrink-0, ...).
export function Spinner({ className }: { className?: string }) {
  return <Loader2 aria-hidden="true" className={className ? `${className} ${SPIN_CLASS}` : SPIN_CLASS} />;
}
