// Single source of truth for how a label color key looks (DFLT-00084) and for
// the toolbar's label filter predicate. Read by LabelChip (chips on the
// ticket row/detail and the settings preview), LabelsEditor (the palette
// buttons) and App.tsx (matchesLabelFilter).
//
// Every class name below is written out literally, once per color: Tailwind
// only generates classes it can find verbatim in the source, so building
// them by string concatenation (`bg-${color}-100`) would silently vanish
// from the production build.
//
// The chip uses the same lightness band as statusMeta.ts's chips (100 / 800
// in light, 900-ish / 300 in dark) so the text stays readable in both themes.
import { LabelColor, LABEL_COLORS } from './types';

export interface LabelColorMeta {
  // i18n key under `labels.colors.*`, used for palette button aria-labels.
  nameKey: string;
  // Chip colors, each including its dark-theme variant.
  chip: { bg: string; text: string; border: string };
  // Solid swatch for the settings palette buttons.
  swatch: string;
}

const LABEL_COLOR_META: Record<LabelColor, LabelColorMeta> = {
  gray: {
    nameKey: 'labels.colors.gray',
    chip: { bg: 'bg-gray-100 dark:bg-gray-800', text: 'text-gray-800 dark:text-gray-200', border: 'border-gray-200 dark:border-gray-600' },
    swatch: 'bg-gray-500'
  },
  red: {
    nameKey: 'labels.colors.red',
    chip: { bg: 'bg-red-100 dark:bg-red-900/40', text: 'text-red-800 dark:text-red-300', border: 'border-red-200 dark:border-red-800' },
    swatch: 'bg-red-500'
  },
  orange: {
    nameKey: 'labels.colors.orange',
    chip: { bg: 'bg-orange-100 dark:bg-orange-900/40', text: 'text-orange-800 dark:text-orange-300', border: 'border-orange-200 dark:border-orange-800' },
    swatch: 'bg-orange-500'
  },
  amber: {
    nameKey: 'labels.colors.amber',
    chip: { bg: 'bg-amber-100 dark:bg-amber-900/40', text: 'text-amber-800 dark:text-amber-300', border: 'border-amber-200 dark:border-amber-800' },
    swatch: 'bg-amber-500'
  },
  green: {
    nameKey: 'labels.colors.green',
    chip: { bg: 'bg-green-100 dark:bg-green-900/40', text: 'text-green-800 dark:text-green-300', border: 'border-green-200 dark:border-green-800' },
    swatch: 'bg-green-500'
  },
  teal: {
    nameKey: 'labels.colors.teal',
    chip: { bg: 'bg-teal-100 dark:bg-teal-900/40', text: 'text-teal-800 dark:text-teal-300', border: 'border-teal-200 dark:border-teal-800' },
    swatch: 'bg-teal-500'
  },
  blue: {
    nameKey: 'labels.colors.blue',
    chip: { bg: 'bg-blue-100 dark:bg-blue-900/40', text: 'text-blue-800 dark:text-blue-300', border: 'border-blue-200 dark:border-blue-800' },
    swatch: 'bg-blue-500'
  },
  indigo: {
    nameKey: 'labels.colors.indigo',
    chip: { bg: 'bg-indigo-100 dark:bg-indigo-900/40', text: 'text-indigo-800 dark:text-indigo-300', border: 'border-indigo-200 dark:border-indigo-800' },
    swatch: 'bg-indigo-500'
  },
  purple: {
    nameKey: 'labels.colors.purple',
    chip: { bg: 'bg-purple-100 dark:bg-purple-900/40', text: 'text-purple-800 dark:text-purple-300', border: 'border-purple-200 dark:border-purple-800' },
    swatch: 'bg-purple-500'
  },
  pink: {
    nameKey: 'labels.colors.pink',
    chip: { bg: 'bg-pink-100 dark:bg-pink-900/40', text: 'text-pink-800 dark:text-pink-300', border: 'border-pink-200 dark:border-pink-800' },
    swatch: 'bg-pink-500'
  }
};

// A color key as the UI should treat it: itself when it is in the palette,
// gray otherwise (e.g. a value written by some other client directly to the
// DB -- the backend validates, so this is purely defensive).
export function normalizeLabelColor(color: string | null | undefined): LabelColor {
  return color != null && (LABEL_COLORS as readonly string[]).includes(color) ? (color as LabelColor) : 'gray';
}

export function getLabelColorMeta(color: string | null | undefined): LabelColorMeta {
  return LABEL_COLOR_META[normalizeLabelColor(color)];
}

// Whether a ticket with these labels passes the toolbar's label filter
// (App.tsx). Nothing selected means "don't filter by label" (every ticket
// passes, including unlabeled ones); otherwise a ticket passes when it
// carries at least one of the selected labels (OR).
export function matchesLabelFilter(
  ticketLabels: readonly { id: string }[] | null | undefined,
  selectedIds: readonly string[]
): boolean {
  if (selectedIds.length === 0) return true;
  return (ticketLabels ?? []).some(l => selectedIds.includes(l.id));
}
