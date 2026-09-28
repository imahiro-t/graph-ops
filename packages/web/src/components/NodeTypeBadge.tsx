import React from 'react';
import { useTranslation } from 'react-i18next';
import { getNodeTypeMeta } from '../nodeTypeMeta';

interface Props {
  type: string;
  // Which background the badge is being drawn on. Both variants read from
  // the same getNodeTypeMeta() so the color/icon/label triple never drifts
  // between surfaces (DFLT-00012). Only 'light' has a caller right now
  // (TicketItem's node list); 'dark' is retained for a dark-background
  // surface -- see nodeTypeMeta.ts's note (DFLT-00023 D-2).
  theme: 'light' | 'dark';
  className?: string;
  // Extra classes for the label span, which truncates by default (DFLT-00253:
  // TicketItem lets it wrap instead where the row wraps, so a 200% default
  // font on a narrow screen shows the whole label, not an ellipsis).
  labelClassName?: string;
}

// Shared node-type badge (icon + color + label). Every surface that shows a
// node's type renders it through this one component, so "different surfaces
// use the same rule to distinguish node types" holds by construction rather
// than by two components happening to agree. TicketItem.tsx's node list is
// its only caller at the moment (DFLT-00023 D-2 removed the other one).
export const NodeTypeBadge: React.FC<Props> = ({ type, theme, className, labelClassName }) => {
  const { t } = useTranslation();
  const meta = getNodeTypeMeta(type);
  const colors = theme === 'dark' ? meta.dark : meta.light;
  const Icon = meta.icon;
  const label = meta.labelKey ? t(meta.labelKey) : type;

  // text-[0.625rem] (10px at the default 16px), not text-[10px]: a rem size
  // follows the browser's default font size (WCAG 1.4.4, DFLT-00260).
  return (
    <span
      title={label}
      className={`inline-flex items-center gap-1 text-[0.625rem] font-bold px-1.5 py-0.5 rounded-sm border uppercase tracking-wide ${colors.bg} ${colors.text} ${colors.border} ${className || ''}`}
    >
      <Icon aria-hidden="true" className="w-3 h-3 shrink-0" />
      <span className={labelClassName ? `truncate ${labelClassName}` : 'truncate'}>{label}</span>
    </span>
  );
};
