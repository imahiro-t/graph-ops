import React from 'react';
import { getLabelColorMeta, normalizeLabelColor } from '../labelMeta';

interface Props {
  name: string;
  color: string;
  className?: string;
}

// A label's colored chip (DFLT-00084). Display only; long names truncate
// (the full name stays available via the title attribute). data-label-color
// exposes the (normalized) palette key for tests and styling hooks.
export const LabelChip: React.FC<Props> = ({ name, color, className = '' }) => {
  const meta = getLabelColorMeta(color);
  return (
    <span
      title={name}
      data-testid="label-chip"
      data-label-color={normalizeLabelColor(color)}
      className={`inline-flex items-center max-w-[10rem] px-2 py-0.5 rounded-full border text-[11px] font-semibold leading-4 whitespace-nowrap ${meta.chip.bg} ${meta.chip.text} ${meta.chip.border} ${className}`}
    >
      <span className="truncate">{name}</span>
    </span>
  );
};
