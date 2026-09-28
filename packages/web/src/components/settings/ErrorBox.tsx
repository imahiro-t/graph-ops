// The red box the settings editors show an error or warning in (DFLT-00223).
// Every editor used to spell out the same red class list by hand, so a color
// or dark-mode change had to be repeated in each copy and the copies drifted
// apart. The colors now live here, in one place.
//
// - ERROR_BOX_CLASS holds the colors, the shape and the wrapping policy
//   only. The wrapping policy is `wrap-anywhere` (overflow-wrap: anywhere,
//   DFLT-00261): an error can carry a path, URL or ID with no spaces, which
//   would otherwise run past the box on a narrow screen or at a large font.
//   Ordinary words still break at word boundaries. It is the same for every
//   box, so it lives here; callers must not add `wrap-break-word`, which
//   Tailwind v4 emits later and so would win. Padding (p-2 /
//   p-2.5), font size (text-[11px] or inherited) and layout (flex gap-2,
//   basis-full, whitespace-pre-wrap) differ from box to box, and the app does
//   not use tailwind-merge, so two utilities of the same kind in one class
//   string would be settled by CSS order rather than by the caller. Those are
//   left to each caller's `className`, never put in the constant.
// - The role is the caller's choice and is not defaulted: "alert" for an
//   error that should be announced when it appears, "note" for a warning
//   that is always on screen, and none when something else announces the
//   text (e.g. a separate live region, in which case the box also takes
//   aria-hidden="true" so the text is not read twice).
// - The dark-mode border is red-800 everywhere. It used to be red-800 in
//   LabelsEditor and red-900 elsewhere; red-800 stands further from the
//   red-950 background, so settling on it only raises the border's contrast.
//   Text and background colors are unchanged.
import type { ReactNode } from 'react';

export const ERROR_BOX_CLASS =
  'bg-red-50 dark:bg-red-950 text-red-700 dark:text-red-300 rounded-lg border border-red-200 dark:border-red-800 wrap-anywhere';

interface ErrorBoxProps {
  id?: string;
  role?: 'alert' | 'note';
  'aria-hidden'?: 'true';
  /** Padding, font size and layout for this box; added after the colors. */
  className?: string;
  children: ReactNode;
}

export function ErrorBox({ id, role, 'aria-hidden': ariaHidden, className, children }: ErrorBoxProps) {
  return (
    <div
      id={id}
      role={role}
      aria-hidden={ariaHidden}
      className={className ? `${ERROR_BOX_CLASS} ${className}` : ERROR_BOX_CLASS}
    >
      {children}
    </div>
  );
}
