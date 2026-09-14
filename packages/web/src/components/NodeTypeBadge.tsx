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
}

// Shared node-type badge (icon + color + label). Every surface that shows a
// node's type renders it through this one component, so "different surfaces
// use the same rule to distinguish node types" holds by construction rather
// than by two components happening to agree. TicketItem.tsx's node list is
// its only caller at the moment (DFLT-00023 D-2 removed the other one).
export const NodeTypeBadge: React.FC<Props> = ({ type, theme, className }) => {
  const { t } = useTranslation();
  const meta = getNodeTypeMeta(type);
  const colors = theme === 'dark' ? meta.dark : meta.light;
  const Icon = meta.icon;
  const label = meta.labelKey ? t(meta.labelKey) : type;

  return (
    <span
      title={label}
      className={`inline-flex items-center gap-1 text-[10px] font-bold px-1.5 py-0.5 rounded border uppercase tracking-wide ${colors.bg} ${colors.text} ${colors.border} ${className || ''}`}
    >
      <Icon className="w-3 h-3 shrink-0" />
      <span className="truncate">{label}</span>
    </span>
  );
};
