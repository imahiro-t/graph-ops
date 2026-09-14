// Single source of truth for how a node's *type* (as opposed to its status)
// is labeled/colored/iconized, consumed through NodeTypeBadge -- see
// DFLT-00012's completion criterion that every surface showing node types
// must use "the same unified rule" for every node type, old and new alike.
// The only surface rendering it today is TicketItem.tsx's node list (light
// theme); the dark-theme half of each entry below is currently uncalled
// because the graph canvas that used it was removed as dead code
// (DFLT-00023 D-2). It is kept deliberately: the light/dark pair is the
// shape of NodeTypeMeta itself, and dropping one half would make re-adding a
// dark-background surface a re-derivation of every type's color triple.
//
// A type not listed here (a custom node type introduced by a project/team
// workflow.yaml -- see packages/core-go/internal/domain/types.go's NodeType
// doc comment) falls back to FALLBACK_NODE_TYPE_META and its own raw type
// string as the label, rather than breaking the UI.
import {
  ClipboardEdit,
  Eye,
  FileCode,
  Code2,
  ShieldCheck,
  UserCheck,
  FlaskConical,
  FileBarChart2,
  Rocket,
  Search,
  BookOpen,
  HelpCircle,
  type LucideIcon
} from 'lucide-react';
import { NodeType } from './types';

export interface NodeTypeMeta {
  icon: LucideIcon;
  // i18n key under `nodeType.*` (see en/ja translation.json). Absent only on
  // the fallback entry, whose label is the raw type string instead.
  labelKey?: string;
  // Badge colors for TicketItem's light-background node list.
  light: { bg: string; text: string; border: string };
  // Badge colors for a dark-background surface. No current caller -- see the
  // note at the top of this file (DFLT-00023 D-2).
  dark: { bg: string; text: string; border: string };
}

const NODE_TYPE_META: Record<NodeType, NodeTypeMeta> = {
  plan: {
    icon: ClipboardEdit,
    labelKey: 'nodeType.plan',
    light: { bg: 'bg-indigo-100 dark:bg-indigo-950', text: 'text-indigo-700 dark:text-indigo-300', border: 'border-indigo-200 dark:border-indigo-800' },
    dark: { bg: 'bg-indigo-950', text: 'text-indigo-300', border: 'border-indigo-500' }
  },
  review: {
    icon: Eye,
    labelKey: 'nodeType.review',
    light: { bg: 'bg-purple-100 dark:bg-purple-950', text: 'text-purple-700 dark:text-purple-300', border: 'border-purple-200 dark:border-purple-800' },
    dark: { bg: 'bg-purple-950', text: 'text-purple-300', border: 'border-purple-500' }
  },
  gherkin_spec: {
    icon: FileCode,
    labelKey: 'nodeType.gherkinSpec',
    light: { bg: 'bg-amber-100 dark:bg-amber-950', text: 'text-amber-700 dark:text-amber-300', border: 'border-amber-200 dark:border-amber-800' },
    dark: { bg: 'bg-amber-950', text: 'text-amber-300', border: 'border-amber-500' }
  },
  implementation: {
    icon: Code2,
    labelKey: 'nodeType.implementation',
    light: { bg: 'bg-blue-100 dark:bg-blue-950', text: 'text-blue-700 dark:text-blue-300', border: 'border-blue-200 dark:border-blue-800' },
    dark: { bg: 'bg-blue-950', text: 'text-blue-300', border: 'border-blue-500' }
  },
  review_gate: {
    icon: ShieldCheck,
    labelKey: 'nodeType.reviewGate',
    light: { bg: 'bg-teal-100 dark:bg-teal-950', text: 'text-teal-700 dark:text-teal-300', border: 'border-teal-200 dark:border-teal-800' },
    dark: { bg: 'bg-teal-950', text: 'text-teal-300', border: 'border-teal-500' }
  },
  approval_gate: {
    icon: UserCheck,
    labelKey: 'nodeType.approvalGate',
    light: { bg: 'bg-pink-100 dark:bg-pink-950', text: 'text-pink-700 dark:text-pink-300', border: 'border-pink-200 dark:border-pink-800' },
    dark: { bg: 'bg-pink-950', text: 'text-pink-300', border: 'border-pink-500' }
  },
  gherkin_test: {
    icon: FlaskConical,
    labelKey: 'nodeType.gherkinTest',
    light: { bg: 'bg-orange-100 dark:bg-orange-950', text: 'text-orange-700 dark:text-orange-300', border: 'border-orange-200 dark:border-orange-800' },
    dark: { bg: 'bg-orange-950', text: 'text-orange-300', border: 'border-orange-500' }
  },
  report: {
    icon: FileBarChart2,
    labelKey: 'nodeType.report',
    light: { bg: 'bg-cyan-100 dark:bg-cyan-950', text: 'text-cyan-700 dark:text-cyan-300', border: 'border-cyan-200 dark:border-cyan-800' },
    dark: { bg: 'bg-cyan-950', text: 'text-cyan-300', border: 'border-cyan-500' }
  },
  release: {
    icon: Rocket,
    labelKey: 'nodeType.release',
    light: { bg: 'bg-emerald-100 dark:bg-emerald-950', text: 'text-emerald-700 dark:text-emerald-300', border: 'border-emerald-200 dark:border-emerald-800' },
    dark: { bg: 'bg-emerald-950', text: 'text-emerald-300', border: 'border-emerald-500' }
  },
  investigation: {
    icon: Search,
    labelKey: 'nodeType.investigation',
    light: { bg: 'bg-violet-100 dark:bg-violet-950', text: 'text-violet-700 dark:text-violet-300', border: 'border-violet-200 dark:border-violet-800' },
    dark: { bg: 'bg-violet-950', text: 'text-violet-300', border: 'border-violet-500' }
  },
  documentation: {
    icon: BookOpen,
    labelKey: 'nodeType.documentation',
    light: { bg: 'bg-sky-100 dark:bg-sky-950', text: 'text-sky-700 dark:text-sky-300', border: 'border-sky-200 dark:border-sky-800' },
    dark: { bg: 'bg-sky-950', text: 'text-sky-300', border: 'border-sky-500' }
  },
  custom: {
    icon: HelpCircle,
    labelKey: 'nodeType.custom',
    light: { bg: 'bg-slate-200 dark:bg-slate-800', text: 'text-slate-600 dark:text-slate-300', border: 'border-slate-300 dark:border-slate-600' },
    dark: { bg: 'bg-slate-800', text: 'text-slate-300', border: 'border-slate-500' }
  }
};

// Used for any node type string that isn't a key of NODE_TYPE_META above --
// e.g. a project/team workflow.yaml's own custom node type. Deliberately
// distinct (lighter/duller) from the `custom` entry above so a workflow
// author who explicitly opts a node into type: "custom" still gets a
// recognizable badge, while a truly unmodeled type reads as "unstyled".
export const FALLBACK_NODE_TYPE_META: NodeTypeMeta = {
  icon: HelpCircle,
  light: { bg: 'bg-slate-100 dark:bg-slate-800', text: 'text-slate-500 dark:text-slate-400', border: 'border-slate-200 dark:border-slate-700' },
  dark: { bg: 'bg-slate-900', text: 'text-slate-500', border: 'border-slate-700' }
};

export function getNodeTypeMeta(type: string): NodeTypeMeta {
  return NODE_TYPE_META[type as NodeType] || FALLBACK_NODE_TYPE_META;
}
