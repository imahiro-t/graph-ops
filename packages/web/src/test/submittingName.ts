// DFLT-00206: the accessible name of a button that is sending the user's own
// action -- its label followed by the shared "(submitting)" text. The name
// computation may put whitespace between the visible label and the
// visually hidden suffix, so match it anchored at both ends with optional
// whitespace in between.
import i18n from '../i18n';

const escapeRegExp = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

export const submittingName = (label: string): RegExp =>
  new RegExp(`^${escapeRegExp(label)}\\s*${escapeRegExp(i18n.t('common.submitting'))}$`);
