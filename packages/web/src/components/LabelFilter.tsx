import React from 'react';
import { Label } from '../types';
import { LabelChip } from './LabelChip';
import { MultiSelectFilter } from './MultiSelectFilter';

interface Props {
  // The current project's labels (the filter's options).
  labels: Label[];
  // Selected label ids; [] means "don't filter by label".
  selectedIds: string[];
  onChange: (next: string[]) => void;
}

// Toolbar label filter (DFLT-00084). Everything about how it opens, closes
// and behaves now lives in MultiSelectFilter, which the toolbar's status,
// assignee and priority filters use too (DFLT-00086) -- this component is
// only the label-specific part: turning Label records into options whose
// visible content is a colored chip rather than plain text.
//
// That chip is also why these options carry an explicit optionLabel: a
// checkbox's accessible name comes from the text next to it, and the chip's
// name is inside a styled element the filter component knows nothing about.
// The other three filters deliberately pass no optionLabel (see
// MultiSelectFilter.tsx).
//
// The selection model -- empty means no label filtering, so unlabeled
// tickets stay visible, and a selection matches tickets carrying ANY of the
// selected labels (OR) -- is the one all four filters share; the predicate
// is labelMeta.ts's matchesLabelFilter.
export const LabelFilter: React.FC<Props> = ({ labels, selectedIds, onChange }) => (
  <MultiSelectFilter
    panelId="toolbar-label-filter-panel"
    allKey="toolbar.labelAll"
    selectedKey="toolbar.labelSelected"
    groupLabelKey="toolbar.labelGroupLabel"
    emptyKey="toolbar.labelEmpty"
    options={labels.map(l => ({
      value: l.id,
      label: <LabelChip name={l.name} color={l.color} />,
      optionLabel: l.name
    }))}
    selected={selectedIds}
    onChange={onChange}
  />
);
