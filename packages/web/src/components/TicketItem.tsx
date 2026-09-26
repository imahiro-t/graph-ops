import React, { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  ChevronDown,
  ChevronRight,
  GitBranch,
  Play,
  FileCode,
  Globe,
  FileText,
  ExternalLink,
  Download,
  Send,
  Loader2,
  Layers,
  ClipboardEdit,
  Check,
  Copy,
  X,
  Trash2,
  History,
  UserPlus,
  Archive,
  RotateCcw
} from 'lucide-react';
import { Label, TicketDetail, TicketPriority } from '../types';
import { getStatusMeta, TODO_META } from '../statusMeta';
import { GherkinViewer } from './GherkinViewer';
import { MarkdownViewer } from './MarkdownViewer';
import { NodeTypeBadge } from './NodeTypeBadge';
import { PrioritySelect } from './PrioritySelect';
import { LabelChip } from './LabelChip';
import { LabelSelect } from './LabelSelect';
import { StatusLiveRegion } from './StatusLiveRegion';
import { IconButton } from './IconButton';
import { TicketFamily } from './TicketFamily';
import { AutopilotBadges } from './AutopilotBadges';
import { AutopilotControls } from './AutopilotControls';
import { AutopilotDecisions } from './AutopilotDecisions';
import { NO_AUTOPILOT, TicketAutopilotView } from '../lib/autopilotApi';
import { useClaudeLaunch } from '../hooks/useClaudeLaunch';
import { useConfirmDialog } from '../hooks/useConfirmDialog';
import { formatDateTime, formatTime } from '../i18n/formatDate';
import { localizedApiErrorMessage, errorMessage } from '../lib/apiError';
import { apiFetch } from '../lib/apiFetch';
import { isSubmitShortcut } from '../lib/keyboardShortcuts';
import { focusIfLost } from '../lib/focusAfterRemoval';

interface Props {
  ticket: TicketDetail;
  isExpanded: boolean;
  onToggleExpand: () => void;
  // Opens another ticket of the list (DFLT-00142): used by the parent/
  // children links. Optional so a caller without a list can omit it.
  onOpenTicket?: (id: string) => void;
  onRefresh: () => void | Promise<void>;
  // Called in place of onRefresh once this ticket has been deleted
  // (DFLT-00191), so the list can refresh itself and move focus off the card
  // that is about to disappear. Omitted means onRefresh is called instead.
  // The title comes along so the list can announce the delete (DFLT-00194)
  // even when a poll has already dropped the ticket from its own copy.
  onDeleted?: (ticketId: string, ticketTitle: string) => void | Promise<void>;
  // The viewer's own display name (from "アプリ設定", GET /api/settings/app's
  // "myName"). Powers the "assign to me"/"unassign" action buttons below; an
  // empty string hides only those actions (there is no "me" to act as), not
  // the read-only assignee chip -- that chip must stay visible to every
  // viewer regardless of whether they've configured their own name
  // (completion criterion 5: assignee must be identifiable to any viewer).
  myName: string;
  // The current project's registered labels (DFLT-00084): the choices of the
  // detail view's label picker. Omitted/empty makes the picker point the
  // user to Settings instead.
  projectLabels?: Label[];
  // This ticket's autopilot state (DFLT-00142 phase 5), derived from the
  // project's runs: its badges and whether the autopilot buttons can start
  // a run. Omitted means no run concerns it.
  autopilot?: TicketAutopilotView;
  // Called when an autopilot start settles, to refresh the runs at once.
  onAutopilotChanged?: () => void | Promise<void>;
}

// How many label chips the collapsed header row shows before folding the
// rest into "+N" -- the row already carries id/status/priority/title/assignee.
const MAX_HEADER_LABELS = 3;

// How long the ID copy button shows its "copied"/"failed" state before going
// back to idle (DFLT-00143).
const COPY_FEEDBACK_MS = 1500;

// A snapshot of the document selection, taken on mousedown on the header row
// so the following click can tell whether the selection changed in between
// (DFLT-00143). null means there is no (non-empty) selection.
type SelectionSnapshot = {
  anchorNode: Node | null;
  anchorOffset: number;
  focusNode: Node | null;
  focusOffset: number;
  text: string;
} | null;

function takeSelectionSnapshot(): SelectionSnapshot {
  if (typeof window.getSelection !== 'function') return null;
  const sel = window.getSelection();
  if (!sel || sel.isCollapsed || sel.rangeCount === 0) return null;
  return {
    anchorNode: sel.anchorNode,
    anchorOffset: sel.anchorOffset,
    focusNode: sel.focusNode,
    focusOffset: sel.focusOffset,
    text: sel.toString()
  };
}

function sameSelection(a: SelectionSnapshot, b: SelectionSnapshot): boolean {
  if (a === null || b === null) return a === b;
  return a.anchorNode === b.anchorNode && a.anchorOffset === b.anchorOffset
    && a.focusNode === b.focusNode && a.focusOffset === b.focusOffset && a.text === b.text;
}

// Whether a non-empty selection overlaps the row. Range.intersectsNode(row)
// is true when a selected range even partly covers the row (including a
// drag that starts outside the row and ends inside it). It is used instead
// of Selection.containsNode(row, true), whose partial-containment result
// jsdom gets wrong (true for a selection entirely after the row). The
// anchor/focus check is always applied as well, as an extra safety net for
// ranges intersectsNode does not report.
function selectionOverlapsRow(row: HTMLElement): boolean {
  if (typeof window.getSelection !== 'function') return false;
  const sel = window.getSelection();
  if (!sel || sel.isCollapsed || sel.rangeCount === 0) return false;
  for (let i = 0; i < sel.rangeCount; i++) {
    const range = sel.getRangeAt(i);
    if (typeof range.intersectsNode === 'function' && range.intersectsNode(row)) return true;
  }
  return (!!sel.anchorNode && row.contains(sel.anchorNode)) || (!!sel.focusNode && row.contains(sel.focusNode));
}

// Whether a click on the header row is part of a text selection rather than
// a plain "toggle the row" click (DFLT-00143):
// - A keyboard-generated click (detail 0, e.g. Enter/Space on the chevron)
//   is never a selection click.
// - The second and later clicks of a double/triple click (which select a
//   word/the element) always are.
// - Otherwise it is one only if a non-empty selection overlaps the row now
//   AND the selection changed since this click's mousedown -- i.e. the
//   press/release made or changed it (the click that ends a drag over the
//   ID or title). A selection left over from before (Chromium does not
//   clear it on a mousedown over the row's select-none parts, nor before
//   the click when the press lands inside the selection) does not stop the
//   toggle. `pressSnapshot` is undefined when no mousedown on the row was
//   seen for this click (e.g. a synthetic click); then any overlapping
//   selection counts.
function isSelectionClick(
  event: React.MouseEvent,
  row: HTMLElement | null,
  pressSnapshot: SelectionSnapshot | undefined
): boolean {
  if (event.detail === 0) return false;
  if (event.detail >= 2) return true;
  if (!row || !selectionOverlapsRow(row)) return false;
  if (pressSnapshot === undefined) return true;
  return !sameSelection(pressSnapshot, takeSelectionSnapshot());
}

interface RejectReasonPromptProps {
  nodeId: string;
  draft: string;
  onDraftChange: (value: string) => void;
  onConfirm: () => void;
  onCancel: () => void;
  isSubmitting: boolean;
  // Must be stable (useCallback) -- they are this component's effect deps.
  onMount: (nodeId: string) => void;
  onUnmount: (nodeId: string, hadFocus: boolean) => void;
}

// An approval_gate's reject-with-reason prompt (DFLT-00016), split out of
// TicketItem (DFLT-00172) only so that it can tell its parent, at the moment
// it unmounts, whether focus was inside it.
//
// That has to happen in this component's own layout effect cleanup: React 18
// runs the layout effect cleanups of a deleted subtree during the commit's
// mutation phase, before it removes the subtree's host nodes from the DOM (and
// before any layout effect of the same commit). So document.activeElement still
// points into this prompt here, whereas by the time any effect of the parent
// runs the input is gone and focus has fallen to <body>. Tracking focus with
// onFocus/onBlur instead would not work either: whether removing a focused
// element fires blur differs between browsers.
//
// Under React.StrictMode (development) this cleanup also runs for the fake
// unmount StrictMode performs right after mounting, immediately followed by
// the effect running again -- TicketItem's handlePromptMount discards the fake
// closure for that reason.
const RejectReasonPrompt: React.FC<RejectReasonPromptProps> = ({
  nodeId,
  draft,
  onDraftChange,
  onConfirm,
  onCancel,
  isSubmitting,
  onMount,
  onUnmount
}) => {
  const { t } = useTranslation();
  const containerRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  useLayoutEffect(() => {
    const container = containerRef.current;
    onMount(nodeId);
    return () => onUnmount(nodeId, container?.contains(document.activeElement) ?? false);
  }, [nodeId, onMount, onUnmount]);

  // DFLT-00174: a submit that ends while this prompt is still mounted has
  // failed -- a successful rejection closes the prompt before its submitting
  // state is cleared, so this component is gone by then. The confirm button
  // turning disabled mid-submit may have dropped focus to <body> (browser
  // dependent), so bring it back to the reason field, where the user can fix
  // the reason or retry. Only when focus is nowhere or still inside the
  // prompt: a user who moved elsewhere during the submit is left there.
  const wasSubmittingRef = useRef(isSubmitting);
  useEffect(() => {
    const submitEnded = wasSubmittingRef.current && !isSubmitting;
    wasSubmittingRef.current = isSubmitting;
    if (!submitEnded) return;
    const active = document.activeElement;
    const focusIsNowhere = active === null || active === document.body;
    if (focusIsNowhere || containerRef.current?.contains(active)) inputRef.current?.focus();
  }, [isSubmitting]);

  // DFLT-00174: Escape anywhere in the prompt does what the Cancel button
  // does (close, drop the draft, focus back to the Reject button). Not while
  // submitting -- Cancel is disabled then -- nor while an IME composition is
  // in progress (the Escape belongs to the IME). A handled Escape goes no
  // further: stopPropagation for React ancestors, preventDefault for the
  // document-level listeners (App, useModalDialog) that skip defaultPrevented
  // events.
  const handleKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    if (e.key !== 'Escape' || e.nativeEvent.isComposing || e.keyCode === 229 || isSubmitting) return;
    e.preventDefault();
    e.stopPropagation();
    onCancel();
  };

  return (
    <div
      ref={containerRef}
      className="px-3 pb-3 -mt-1 flex items-center gap-2"
      onClick={e => e.stopPropagation()}
      onKeyDown={handleKeyDown}
    >
      <input
        ref={inputRef}
        type="text"
        autoFocus
        value={draft}
        onChange={e => onDraftChange(e.target.value)}
        placeholder={t('ticketItem.approvalGate.reasonPlaceholder')}
        className="flex-1 text-[11px] border border-red-300 dark:border-red-800 rounded px-2 py-1 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 focus:outline-none focus:ring-1 focus:ring-red-400"
      />
      <button
        type="button"
        onClick={onConfirm}
        disabled={isSubmitting || draft.trim() === ''}
        aria-busy={isSubmitting || undefined}
        className="px-2 py-1 bg-red-600 hover:bg-red-500 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded text-[11px] font-bold flex items-center gap-1 transition shrink-0"
      >
        {isSubmitting ? (
          <Loader2 aria-hidden="true" className="w-3 h-3 animate-spin" />
        ) : (
          <X aria-hidden="true" className="w-3 h-3" />
        )}
        {t('ticketItem.approvalGate.confirmReject')}
        {/* DFLT-00176: the spinner is aria-hidden and disabled alone reads as
            "unavailable", so while submitting the accessible name also says
            so (with aria-busy as the standard hint alongside). */}
        {isSubmitting && <span className="sr-only">{t('ticketItem.approvalGate.submitting')}</span>}
      </button>
      <button
        type="button"
        onClick={onCancel}
        disabled={isSubmitting}
        className="px-2 py-1 text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 disabled:opacity-50 text-[11px] font-semibold shrink-0"
      >
        {t('ticketItem.approvalGate.cancelReject')}
      </button>
    </div>
  );
};

export const TicketItem: React.FC<Props> = ({
  ticket,
  isExpanded,
  onToggleExpand,
  onOpenTicket,
  onRefresh,
  onDeleted,
  myName,
  projectLabels = [],
  autopilot = NO_AUTOPILOT,
  onAutopilotChanged
}) => {
  const { t, i18n } = useTranslation();
  const [promptText, setPromptText] = useState('');
  const [activeTab, setActiveTab] = useState<'nodes' | 'gherkin' | 'html' | 'artifacts'>('nodes');
  const [expandedNodeIds, setExpandedNodeIds] = useState<Set<string>>(new Set());
  const [isDescriptionExpanded, setIsDescriptionExpanded] = useState(false);
  const { isLaunching: isRunning, lastMessage: statusMessage, launch: handleRunClaude } = useClaudeLaunch(onRefresh);

  // The execution graph panel must never scroll -- it always renders in
  // full, growing past the default height when the graph has many nodes
  // (see graphPanelRef's min-h-[32rem] below). The node/artifact panel on
  // the right is allowed to scroll internally, but its *card* height should
  // track the graph panel's rendered height once that exceeds the default,
  // rather than the two drifting apart or the page having to reconcile two
  // independently auto-sized columns. A ResizeObserver on the graph panel
  // (rather than relying on CSS grid's default stretch-to-tallest-item
  // behavior) is what makes this a one-way match: stretch would also let an
  // unusually long *node list* balloon the graph panel's height even though
  // the graph itself doesn't need it, which is exactly the mismatch this is
  // meant to avoid.
  const graphPanelRef = useRef<HTMLDivElement>(null);
  const [nodeListCardHeight, setNodeListCardHeight] = useState<number | null>(null);

  useLayoutEffect(() => {
    if (!isExpanded) return;
    const el = graphPanelRef.current;
    if (!el) return;
    const updateHeight = () => setNodeListCardHeight(el.getBoundingClientRect().height);
    updateHeight();
    const observer = new ResizeObserver(updateHeight);
    observer.observe(el);
    return () => observer.disconnect();
  }, [isExpanded, ticket.nodes.length, ticket.edges.length]);

  const handleSendPrompt = async () => {
    if (isRunning || !promptText.trim()) return;
    // Only clear the free-text prompt on a successful send -- a failed one
    // should keep the text so it can be retried without retyping it.
    const succeeded = await handleRunClaude(promptText, ticket.id);
    if (succeeded) setPromptText('');
  };

  // Self-assign ("assign to me" / "unassign") -- the ticket's only form of
  // assignment; see ticket.assignee and Props.myName's doc comments.
  //
  // ticket.assignee is the server-recorded name of whoever last assigned
  // themselves, not a viewer-local flag (DFLT-00047), so this button only
  // ever acts when the ticket is unassigned or already assigned to *this*
  // viewer (isAssignedToMe below) -- someone else's assignment is shown
  // read-only elsewhere and can't be taken over or cleared from here.
  const isAssignedToMe = !!ticket.assignee && ticket.assignee === myName;
  const [assignToMeSaving, setAssignToMeSaving] = useState(false);
  const [assignToMeError, setAssignToMeError] = useState('');

  const handleToggleAssignedToMe = async (e: React.MouseEvent) => {
    e.stopPropagation();
    if (assignToMeSaving) return;
    setAssignToMeSaving(true);
    setAssignToMeError('');
    try {
      const res = await apiFetch(`/api/tickets/${ticket.id}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ assignee: isAssignedToMe ? null : myName })
      });
      if (!res.ok) throw new Error(await localizedApiErrorMessage(t, res));
      await onRefresh();
    } catch (err) {
      setAssignToMeError(errorMessage(err, t('errors.UNKNOWN')));
    } finally {
      setAssignToMeSaving(false);
    }
  };

  // Header row text selection and the ID copy button (DFLT-00143).
  const headerRowRef = useRef<HTMLDivElement>(null);
  // The selection as it was on the latest mousedown on the row; consumed
  // (reset to undefined) by the click that follows it.
  const pressSnapshotRef = useRef<SelectionSnapshot | undefined>(undefined);
  const handleHeaderMouseDown = (e: React.MouseEvent) => {
    pressSnapshotRef.current = e.button === 0 ? takeSelectionSnapshot() : undefined;
  };
  const handleHeaderClick = (e: React.MouseEvent) => {
    const pressSnapshot = pressSnapshotRef.current;
    pressSnapshotRef.current = undefined;
    if (isSelectionClick(e, headerRowRef.current, pressSnapshot)) return;
    onToggleExpand();
  };
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'failed'>('idle');
  const copyTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Guards showCopyResult against a writeText that settles after the row
  // unmounted (a list refresh or a filter change), which would otherwise
  // start a timer no cleanup would ever clear.
  const mountedRef = useRef(false);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      if (copyTimerRef.current) clearTimeout(copyTimerRef.current);
      copyTimerRef.current = null;
    };
  }, []);
  const showCopyResult = (state: 'copied' | 'failed') => {
    if (!mountedRef.current) return;
    if (copyTimerRef.current) clearTimeout(copyTimerRef.current);
    setCopyState(state);
    copyTimerRef.current = setTimeout(() => {
      copyTimerRef.current = null;
      setCopyState('idle');
    }, COPY_FEEDBACK_MS);
  };
  // No document.execCommand('copy') fallback: it is deprecated. Without the
  // Clipboard API (e.g. plain HTTP outside localhost) the button just shows
  // the "failed" state; the ID text itself can still be selected and copied.
  const handleCopyId = async (e: React.MouseEvent) => {
    e.stopPropagation();
    try {
      const clipboard = typeof navigator !== 'undefined' ? navigator.clipboard : undefined;
      if (!clipboard || typeof clipboard.writeText !== 'function') throw new Error('Clipboard API unavailable');
      await clipboard.writeText(ticket.id);
      showCopyResult('copied');
    } catch {
      showCopyResult('failed');
    }
  };
  const copyIdLabel =
    copyState === 'copied'
      ? t('ticketItem.copyId.copied', { id: ticket.id })
      : copyState === 'failed'
        ? t('ticketItem.copyId.failed', { id: ticket.id })
        : t('ticketItem.copyId.button', { id: ticket.id });
  const copyIdStatus = copyState === 'idle' ? '' : copyIdLabel;

  // Priority (DFLT-00048): changed from the ticket header via PATCH
  // /api/tickets/{id}'s "priority" field. Always one of the three levels --
  // a priority can't be cleared (DFLT-00083; the backend rejects null).
  const [prioritySaving, setPrioritySaving] = useState(false);
  const [priorityError, setPriorityError] = useState('');

  const handleChangePriority = async (nextPriority: TicketPriority) => {
    if (prioritySaving || nextPriority === ticket.priority) return;
    setPrioritySaving(true);
    setPriorityError('');
    try {
      const res = await apiFetch(`/api/tickets/${ticket.id}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ priority: nextPriority })
      });
      if (!res.ok) throw new Error(await localizedApiErrorMessage(t, res));
      await onRefresh();
    } catch (err) {
      setPriorityError(errorMessage(err, t('errors.UNKNOWN')));
    } finally {
      setPrioritySaving(false);
    }
  };

  // approval_gate approve/reject (DFLT-00012). Keyed by node id (rather than
  // one flat flag) so an in-flight decision on one approval_gate node
  // doesn't disable the buttons on another one in the same ticket.
  // DFLT-00173: a set, not a single id -- decisions on different gates can be
  // in flight at the same time, and one finishing must not make another,
  // still in flight, look idle (and clickable again). Always updated
  // functionally, and only for the gate in question.
  const [submittingApprovalNodeIds, setSubmittingApprovalNodeIds] = useState<ReadonlySet<string>>(() => new Set());
  // The same set, readable synchronously: handleApprovalDecision refuses a
  // second decision on a gate whose first one is still in flight, without
  // relying on the buttons' disabled state having been rendered yet.
  const approvalsInFlightRef = useRef<Set<string>>(new Set());
  const [approvalErrors, setApprovalErrors] = useState<Record<string, string>>({});
  // DFLT-00016: rejecting an approval_gate now requires a free-text reason
  // (no more window.confirm -- the reason input itself, plus a distinctly
  // labeled confirm button, is the confirmation step). rejectPrompt tracks
  // which node's reason prompt is currently open, together with its draft;
  // at most one at a time keeps this simple. DFLT-00173: the draft belongs
  // to that one prompt, so closing the prompt and dropping the draft are a
  // single update -- a decision on some other gate finishing can never wipe
  // what is being typed here.
  const [rejectPrompt, setRejectPrompt] = useState<{ nodeId: string; draft: string } | null>(null);
  const rejectingNodeId = rejectPrompt?.nodeId ?? null;
  const rejectReasonDraft = rejectPrompt?.draft ?? '';
  const setRejectReasonDraft = useCallback(
    (draft: string) => setRejectPrompt(prev => (prev ? { ...prev, draft } : prev)),
    []
  );

  // DFLT-00172: where focus goes when the reject prompt unmounts, and what
  // gets announced. Unmounting the prompt while focus is inside it would
  // otherwise drop focus to <body> with no word about what happened (WCAG
  // 2.4.3 / 4.1.3). By the time any effect of this component runs, the
  // prompt's DOM is already gone and document.activeElement is <body>, so
  // RejectReasonPrompt reports "was focus inside me?" from its own layout
  // effect cleanup (see there) into these refs, and the layout effect below
  // decides what the closure meant. Refs only -- the child's cleanup must
  // not setState mid-commit.
  //
  // mountedPromptNodeRef: node id of the prompt currently mounted, if any.
  const mountedPromptNodeRef = useRef<string | null>(null);
  // promptClosureRef: the prompt that just unmounted and whether focus was
  // inside it at that moment. Consumed (reset to null) by the layout effect.
  const promptClosureRef = useRef<{ nodeId: string; hadFocus: boolean } | null>(null);
  // closeReasonRef: why this component itself closed the prompt, tagged
  // with the node id so it can never be applied to some other, later
  // unmount. Anything else closing it (ticket CLOSED, gate judged
  // elsewhere, a poll landing mid-submit) leaves this unset.
  const closeReasonRef = useRef<{ nodeId: string; reason: 'submitted' | 'cancelled' | 'switched' } | null>(null);
  // rejectsInFlightRef: node ids whose reject POST is in flight. A prompt
  // that unmounts during it (App's polling can land the REJECTED gate before
  // the POST's own response) is parked in deferredClosuresRef under its node
  // id, and the response decides whether it was our rejection or the gate
  // being judged elsewhere. Keyed per node because rejects on different
  // gates can be in flight at the same time (approval decisions are only
  // serialised per gate -- see submittingApprovalNodeIds), and one gate's
  // response must never clear or settle another gate's state.
  const rejectsInFlightRef = useRef<Set<string>>(new Set());
  const deferredClosuresRef = useRef<Map<string, { hadFocus: boolean }>>(new Map());
  // Announced through a StatusLiveRegion that is always mounted, placed
  // directly under this component's root div (outside the header row and
  // its own copy-id region). Cleared whenever a prompt opens so repeating
  // the same text is still a change the screen reader picks up.
  const [approvalAnnouncement, setApprovalAnnouncement] = useState('');
  const rootRef = useRef<HTMLDivElement>(null);

  const handlePromptMount = useCallback((nodeId: string) => {
    mountedPromptNodeRef.current = nodeId;
    // React.StrictMode (main.tsx, development only) fakes an unmount and a
    // remount of every newly mounted component right after the commit. The
    // fake unmount records { nodeId, hadFocus: true } (the autoFocus input
    // has focus), and nothing would consume it until the next commit -- the
    // user's first keystroke -- which would then yank focus to the node
    // toggle and announce "no longer awaiting approval". A real unmount is
    // never followed by a mount of the same node's prompt, so a remount
    // discarding its own closure only ever drops the fake one. (The layout
    // effect below also ignores a closure whose prompt is still mounted, in
    // case the order of StrictMode's double invocation ever changes.)
    if (promptClosureRef.current?.nodeId === nodeId) promptClosureRef.current = null;
  }, []);
  const handlePromptUnmount = useCallback((nodeId: string, hadFocus: boolean) => {
    // When one prompt replaces another in the same commit, the old one's
    // cleanup (mutation phase) runs before the new one's mount (layout
    // phase), so the new id survives.
    if (mountedPromptNodeRef.current === nodeId) mountedPromptNodeRef.current = null;
    promptClosureRef.current = { nodeId, hadFocus };
  }, []);

  const findInTicket = (selector: string) => rootRef.current?.querySelector<HTMLElement>(selector) ?? null;
  // The node row's expand toggle is rendered in every state (DFLT-00152),
  // which makes it the one stable place to land after the prompt is gone.
  const focusNodeToggle = (nodeId: string) => findInTicket(`[data-testid="node-toggle-expand-${nodeId}"]`)?.focus();
  const focusIsNowhere = () => {
    const active = document.activeElement;
    return active === null || active === document.body;
  };
  const gateName = (nodeId: string) => ticket.nodes.find(n => n.id === nodeId)?.name ?? nodeId;
  const announceRejected = (nodeId: string) =>
    setApprovalAnnouncement(t('ticketItem.approvalGate.rejectedAnnouncement', { name: gateName(nodeId) }));
  // `deferred`: the prompt unmounted earlier (a poll removed it while its
  // reject POST was in flight) and is only being settled now, when the
  // response arrives. hadFocus describes the moment it unmounted, not now:
  // in between the user may have moved on -- opened another gate's prompt,
  // tabbed to some other control -- and a late response must not pull them
  // back. So a deferred settle only moves focus while it is still nowhere
  // (<body>), i.e. where the prompt's removal left it.
  //
  // The prompt closed because the gate stopped being pending for a reason
  // other than our own rejection. Only a user who was inside the prompt
  // gets moved and told -- a background refresh must not steal focus or
  // read out something unrelated to what they are doing.
  const settleNoLongerPending = (nodeId: string, hadFocus: boolean, deferred = false) => {
    if (!hadFocus) return;
    if (!deferred || focusIsNowhere()) focusNodeToggle(nodeId);
    setApprovalAnnouncement(t('ticketItem.approvalGate.noLongerPendingAnnouncement', { name: gateName(nodeId) }));
  };
  // Our rejection went through. Focus follows unless the user has already
  // moved somewhere else on purpose; "nowhere" counts as not having moved,
  // since the confirm button turning disabled mid-submit drops focus to
  // <body> in some browsers.
  const settleSubmitted = (nodeId: string, hadFocus: boolean, deferred = false) => {
    if (deferred ? focusIsNowhere() : hadFocus || focusIsNowhere()) focusNodeToggle(nodeId);
    announceRejected(nodeId);
  };

  // No dependency array: runs after every commit, and consumes whatever
  // RejectReasonPrompt's cleanup recorded in that same commit (its cleanup
  // runs in the mutation phase, before this layout effect).
  useLayoutEffect(() => {
    const closure = promptClosureRef.current;
    if (!closure) return;
    promptClosureRef.current = null;
    // Still mounted: a StrictMode fake unmount, not a real one (see
    // handlePromptMount). Leave closeReasonRef for the real unmount.
    if (mountedPromptNodeRef.current === closure.nodeId) return;
    const closeReason = closeReasonRef.current;
    if (closeReason?.nodeId === closure.nodeId) {
      closeReasonRef.current = null;
      if (closeReason.reason === 'cancelled') {
        // Back to the Reject button that just reappeared in its place. No
        // announcement: the user did this themselves.
        findInTicket(`[data-testid="node-reject-${closure.nodeId}"]`)?.focus();
      } else if (closeReason.reason === 'submitted') {
        settleSubmitted(closure.nodeId, closure.hadFocus);
      }
      // 'switched': another gate's prompt took over and its autoFocus input
      // already has focus -- nothing to move or announce.
      return;
    }
    if (rejectsInFlightRef.current.has(closure.nodeId)) {
      // Our reject POST is still in flight; its response settles this.
      deferredClosuresRef.current.set(closure.nodeId, { hadFocus: closure.hadFocus });
      return;
    }
    settleNoLongerPending(closure.nodeId, closure.hadFocus);
  });

  // Ticket deletion. A confirm dialog gates it (this is unrecoverable --
  // there's no undo/trash): the in-app ConfirmDialog through
  // useConfirmDialog (DFLT-00148), not window.confirm, so browser automation
  // and tests can drive it. Its overlay stops click propagation, so
  // answering it never toggles the card. On cancel focus goes back to the
  // delete button. After a successful delete the re-fetch removes the card
  // itself, so where focus goes then is the list's call: onDeleted (App)
  // moves it to a neighboring card (DFLT-00191). After a failed one -- or a
  // successful one whose re-fetch still shows this card (the re-fetch failed,
  // or its result was discarded as superseded) -- focus is put back on the
  // delete button once it is re-enabled: a browser may drop focus from a
  // button while it is disabled. In the latter case App keeps its move
  // pending, and takes focus on to a neighbor when a later fetch drops the
  // card. When the card is gone this effect never runs, and focusIfLost
  // leaves alone a neighbor App has already focused.
  const [isDeletingTicket, setIsDeletingTicket] = useState(false);
  const [deleteError, setDeleteError] = useState('');
  const { confirm: confirmDelete, confirmDialog: deleteConfirmDialog } = useConfirmDialog();
  const deleteButtonRef = useRef<HTMLButtonElement>(null);
  const [refocusDeleteButton, setRefocusDeleteButton] = useState(false);
  useEffect(() => {
    if (!refocusDeleteButton || isDeletingTicket) return;
    setRefocusDeleteButton(false);
    focusIfLost(deleteButtonRef.current);
  }, [refocusDeleteButton, isDeletingTicket]);

  const handleDeleteTicket = async (e: React.MouseEvent) => {
    e.stopPropagation();
    const confirmed = await confirmDelete({
      title: t('ticketItem.delete.confirmTitle'),
      message: t('ticketItem.delete.confirm', { id: ticket.id, title: ticket.title }),
      confirmLabel: t('ticketItem.delete.confirmButton'),
      tone: 'danger',
      testIdPrefix: 'ticket-delete-confirm'
    });
    if (!confirmed) return;
    setIsDeletingTicket(true);
    setDeleteError('');
    try {
      const res = await apiFetch(`/api/tickets/${ticket.id}`, { method: 'DELETE' });
      if (!res.ok) {
        throw new Error(await localizedApiErrorMessage(t, res));
      }
      await (onDeleted ? onDeleted(ticket.id, ticket.title) : onRefresh());
      setRefocusDeleteButton(true);
    } catch (err) {
      setDeleteError(errorMessage(err, t('errors.UNKNOWN')));
      setRefocusDeleteButton(true);
    } finally {
      setIsDeletingTicket(false);
    }
  };

  // Close/reopen (DFLT-00043). Closing takes an optional free-text reason,
  // via the same inline-prompt pattern as the approval_gate reject reason
  // below -- but unlike rejecting, the reason is optional here (closing works
  // from any status, with or without an explanation), so the confirm button
  // is never disabled by empty text. Reopening needs no reason and no
  // confirmation prompt: it just moves the ticket back into the normal
  // status flow, the same one-click pattern as the self-assign toggle above.
  const [isClosePromptOpen, setIsClosePromptOpen] = useState(false);
  const [closeReasonDraft, setCloseReasonDraft] = useState('');
  const [isClosingTicket, setIsClosingTicket] = useState(false);
  const [closeError, setCloseError] = useState('');
  const [isReopeningTicket, setIsReopeningTicket] = useState(false);
  const [reopenError, setReopenError] = useState('');

  const handleCloseTicket = async () => {
    setIsClosingTicket(true);
    setCloseError('');
    try {
      const res = await apiFetch(`/api/tickets/${ticket.id}/close`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ reason: closeReasonDraft })
      });
      if (!res.ok) throw new Error(await localizedApiErrorMessage(t, res));
      setIsClosePromptOpen(false);
      setCloseReasonDraft('');
      await onRefresh();
    } catch (err) {
      setCloseError(errorMessage(err, t('errors.UNKNOWN')));
    } finally {
      setIsClosingTicket(false);
    }
  };

  const handleReopenTicket = async (e: React.MouseEvent) => {
    e.stopPropagation();
    setIsReopeningTicket(true);
    setReopenError('');
    try {
      const res = await apiFetch(`/api/tickets/${ticket.id}/reopen`, { method: 'POST' });
      if (!res.ok) throw new Error(await localizedApiErrorMessage(t, res));
      await onRefresh();
    } catch (err) {
      setReopenError(errorMessage(err, t('errors.UNKNOWN')));
    } finally {
      setIsReopeningTicket(false);
    }
  };

  // DFLT-00016: rejecting an approval_gate now requires a non-empty free-text
  // reason, sent as a "rejection_reason" text artifact in the same request
  // (the same convention the CLI's `complete-node --reason` flag uses --
  // see engine.CompleteNode's doc comment and handleCompleteNode). Approving
  // never takes a reason. reason is validated by the caller (the reject
  // button is disabled while empty, see the reason-prompt JSX below) rather
  // than here, so this function has one job: send the request.
  const handleApprovalDecision = async (nodeId: string, passed: boolean, reason?: string) => {
    // One decision per gate at a time (DFLT-00173). Checked before anything
    // else, so a refused second call leaves the first one's state alone.
    if (approvalsInFlightRef.current.has(nodeId)) return;
    approvalsInFlightRef.current.add(nodeId);
    setSubmittingApprovalNodeIds(prev => new Set(prev).add(nodeId));
    if (!passed) rejectsInFlightRef.current.add(nodeId);
    setApprovalErrors(prev => {
      if (!(nodeId in prev)) return prev;
      const next = { ...prev };
      delete next[nodeId];
      return next;
    });
    try {
      const body: { passed: boolean; artifacts?: Array<{ name: string; type: string; content: string }> } = { passed };
      if (!passed) {
        body.artifacts = [{ name: 'rejection_reason', type: 'text', content: reason || '' }];
      }
      const res = await apiFetch(`/api/nodes/${nodeId}/complete`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      });
      if (!res.ok) {
        throw new Error(await localizedApiErrorMessage(t, res));
      }
      if (!passed) {
        // DFLT-00172: how the rejection's focus/announcement is settled
        // depends on whether its prompt is still on screen.
        const deferred = deferredClosuresRef.current.get(nodeId);
        if (mountedPromptNodeRef.current === nodeId) {
          // Still mounted: the setRejectPrompt below unmounts it in the
          // next commit, and the layout effect settles it then.
          closeReasonRef.current = { nodeId, reason: 'submitted' };
        } else if (deferred) {
          // A poll already removed it while the POST was in flight. The DOM
          // is committed, so settle it right here -- once.
          settleSubmitted(nodeId, deferred.hadFocus, true);
        } else {
          // Its prompt was replaced by another gate's while in flight: the
          // user is typing there, so only announce.
          announceRejected(nodeId);
        }
      }
      // Close this gate's prompt (and drop its draft) only if it is the one
      // open: another gate's prompt may be open with a draft in progress.
      setRejectPrompt(prev => (prev?.nodeId === nodeId ? null : prev));
      await onRefresh();
    } catch (err) {
      setApprovalErrors(prev => ({ ...prev, [nodeId]: errorMessage(err, t('errors.UNKNOWN')) }));
      // The prompt vanished mid-submit and the rejection failed: the gate
      // stopped being pending some other way.
      const deferred = deferredClosuresRef.current.get(nodeId);
      if (deferred) settleNoLongerPending(nodeId, deferred.hadFocus, true);
    } finally {
      if (!passed) {
        // Only this gate's entries: another gate's reject may still be in
        // flight, with its own parked closure.
        rejectsInFlightRef.current.delete(nodeId);
        deferredClosuresRef.current.delete(nodeId);
      }
      approvalsInFlightRef.current.delete(nodeId);
      setSubmittingApprovalNodeIds(prev => {
        const next = new Set(prev);
        next.delete(nodeId);
        return next;
      });
    }
  };

  // Opens the reject-with-reason prompt for nodeId, closing it for whatever
  // other node had it open (only one at a time -- see rejectingNodeId).
  const startRejecting = (nodeId: string) => {
    // DFLT-00172: a fresh prompt starts with no leftover close reason, and
    // an announcement slot that is empty so the next one is a change.
    closeReasonRef.current = null;
    const openPromptNodeId = mountedPromptNodeRef.current;
    if (openPromptNodeId !== null && openPromptNodeId !== nodeId) {
      closeReasonRef.current = { nodeId: openPromptNodeId, reason: 'switched' };
    }
    setApprovalAnnouncement('');
    setRejectPrompt({ nodeId, draft: '' });
  };
  const cancelRejecting = () => {
    if (rejectingNodeId !== null) closeReasonRef.current = { nodeId: rejectingNodeId, reason: 'cancelled' };
    setRejectPrompt(null);
  };

  const toggleNodeExpand = (nodeId: string) => {
    setExpandedNodeIds(prev => {
      const next = new Set(prev);
      if (next.has(nodeId)) next.delete(nodeId);
      else next.add(nodeId);
      return next;
    });
  };

  // Node status badges. Label and colors come from statusMeta.ts, shared
  // with the ticket status badge (DFLT-00030); only the node badge's own
  // look (smaller size, pulse dot while IN PROGRESS, medium weight for
  // not-yet-started) is decided here.
  const getNodeBadge = (status: string) => {
    const meta = getStatusMeta(status);
    const isInProgress = status === 'IN PROGRESS';
    const isNotStarted = meta === TODO_META;
    return (
      <span className={`text-[11px] px-2 py-0.5 rounded-full ${isNotStarted ? 'font-medium' : 'font-bold'} ${meta.chip.bg} ${meta.chip.text}${isInProgress ? ' flex items-center gap-1' : ''}`}>
        {isInProgress && <span className="w-1.5 h-1.5 rounded-full bg-blue-500 animate-pulse" />}
        {t(meta.labelKey)}
      </span>
    );
  };

  // "Open in new tab" link. gherkin/text/html all open /artifacts/{id}/preview,
  // a small client-side page (see ArtifactPreviewPage) rather than
  // /api/artifacts/{id}/content directly. For gherkin/text that page fetches
  // the same raw content and renders it with the same
  // GherkinViewer/MarkdownViewer the inline preview uses -- .../content
  // deliberately never serves those two types as HTML (see
  // internal/artifactcontent/content.go's contentTypeFor), so linking
  // straight to it would only ever show raw text in the new tab. For html,
  // linking straight to .../content used to render agent-authored HTML as a
  // same-origin top-level document with no isolation (DFLT-00053); the
  // preview page instead embeds it in the same sandbox="allow-scripts"
  // <iframe> the inline preview below already uses, so a new tab never opens
  // artifact HTML with the app's own origin.
  const openInNewTabLink = (artifact: {
    id: string;
    type: string;
    name: string;
    content?: string | null;
    file_path?: string | null;
    has_content?: boolean;
  }) => {
    if (!(artifact.content || artifact.file_path || artifact.has_content)) return null;
    const href = `/artifacts/${artifact.id}/preview?type=${artifact.type}&name=${encodeURIComponent(artifact.name)}`;
    return (
      <a
        href={href}
        target="_blank"
        rel="noreferrer"
        className="text-indigo-600 dark:text-indigo-400 hover:underline flex items-center gap-1 text-[11px]"
      >
        <ExternalLink aria-hidden="true" className="w-3 h-3" />
        {t('ticketItem.openInNewTab')}
      </a>
    );
  };

  // Individual-artifact download: GET /api/artifacts/{id}/content?download=1
  // serves the exact same bytes openInNewTabLink/the inline preview do, but
  // with Content-Disposition: attachment and a guaranteed filename (see
  // artifactDownloadFilename in internal/httpserver/tickets.go) so the
  // browser always saves it instead of trying to render it in place.
  const downloadLink = (artifact: {
    id: string;
    content?: string | null;
    file_path?: string | null;
    has_content?: boolean;
  }) => {
    if (!(artifact.content || artifact.file_path || artifact.has_content)) return null;
    return (
      <a
        href={`/api/artifacts/${artifact.id}/content?download=1`}
        className="text-slate-500 dark:text-slate-400 hover:text-indigo-600 dark:hover:text-indigo-400 flex items-center gap-1 text-[11px]"
      >
        <Download aria-hidden="true" className="w-3 h-3" />
        {t('ticketItem.download')}
      </a>
    );
  };

  // Accessible name for a capped inline preview (GherkinViewer /
  // MarkdownViewer with `scrollable`). The artifact name alone is not unique:
  // a review loop-back re-runs a node, so the same node -- and even more so
  // the Artifacts tab, which lists the whole ticket -- can hold several
  // artifacts sharing a name (two "実装メモ", two review verdicts, ...).
  // Sighted users tell those apart by position and by the timestamp the
  // Gherkin tab already prints; adding created_at to the name gives assistive
  // technology the same distinguishing information, in the same wording the
  // UI shows (DFLT-00085 accessibility review, condition 2).
  const artifactScrollLabel = (artifact: { name: string; created_at: string }) =>
    t('ticketItem.artifactScrollRegion', {
      name: artifact.name,
      timestamp: formatDateTime(artifact.created_at, i18n.language)
    });

  const description = ticket.description || '';

  const totalNodes = ticket.nodes.length;
  const doneNodes = ticket.nodes.filter(n => n.status === 'DONE').length;
  const progressPercent = totalNodes > 0 ? Math.round((doneNodes / totalNodes) * 100) : 0;
  // A ticket sitting at an approval_gate must never be mistaken for finished
  // or otherwise overlooked -- an is_manual node like this never advances on
  // its own, and the ticket-level status badge can still say things like
  // "IN PROGRESS" while it waits. Rather than a separate badge, the node's
  // own status tick (see the per-node ticks below) blinks yellow while
  // pending -- pendingApprovalNodeIds is just the lookup set for that.
  //
  // All of a ticket's nodes are persisted up front (see persistPlan), so a
  // downstream approval_gate (e.g. release_approval) sits at status TODO
  // from creation, long before the ticket actually reaches it -- filtering
  // on status alone made every approval_gate in the graph blink at once.
  // What actually determines whether a gate is the one the ticket is
  // currently stuck on is the same prerequisite check the engine itself
  // uses to decide what's executable (see GetExecutableNodes in
  // packages/core-go/internal/engine/engine.go): every non-loop edge
  // feeding into it must come from a DONE node. The server's own copy of
  // this "awaiting approval" definition is engine.HasPendingApproval, which
  // drives both the ticket's IN REVIEW status and the project switcher's
  // pending-approval counts (DFLT-00144); keep the two in step.
  //
  // DFLT-00157: a CLOSED ticket's gates are never pending here, however
  // reached they look. The engine refuses to complete any node of a CLOSED
  // ticket (INVALID_NODE_STATE), so approving or rejecting would always
  // fail -- no blink, no "awaiting approval" tooltip, no approve/reject
  // buttons. This is the same definition GET /api/projects/pending-approvals
  // counts by (handlePendingApprovals in
  // packages/core-go/internal/httpserver/pending_approvals.go leaves CLOSED
  // tickets out, DFLT-00144 D-1); a DONE ticket is still counted by both.
  // Change one only together with the other.
  const nodeById = new Map(ticket.nodes.map(n => [n.id, n]));
  const isNodeReached = (nodeId: string) =>
    ticket.edges.every(e => {
      if (e.to_node_id !== nodeId || e.condition === 'iteration_loop') return true;
      return nodeById.get(e.from_node_id)?.status === 'DONE';
    });
  const pendingApprovalNodeIds = new Set(
    ticket.status === 'CLOSED'
      ? []
      : ticket.nodes
          .filter(n => n.type === 'approval_gate' && n.status === 'TODO' && isNodeReached(n.id))
          .map(n => n.id)
  );
  // DFLT-00157: once the gate an open reject prompt belongs to stops being
  // pending (the ticket turned CLOSED, or the gate was judged elsewhere),
  // actually close the prompt and drop its draft rather than just hiding it.
  // Otherwise reopening the ticket (or the gate going back to TODO) would
  // remount the autoFocus reason input with the stale draft and pull focus
  // away from whatever the user was doing.
  const rejectingGateNoLongerPending = rejectingNodeId !== null && !pendingApprovalNodeIds.has(rejectingNodeId);
  useEffect(() => {
    if (rejectingGateNoLongerPending) setRejectPrompt(null);
  }, [rejectingGateNoLongerPending]);
  // DFLT-00016: a REJECTED approval_gate is a materially different state
  // from a never-judged one -- it's not waiting on a human clicking
  // approve/reject here, it's waiting on process-ticket's triage (deciding
  // whether to reopen specific nodes or report back that a requirements-level
  // rethink is needed). Surfaced separately, read-only, so it isn't confused
  // with pendingApprovalNodeIds' actionable approve/reject state.
  const rejectedApprovalNodes = ticket.nodes.filter(n => n.type === 'approval_gate' && n.status === 'REJECTED');
  const loopEdges = ticket.edges.filter(e => e.condition === 'iteration_loop');
  const gherkinArtifacts = ticket.artifacts.filter(a => a.type === 'gherkin');
  const htmlArtifacts = ticket.artifacts.filter(a => a.type === 'html');

  // SVG mini-graph preview: layered DAG layout. Nodes are grouped into rows
  // ("levels") by longest path from a source node, using only forward edges
  // -- iteration_loop edges point backward and would break a forward
  // topological layering, so they're excluded here and drawn separately
  // below. Nodes that land on the same level ran (or will run) in parallel,
  // so they're spread across columns on that row instead of every node
  // sharing one column top-to-bottom, which made parallel and serial graphs
  // look identical.
  const forwardEdges = ticket.edges.filter(e => e.condition !== 'iteration_loop');
  const adjacency = new Map<string, string[]>();
  const indegree = new Map<string, number>();
  ticket.nodes.forEach(n => {
    adjacency.set(n.id, []);
    indegree.set(n.id, 0);
  });
  forwardEdges.forEach(e => {
    if (!adjacency.has(e.from_node_id) || !indegree.has(e.to_node_id)) return;
    adjacency.get(e.from_node_id)!.push(e.to_node_id);
    indegree.set(e.to_node_id, (indegree.get(e.to_node_id) || 0) + 1);
  });
  const level = new Map<string, number>();
  const remainingIndegree = new Map(indegree);
  const levelQueue: string[] = [];
  ticket.nodes.forEach(n => {
    level.set(n.id, 0);
    if ((indegree.get(n.id) || 0) === 0) levelQueue.push(n.id);
  });
  for (let qi = 0; qi < levelQueue.length; qi++) {
    const cur = levelQueue[qi];
    const curLevel = level.get(cur) || 0;
    for (const next of adjacency.get(cur) || []) {
      if (curLevel + 1 > (level.get(next) || 0)) level.set(next, curLevel + 1);
      const rem = (remainingIndegree.get(next) || 0) - 1;
      remainingIndegree.set(next, rem);
      if (rem <= 0) levelQueue.push(next);
    }
  }

  const levelGroups = new Map<number, string[]>();
  ticket.nodes.forEach(n => {
    const lvl = level.get(n.id) || 0;
    if (!levelGroups.has(lvl)) levelGroups.set(lvl, []);
    levelGroups.get(lvl)!.push(n.id);
  });
  const maxLevel = Math.max(0, ...Array.from(levelGroups.keys()));
  const maxPerLevel = Math.max(1, ...Array.from(levelGroups.values()).map(g => g.length));
  const hasParallelRows = maxPerLevel > 1;

  const colSpacing = 78;
  const rowSpacing = 52;
  const svgWidth = Math.max(300, maxPerLevel * colSpacing + 60);
  const svgHeight = Math.max(180, (maxLevel + 1) * rowSpacing + 46);

  const nodePos = new Map<string, { x: number; y: number }>();
  levelGroups.forEach((ids, lvl) => {
    const rowWidth = (ids.length - 1) * colSpacing;
    const startX = (svgWidth - rowWidth) / 2;
    ids.forEach((id, i) => {
      nodePos.set(id, { x: startX + i * colSpacing, y: 26 + lvl * rowSpacing });
    });
  });

  // The backend claims every review/review_gate node as IN REVIEW the moment
  // it starts running (there's no separate "reviewer is working" status --
  // see engine.go's GetExecutableNodes). For display we flip that: the
  // reviewer's own circle reads as IN PROGRESS (it's the one doing work),
  // while whichever node(s) it depends on -- the actual artifact under
  // review, otherwise stuck showing a stale DONE -- read as IN REVIEW
  // instead. This is purely a display transform; the underlying node.status
  // used for progress counts, prereq checks, etc. is untouched.
  const isReviewType = (type: string) => type === 'review' || type === 'review_gate';
  const reviewedNodeIds = new Set<string>();
  ticket.nodes.forEach(n => {
    if (isReviewType(n.type) && n.status === 'IN REVIEW') {
      ticket.edges.forEach(e => {
        if (e.to_node_id === n.id && e.condition !== 'iteration_loop') {
          reviewedNodeIds.add(e.from_node_id);
        }
      });
    }
  });
  // A reviewer that looped back is stored as AWAITING FIX by the engine
  // (DFLT-00042) and passes through unchanged, so no display-only guess
  // based on the loop target's iteration_count is needed any more.
  const getDisplayStatus = (n: { id: string; type: string; status: string }): string => {
    if (isReviewType(n.type) && n.status === 'IN REVIEW') return 'IN PROGRESS';
    if (n.status === 'DONE' && reviewedNodeIds.has(n.id)) return 'IN REVIEW';
    return n.status;
  };

  const ticketStatusMeta = getStatusMeta(ticket.status);

  const ticketLabels = ticket.labels ?? [];
  const headerLabels = ticketLabels.slice(0, MAX_HEADER_LABELS);
  const hiddenLabelCount = ticketLabels.length - headerLabels.length;

  return (
    <div ref={rootRef} id={`ticket-${ticket.id}`} tabIndex={-1} className="bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-800 rounded-xl shadow-xs transition-all overflow-clip mb-4">
      {/* Reject prompt closing (DFLT-00172). Always mounted -- outside the
          header row and the expandable panel, which each keep their own
          region -- so it exists before its text changes. */}
      <StatusLiveRegion message={approvalAnnouncement} />
      {/* The ticket-delete confirmation (portalled to document.body). Placed
          here rather than next to the delete button so that no event from
          the dialog bubbles (through the React tree) into the header row's
          mouse handlers. */}
      {deleteConfirmDialog}
      {/* Header Row. gap-4 keeps a fixed space between the left group and
          the right-hand group (DFLT-00141): justify-between alone leaves no
          space once a long title stretches the flex-1 left group all the way
          to the right, so its last item (a label chip, "+N", the rejected
          badge or the title) would touch the first item on the right (the
          assignee chip/button or the node progress). */}
      <div
        ref={headerRowRef}
        onMouseDown={handleHeaderMouseDown}
        onClick={handleHeaderClick}
        data-testid="ticket-header-row"
        className="p-4 flex items-center justify-between gap-4 cursor-pointer hover:bg-slate-50 dark:hover:bg-slate-800 transition select-none"
      >
        <div className="flex items-center gap-3 flex-1 min-w-0">
          {/* The chevron is the row's keyboard / assistive-technology entry
              point: a named button whose aria-expanded mirrors the row
              (DFLT-00152). It deliberately has no onClick of its own -- the
              toggle lives only in handleHeaderClick, which the chevron's
              click (mouse, or Enter/Space with detail 0) bubbles up to, so
              every activation toggles the row exactly once.
              DFLT-00178: it draws its own focus-visible ring (the same
              classes as the node-row chevron, DFLT-00175) instead of relying
              on the browser outline, and none on a mouse click. The ring has
              no offset, so it sits on this header's background: light
              blue-500 is 3.68:1 / 3.52:1 on white / the hovered slate-50,
              dark blue-400 is 7.02:1 / 5.75:1 on slate-900 / the hovered
              slate-800 -- all above the 3:1 non-text minimum. (blue-500
              would also pass in dark mode, 4.85:1 / 3.98:1, but blue-400
              keeps it consistent with the node-row chevron.) */}
          <button
            type="button"
            aria-expanded={isExpanded}
            aria-label={t('ticketItem.toggleTicket', { id: ticket.id })}
            data-testid="ticket-toggle-expand"
            className="text-slate-500 dark:text-slate-400 hover:text-slate-600 dark:hover:text-slate-300 shrink-0 rounded focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 dark:focus-visible:ring-blue-400"
          >
            {isExpanded
              ? <ChevronDown className="w-5 h-5" aria-hidden="true" />
              : <ChevronRight className="w-5 h-5" aria-hidden="true" />}
          </button>

          {/* The ID and the title are the only selectable text in the row
              (select-text over the row's select-none, DFLT-00143), so they
              can be dragged/double-clicked and copied without also picking
              up the chevron or the badges. handleHeaderClick keeps such a
              selection from toggling the row. */}
          <span
            data-testid="ticket-header-id"
            className="font-mono text-xs font-bold px-2 py-1 rounded bg-blue-50 dark:bg-blue-950 text-blue-700 dark:text-blue-300 border border-blue-200 dark:border-blue-800 shrink-0 whitespace-nowrap select-text cursor-text"
          >
            {ticket.id}
          </span>

          {/* Copies the ticket ID (DFLT-00143). The name includes the ID
              since every row has one; the always-mounted live region beside
              it announces the result (the one in the expanded panel is not
              rendered while the row is collapsed). */}
          <IconButton
            onClick={handleCopyId}
            label={copyIdLabel}
            data-testid="ticket-copy-id"
            wrapperClassName="-ml-1 shrink-0"
            className={`w-6 h-6 inline-flex items-center justify-center rounded transition ${
              copyState === 'copied'
                ? 'text-emerald-600 dark:text-emerald-400'
                : copyState === 'failed'
                  ? 'text-red-600 dark:text-red-400'
                  : 'text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 hover:bg-slate-100 dark:hover:bg-slate-700'
            }`}
          >
            {copyState === 'copied' ? (
              <Check className="w-3.5 h-3.5" aria-hidden="true" />
            ) : copyState === 'failed' ? (
              <X className="w-3.5 h-3.5" aria-hidden="true" />
            ) : (
              <Copy className="w-3.5 h-3.5" aria-hidden="true" />
            )}
          </IconButton>
          <StatusLiveRegion message={copyIdStatus} />

          {/* Label/colors shared with the node badge via statusMeta.ts
              (DFLT-00030). No `uppercase`/`tracking-wider`: English labels
              are uppercase in the translation data itself, and the extra
              letter-spacing looked broken on Japanese labels. */}
          <span className={`text-xs px-2.5 py-0.5 rounded-full font-bold shrink-0 whitespace-nowrap ${ticketStatusMeta.chip.bg} ${ticketStatusMeta.chip.text}`}>
            {t(ticketStatusMeta.labelKey)}
          </span>

          {/* Priority badge/selector (DFLT-00048, DFLT-00083): a
              one-character chip over a native <select> -- see
              PrioritySelect.tsx. Clicks must not toggle the row. */}
          <span className="shrink-0" onClick={e => e.stopPropagation()}>
            <PrioritySelect
              ticketId={ticket.id}
              priority={ticket.priority}
              disabled={prioritySaving}
              onChange={handleChangePriority}
            />
          </span>
          {priorityError && (
            <span className="text-red-600 dark:text-red-400 font-medium text-xs shrink-0" onClick={e => e.stopPropagation()}>
              {priorityError}
            </span>
          )}

          {/* min-w-0 (alongside truncate) is required for this flex item to
              actually shrink below its own text's natural width -- without
              it, a long title would instead push the id/status badges (and
              the right-hand action area) to wrap/overflow. This is the one
              element in the row meant to give up space first. */}
          <span
            data-testid="ticket-header-title"
            className="font-bold text-slate-900 dark:text-slate-100 text-sm truncate min-w-0 select-text cursor-text"
          >
            {ticket.title}
          </span>

          {/* Labels (DFLT-00084): at most MAX_HEADER_LABELS chips, the rest
              folded into "+N" whose title lists every label name. shrink-0
              keeps them intact; the title above is what truncates. */}
          {ticketLabels.length > 0 && (
            <span className="flex items-center gap-1 shrink-0" data-testid="ticket-header-labels">
              {headerLabels.map(l => (
                <LabelChip key={l.id} name={l.name} color={l.color} />
              ))}
              {hiddenLabelCount > 0 && (
                <span
                  data-testid="ticket-header-labels-more"
                  title={ticketLabels.map(l => l.name).join(', ')}
                  className="px-1.5 py-0.5 rounded-full bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-300 text-[11px] font-semibold"
                >
                  {/* The bare "+N" means nothing read aloud, and screen
                      readers don't reliably read title: they get the
                      folded label names as sr-only text instead. */}
                  <span aria-hidden="true">{t('ticket.labels.more', { count: hiddenLabelCount })}</span>
                  <span className="sr-only">
                    {t('ticket.labels.moreSr', {
                      count: hiddenLabelCount,
                      names: ticketLabels
                        .slice(MAX_HEADER_LABELS)
                        .map(l => l.name)
                        .join(', ')
                    })}
                  </span>
                </span>
              )}
            </span>
          )}

          {rejectedApprovalNodes.length > 0 && (
            <span
              className="flex items-center gap-1.5 pl-2 pr-2 py-0.5 rounded-full bg-red-50 dark:bg-red-950 border border-red-200 dark:border-red-800 text-red-700 dark:text-red-300 text-[11px] font-bold shrink-0"
              onClick={e => e.stopPropagation()}
              title={t('ticketItem.approvalGate.rejectedHint')}
            >
              <X aria-hidden="true" className="w-3 h-3 shrink-0" />
              <span className="truncate max-w-[12rem]">
                {rejectedApprovalNodes.length === 1
                  ? t('ticketItem.approvalGate.rejectedBadgeOne', { name: rejectedApprovalNodes[0].name })
                  : t('ticketItem.approvalGate.rejectedBadgeMany', { count: rejectedApprovalNodes.length })}
              </span>
            </span>
          )}

          {/* Autopilot (DFLT-00142): running / processing / waiting for a
              person / not reached yet. In the header row so the list shows
              it too, not only the expanded detail. */}
          <AutopilotBadges view={autopilot} />
        </div>

        {/* Right Info: Self-assign chip, Progress Pill. shrink-0 keeps this whole
            group (and everything inside it) at its natural width -- the
            ticket title above is the only thing that gives up space when
            the row is too narrow. */}
        <div className="flex items-center gap-4 text-xs shrink-0">
          {/* Assignee chip. The read-only "someone else has this" chip must
              stay visible regardless of whether the viewer has configured a
              "アプリ設定" myName -- it conveys who has the ticket, which has
              nothing to do with the viewer's own identity (completion
              criterion 5: any viewer must be able to tell who a ticket's
              assignee is, not only ones who've set myName). Only the
              *action* buttons (assign to me / unassign), which do depend on
              knowing who "me" is, stay gated behind myName. So the whole
              block renders whenever there is something to show: either an
              existing assignee (chip, any viewer) or a configured myName
              (assign button on an unassigned ticket). */}
          {(ticket.assignee || myName) && (
            <span className="flex items-center gap-1.5" onClick={e => e.stopPropagation()}>
              {myName && isAssignedToMe ? (
                <span className="inline-flex items-center gap-1 pl-2 pr-1 py-0.5 rounded-full bg-indigo-50 dark:bg-indigo-950 text-indigo-700 dark:text-indigo-300 border border-indigo-200 dark:border-indigo-800 text-[11px] font-semibold">
                  {ticket.assignee}
                  <IconButton
                    onClick={handleToggleAssignedToMe}
                    disabled={assignToMeSaving}
                    label={t('ticketItem.selfAssign.unassign')}
                    className="p-0.5 text-indigo-400 hover:text-indigo-700 dark:hover:text-indigo-200 disabled:opacity-50 rounded-full"
                  >
                    {assignToMeSaving ? <Loader2 aria-hidden="true" className="w-3 h-3 animate-spin" /> : <X aria-hidden="true" className="w-3 h-3" />}
                  </IconButton>
                </span>
              ) : ticket.assignee ? (
                // Someone else already has this ticket (or the viewer hasn't
                // configured myName yet, so we can't tell if it's "them") --
                // shown read-only so it can never be mistaken for (or, by
                // clicking, silently taken over from) the viewer's own
                // assignment. Deliberately not gated on myName: this is the
                // one piece of assignee UI every viewer needs to see, with
                // or without their own name configured.
                <span
                  className="inline-flex items-center gap-1 pl-2 pr-2 py-0.5 rounded-full bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-300 border border-slate-200 dark:border-slate-700 text-[11px] font-semibold"
                  title={t('ticketItem.selfAssign.assignedToOther', { name: ticket.assignee })}
                >
                  {ticket.assignee}
                </span>
              ) : myName ? (
                <button
                  type="button"
                  onClick={handleToggleAssignedToMe}
                  disabled={assignToMeSaving}
                  className="px-2 py-0.5 rounded-full border border-slate-300 dark:border-slate-700 text-slate-500 dark:text-slate-400 hover:text-indigo-600 dark:hover:text-indigo-400 hover:border-indigo-300 dark:hover:border-indigo-700 disabled:opacity-50 text-[11px] font-semibold flex items-center gap-1 transition"
                >
                  {assignToMeSaving ? <Loader2 aria-hidden="true" className="w-3 h-3 animate-spin" /> : <UserPlus aria-hidden="true" className="w-3 h-3" />}
                  {t('ticketItem.selfAssign.assign')}
                </button>
              ) : null}
              {assignToMeError && (
                <span className="text-red-600 dark:text-red-400 font-medium">{assignToMeError}</span>
              )}
            </span>
          )}

          {totalNodes > 0 && (
            <div className="flex items-center gap-2">
              <div className="flex gap-0.5">
                {ticket.nodes.map(n => {
                  const displayStatus = getDisplayStatus(n);
                  const isPendingApproval = pendingApprovalNodeIds.has(n.id);
                  return (
                    <div
                      key={n.id}
                      title={isPendingApproval ? `${n.name} (${t('ticketItem.approvalGate.pendingStatus')})` : `${n.name} (${t(getStatusMeta(displayStatus).labelKey)})`}
                      className={`w-2.5 h-3.5 rounded-xs ${
                        isPendingApproval
                          ? 'bg-amber-400 animate-pulse'
                          : displayStatus === 'DONE'
                          ? 'bg-emerald-500'
                          : displayStatus === 'IN PROGRESS'
                          ? 'bg-blue-500 animate-pulse'
                          : displayStatus === 'IN REVIEW'
                          ? 'bg-purple-500'
                          : displayStatus === 'AWAITING FIX'
                          ? 'bg-orange-500'
                          : 'bg-slate-200 dark:bg-slate-700'
                      }`}
                    />
                  );
                })}
              </div>
              <span className="font-mono text-slate-600 dark:text-slate-400 font-semibold">
                {doneNodes}/{totalNodes}
              </span>
            </div>
          )}

          {closeError && (
            <span className="text-red-600 dark:text-red-400 font-medium" onClick={e => e.stopPropagation()}>
              {closeError}
            </span>
          )}
          {reopenError && (
            <span className="text-red-600 dark:text-red-400 font-medium" onClick={e => e.stopPropagation()}>
              {reopenError}
            </span>
          )}

          {/* Close/reopen (DFLT-00043): a CLOSED ticket shows the reopen
              button only, everything else shows the close button (which
              opens the optional-reason prompt below, rendered outside this
              clickable header row). */}
          {ticket.status === 'CLOSED' ? (
            <IconButton
              onClick={handleReopenTicket}
              disabled={isReopeningTicket}
              label={t('ticketItem.reopen.button')}
              wrapperClassName="-m-1"
              className="text-slate-500 dark:text-slate-400 hover:text-indigo-600 dark:hover:text-indigo-400 disabled:opacity-50 disabled:cursor-not-allowed transition p-1 rounded"
            >
              {isReopeningTicket ? <Loader2 aria-hidden="true" className="w-4 h-4 animate-spin" /> : <RotateCcw aria-hidden="true" className="w-4 h-4" />}
            </IconButton>
          ) : (
            <IconButton
              onClick={e => {
                e.stopPropagation();
                setIsClosePromptOpen(v => !v);
              }}
              label={t('ticketItem.close.button')}
              wrapperClassName="-m-1"
              className="text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 transition p-1 rounded"
            >
              <Archive aria-hidden="true" className="w-4 h-4" />
            </IconButton>
          )}

          {deleteError && (
            <span className="text-red-600 dark:text-red-400 font-medium" onClick={e => e.stopPropagation()}>
              {deleteError}
            </span>
          )}

          <IconButton
            ref={deleteButtonRef}
            data-focus-key={`ticket-delete-${ticket.id}`}
            onClick={handleDeleteTicket}
            disabled={isDeletingTicket}
            label={t('ticketItem.delete.ariaLabel', { id: ticket.id, title: ticket.title })}
            tooltip={t('ticketItem.delete.button')}
            wrapperClassName="-m-1"
            className="text-slate-500 dark:text-slate-400 hover:text-red-600 dark:hover:text-red-400 disabled:opacity-50 disabled:cursor-not-allowed transition p-1 rounded"
          >
            {isDeletingTicket ? <Loader2 aria-hidden="true" className="w-4 h-4 animate-spin" /> : <Trash2 aria-hidden="true" className="w-4 h-4" />}
          </IconButton>
        </div>
      </div>

      {/* Close-with-reason prompt (DFLT-00043). Reason is optional -- closing
          works from any ticket status, with or without an explanation -- so
          the confirm button is never disabled by empty text (unlike the
          approval_gate reject prompt below). Kept outside the clickable
          header row, as a sibling, so it stays visible whether or not the
          ticket is expanded (matching where the close button itself lives). */}
      {isClosePromptOpen && (
        <div
          className="px-4 pb-3 pt-1 border-t border-slate-200 dark:border-slate-800 flex items-center gap-2"
          onClick={e => e.stopPropagation()}
        >
          <input
            type="text"
            autoFocus
            value={closeReasonDraft}
            onChange={e => setCloseReasonDraft(e.target.value)}
            placeholder={t('ticketItem.close.reasonPlaceholder')}
            className="flex-1 text-xs border border-slate-300 dark:border-slate-700 rounded px-2 py-1 bg-white dark:bg-slate-900 text-slate-900 dark:text-slate-100 focus:outline-none focus:ring-1 focus:ring-indigo-400"
          />
          <button
            type="button"
            onClick={handleCloseTicket}
            disabled={isClosingTicket}
            className="px-2 py-1 bg-slate-700 hover:bg-slate-600 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded text-xs font-bold flex items-center gap-1 transition shrink-0"
          >
            {isClosingTicket ? <Loader2 aria-hidden="true" className="w-3.5 h-3.5 animate-spin" /> : <Archive aria-hidden="true" className="w-3.5 h-3.5" />}
            {t('ticketItem.close.confirm')}
          </button>
          <button
            type="button"
            onClick={() => {
              setIsClosePromptOpen(false);
              setCloseReasonDraft('');
            }}
            disabled={isClosingTicket}
            className="px-2 py-1 text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-200 disabled:opacity-50 text-xs font-semibold shrink-0"
          >
            {t('ticketItem.close.cancel')}
          </button>
        </div>
      )}

      {/* Expanded Ticket Details */}
      {isExpanded && (
        <div className="border-t border-slate-200 dark:border-slate-800 bg-slate-50/50 dark:bg-slate-800/30 p-6 space-y-6">
          {/* Metadata Bar */}
          <div className="flex flex-wrap items-center gap-y-2 gap-x-6 text-xs text-slate-600 dark:text-slate-400 border-b border-slate-200 dark:border-slate-800 pb-3">
            <div>
              {t('ticketItem.nodeCount')}: <span className="font-semibold text-slate-800 dark:text-slate-200">{totalNodes}</span>
            </div>
            {loopEdges.length > 0 && (
              <div className="text-amber-600 dark:text-amber-400 font-medium">
                {t('ticketItem.loopEdges', { count: loopEdges.length })}
              </div>
            )}
            <div>
              {t('ticketItem.createdAt')}: <span className="font-mono text-slate-700 dark:text-slate-300">{formatDateTime(ticket.created_at, i18n.language)}</span>
            </div>
            {/* Labels (DFLT-00084): every label, plus the picker. */}
            <div className="flex flex-wrap items-center gap-1.5" data-testid="ticket-detail-labels">
              <span>{t('ticket.labels.title')}:</span>
              {ticketLabels.length === 0 ? (
                <span className="text-slate-500 dark:text-slate-400">{t('ticket.labels.none')}</span>
              ) : (
                ticketLabels.map(l => <LabelChip key={l.id} name={l.name} color={l.color} />)
              )}
              <LabelSelect ticketId={ticket.id} labels={ticketLabels} projectLabels={projectLabels} onSaved={onRefresh} />
            </div>
            {ticket.closed_reason && (
              <div className="flex items-center gap-1 text-slate-700 dark:text-slate-300">
                <Archive aria-hidden="true" className="w-3.5 h-3.5 text-slate-500 dark:text-slate-400" />
                {t('ticketItem.close.reasonLabel')}: <span className="font-medium">{ticket.closed_reason}</span>
              </div>
            )}
          </div>

          {/* Parent and children (DFLT-00142); nothing when there are none. */}
          <TicketFamily parent={ticket.parent} childTickets={ticket.children} onOpenTicket={onOpenTicket} />

          {/* What the autopilot decided instead of a person, and its tree
              summary (DFLT-00142); nothing when it never ran here. */}
          <AutopilotDecisions artifacts={ticket.artifacts} nodes={ticket.nodes} />

          {/* Description Card -- always visible (not tabbed) so the ticket's
              description has a permanent place to be checked. */}
          <div className="bg-white dark:bg-slate-900 p-4 rounded-xl border border-slate-200 dark:border-slate-800 shadow-xs">
            <div className="flex items-center justify-between mb-2">
              <span className="text-xs font-bold text-slate-700 dark:text-slate-300 flex items-center gap-1.5">
                <FileText aria-hidden="true" className="w-3.5 h-3.5 text-indigo-500" />
                {t('ticketItem.description.title')}
              </span>
              <div className="flex items-center gap-3">
                {ticket.refined_at && (
                  <span className="text-[10px] text-slate-500 dark:text-slate-400 flex items-center gap-1">
                    <History aria-hidden="true" className="w-3 h-3" />
                    {t('ticketItem.description.refinedAt', { time: formatDateTime(ticket.refined_at, i18n.language) })}
                  </span>
                )}
                {description.length > 0 && (
                  <button
                    type="button"
                    onClick={() => setIsDescriptionExpanded(v => !v)}
                    className="text-[11px] text-indigo-600 dark:text-indigo-400 hover:underline font-semibold"
                  >
                    {isDescriptionExpanded ? t('ticketItem.description.collapse') : t('ticketItem.description.expand')}
                  </button>
                )}
              </div>
            </div>

            {description.length === 0 ? (
              <div className="text-slate-500 dark:text-slate-400 italic text-xs">{t('ticketItem.description.empty')}</div>
            ) : (
              <div className={isDescriptionExpanded ? '' : 'max-h-56 overflow-y-auto'}>
                <MarkdownViewer content={description} />
              </div>
            )}
          </div>

          {/* Graph + Nodes List Split View. items-start (rather than the
              grid default of stretch) is deliberate -- the two columns'
              heights are synced explicitly via nodeListCardHeight/
              graphPanelRef above, not by letting grid stretch the shorter
              one to match the row's auto-computed height (which would also
              let an unusually long node list balloon the graph panel). */}
          <div className="grid grid-cols-1 lg:grid-cols-12 gap-6 items-start">
            {/* Left Graph Diagram (SVG) - Return edges routed on the LEFT.
                min-h-[32rem] is a floor (the default height reserved when
                there's no execution graph yet), never a cap -- this panel
                always renders its full content with no scrollbar. When it
                grows past that floor, nodeListCardHeight (measured via
                ResizeObserver above) carries the same height over to the
                node/artifact panel on the right. */}
            <div ref={graphPanelRef} className="lg:col-span-4 bg-white dark:bg-slate-900 p-4 rounded-xl border border-slate-200 dark:border-slate-800 flex flex-col min-h-[32rem]">
              <div className="text-xs font-bold text-slate-500 dark:text-slate-400 mb-2 w-full text-left flex items-center justify-between shrink-0">
                <span>{t('ticketItem.graphTitle')}</span>
                <span className="text-[10px] text-indigo-600 dark:text-indigo-400 font-semibold">{t('ticketItem.progress', { percent: progressPercent })}</span>
              </div>
              <div className="flex-1 flex flex-col items-center justify-center">
              <svg className="w-full max-w-[340px] shrink-0" height={svgHeight} viewBox={`0 0 ${svgWidth} ${svgHeight}`}>
                {/* 1. Forward edges -- straight lines between each node's actual
                    (level, column) position, so a fan-out to several nodes on
                    the same row reads as a fork instead of a straight line down. */}
                {ticket.edges.map(e => {
                  const from = nodePos.get(e.from_node_id);
                  const to = nodePos.get(e.to_node_id);
                  if (!from || !to) return null;

                  const isLoop = e.condition === 'iteration_loop';
                  if (isLoop) return null; // Drawn separately on the left below

                  return (
                    <line
                      key={e.id}
                      x1={from.x}
                      y1={from.y}
                      x2={to.x}
                      y2={to.y}
                      className="stroke-slate-300 dark:stroke-slate-600"
                      strokeWidth="2"
                    />
                  );
                })}

                {/* 2. Return loop edges (routed to the LEFT of whichever of the
                    two nodes is further left, so they never cross a parallel
                    sibling in between) */}
                {ticket.edges.map(e => {
                  const from = nodePos.get(e.from_node_id);
                  const to = nodePos.get(e.to_node_id);
                  if (!from || !to) return null;

                  const isLoop = e.condition === 'iteration_loop';
                  if (!isLoop) return null;

                  const curveX = Math.max(12, Math.min(from.x, to.x) - 24);

                  return (
                    <g key={e.id}>
                      <path
                        d={`M ${from.x - 6} ${from.y} C ${curveX} ${from.y}, ${curveX} ${to.y}, ${to.x - 6} ${to.y}`}
                        fill="none"
                        stroke="#f59e0b"
                        strokeWidth="2"
                        strokeDasharray="4,4"
                      />
                      {/* Arrowhead pointing to target node */}
                      <polygon
                        points={`${to.x - 6},${to.y} ${to.x - 12},${to.y - 3} ${to.x - 12},${to.y + 3}`}
                        fill="#f59e0b"
                      />
                    </g>
                  );
                })}

                {/* 3. Nodes and labels -- labels sit below each node (centered)
                    rather than to the right, since a row can hold several
                    nodes side by side. */}
                {ticket.nodes.map(n => {
                  const pos = nodePos.get(n.id);
                  if (!pos) return null;
                  const displayStatus = getDisplayStatus(n);
                  const isDone = displayStatus === 'DONE';
                  const isInProgress = displayStatus === 'IN PROGRESS';
                  const isInReview = displayStatus === 'IN REVIEW';

                  let fill = '#94a3b8';
                  if (isDone) fill = '#10b981';
                  else if (isInProgress) fill = '#3b82f6';
                  else if (isInReview) fill = '#a855f7';
                  // REJECTED (DFLT-00016, approval_gate only): distinct from
                  // the default TODO gray, since it's not merely unstarted.
                  else if (displayStatus === 'REJECTED') fill = '#ef4444';
                  // AWAITING FIX (DFLT-00042): orange-500, same hue as its badge.
                  else if (displayStatus === 'AWAITING FIX') fill = '#f97316';

                  // A halo ring drawn around the status circle marks gate
                  // node types. Every review-performing node is a gate --
                  // there's no such thing as a review node that can't be
                  // sent back -- so both 'review' and 'review_gate' get the
                  // same purple ring (matching the IN REVIEW purple, since
                  // it's conceptually a review step). approval_gate gets its
                  // own pink so the two gate kinds stay visually distinct.
                  // Plain nodes get no ring.
                  const gateRingColor =
                    n.type === 'approval_gate' ? '#ec4899' :
                    isReviewType(n.type) ? '#a855f7' :
                    null;
                  const outerR = isInProgress ? 8 : 6;

                  return (
                    <g key={n.id}>
                      {gateRingColor && (
                        <circle
                          cx={pos.x}
                          cy={pos.y}
                          r={outerR + 3}
                          fill="none"
                          stroke={gateRingColor}
                          strokeWidth="1.5"
                        />
                      )}
                      <circle
                        cx={pos.x}
                        cy={pos.y}
                        r={outerR}
                        fill={fill}
                        className={isInProgress ? 'animate-ping opacity-75' : ''}
                        style={isInProgress ? { transformBox: 'fill-box', transformOrigin: 'center' } : undefined}
                      />
                      <circle
                        cx={pos.x}
                        cy={pos.y}
                        r="6"
                        fill={fill}
                        stroke="#ffffff"
                        strokeWidth="2"
                      />
                      <text
                        x={pos.x}
                        y={pos.y + 18}
                        fontSize="9"
                        fontWeight="600"
                        textAnchor="middle"
                        className="select-none fill-slate-700 dark:fill-slate-300"
                      >
                        {n.name.length > 12 ? n.name.slice(0, 12) + '…' : n.name}
                      </text>
                    </g>
                  );
                })}
              </svg>
              {hasParallelRows && (
                <div className="w-full mt-2 text-[10px] text-indigo-600 dark:text-indigo-400 font-semibold flex items-center gap-1 shrink-0">
                  <Layers aria-hidden="true" className="w-3 h-3" />
                  {t('ticketItem.parallelHint')}
                </div>
              )}
              </div>
              <div className="w-full mt-3 pt-2 border-t border-slate-100 dark:border-slate-800 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-slate-500 dark:text-slate-400 shrink-0">
                <span className="flex items-center gap-1"><span className="w-2 h-2 rounded-full bg-emerald-500" /> {t('ticketItem.legend.done')}</span>
                <span className="flex items-center gap-1"><span className="w-2 h-2 rounded-full bg-blue-500" /> {t('ticketItem.legend.inProgress')}</span>
                <span className="flex items-center gap-1"><span className="w-2 h-2 rounded-full bg-purple-500" /> {t('ticketItem.legend.review')}</span>
                <span className="flex items-center gap-1"><span className="w-2 h-2 border-dashed border-2 border-amber-500" /> {t('ticketItem.legend.loopBack')}</span>
                <span className="flex items-center gap-1"><span className="w-2 h-2 rounded-full border-[1.5px] border-purple-500" /> {t('ticketItem.legend.reviewGate')}</span>
                <span className="flex items-center gap-1"><span className="w-2 h-2 rounded-full border-[1.5px] border-pink-500" /> {t('ticketItem.legend.approvalGate')}</span>
                <span className="flex items-center gap-1"><span className="w-2 h-2 rounded-full bg-orange-500" /> {t('ticketItem.legend.awaitingFix')}</span>
                <span className="flex items-center gap-1"><span className="w-2 h-2 rounded-full bg-red-500" /> {t('ticketItem.legend.rejected')}</span>
              </div>
            </div>

            {/* Right Node & Artifact Detail Tabs. Height is pinned to the
                graph panel's own rendered height (nodeListCardHeight, kept
                in sync by the ResizeObserver above) so it never exceeds a
                default floor of min-h-[32rem] unless the graph itself is
                taller -- this panel's content can still scroll internally
                within that height (flex-1 min-h-0 overflow-y-auto below);
                only the graph on the left must never scroll. */}
            <div
              className="lg:col-span-8 flex flex-col bg-white dark:bg-slate-900 rounded-xl border border-slate-200 dark:border-slate-800 overflow-hidden shadow-xs lg:sticky lg:top-20 min-h-[32rem]"
              style={nodeListCardHeight ? { height: nodeListCardHeight } : undefined}
            >
              {/* Tab Navigation */}
              <div className="flex items-center justify-between border-b border-slate-200 dark:border-slate-800 px-4 bg-slate-50 dark:bg-slate-800 shrink-0">
              <div className="flex">
                <button
                  onClick={() => setActiveTab('nodes')}
                  className={`py-3 px-4 text-xs font-bold border-b-2 flex items-center gap-2 transition ${
                    activeTab === 'nodes'
                      ? 'border-indigo-600 text-indigo-600 dark:text-indigo-400'
                      : 'border-transparent text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200'
                  }`}
                >
                  <GitBranch aria-hidden="true" className="w-4 h-4" />
                  {t('ticketItem.tabs.nodes', { count: totalNodes })}
                </button>
                <button
                  onClick={() => setActiveTab('gherkin')}
                  className={`py-3 px-4 text-xs font-bold border-b-2 flex items-center gap-2 transition ${
                    activeTab === 'gherkin'
                      ? 'border-indigo-600 text-indigo-600 dark:text-indigo-400'
                      : 'border-transparent text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200'
                  }`}
                >
                  <FileCode aria-hidden="true" className="w-4 h-4 text-amber-500" />
                  {t('ticketItem.tabs.gherkin', { count: gherkinArtifacts.length })}
                </button>
                <button
                  onClick={() => setActiveTab('html')}
                  className={`py-3 px-4 text-xs font-bold border-b-2 flex items-center gap-2 transition ${
                    activeTab === 'html'
                      ? 'border-indigo-600 text-indigo-600 dark:text-indigo-400'
                      : 'border-transparent text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200'
                  }`}
                >
                  <Globe aria-hidden="true" className="w-4 h-4 text-cyan-500" />
                  {t('ticketItem.tabs.html', { count: htmlArtifacts.length })}
                </button>
                <button
                  onClick={() => setActiveTab('artifacts')}
                  className={`py-3 px-4 text-xs font-bold border-b-2 flex items-center gap-2 transition ${
                    activeTab === 'artifacts'
                      ? 'border-indigo-600 text-indigo-600 dark:text-indigo-400'
                      : 'border-transparent text-slate-500 dark:text-slate-400 hover:text-slate-800 dark:hover:text-slate-200'
                  }`}
                >
                  <FileText aria-hidden="true" className="w-4 h-4 text-emerald-500" />
                  {t('ticketItem.tabs.artifacts', { count: ticket.artifacts.length })}
                </button>
              </div>
              {ticket.artifacts.length > 0 && (
                <a
                  href={`/api/tickets/${ticket.id}/artifacts/download`}
                  className="shrink-0 flex items-center gap-1.5 px-3 py-1.5 text-xs font-semibold text-slate-600 dark:text-slate-300 hover:text-indigo-600 dark:hover:text-indigo-400 border border-slate-300 dark:border-slate-600 rounded-lg hover:border-indigo-400 dark:hover:border-indigo-500 transition"
                  title={t('ticketItem.downloadAllArtifacts')}
                >
                  <Download aria-hidden="true" className="w-3.5 h-3.5" />
                  {t('ticketItem.downloadAllArtifacts')}
                </a>
              )}
              </div>

              {/* Tab Contents */}
              <div className="p-4 flex-1 min-h-0 overflow-y-auto">
                {/* 1. Nodes with Expandable Artifacts */}
                {activeTab === 'nodes' && (
                  <div className="space-y-2">
                    {ticket.nodes.map((node, index) => {
                      const nodeArtifacts = ticket.artifacts.filter(a => a.node_id === node.id);
                      const isNodeExpanded = expandedNodeIds.has(node.id);
                      const isApprovalSubmitting = submittingApprovalNodeIds.has(node.id);

                      return (
                        <div
                          key={node.id}
                          className="rounded-lg bg-slate-50 dark:bg-slate-800 border border-slate-200 dark:border-slate-700 overflow-hidden text-xs transition"
                        >
                          <div
                            onClick={() => toggleNodeExpand(node.id)}
                            className="flex items-center justify-between p-3 cursor-pointer hover:bg-slate-100 dark:hover:bg-slate-700 select-none"
                          >
                            {/* flex-1 min-w-0 lets node.name (below) shrink
                                and truncate first -- everything else in this
                                row is shrink-0 so the id/type/retry/manual/
                                artifact badges never wrap. */}
                            <div className="flex items-center gap-2.5 flex-1 min-w-0">
                              {/* Named toggle for the node row (DFLT-00152).
                                  No onClick of its own: its click bubbles to
                                  the row's toggleNodeExpand, so it toggles
                                  exactly once.
                                  DFLT-00175: focus returns here after an
                                  approval gate's reject prompt closes
                                  (DFLT-00172), so it draws its own
                                  focus-visible ring instead of relying on the
                                  browser outline, and none on a mouse click.
                                  Dark mode uses blue-400: blue-500 is only
                                  2.82:1 on the hovered slate-700 row, while
                                  blue-400 keeps 4.07:1 (light blue-500:
                                  3.52:1 / 3.36:1 on slate-50 / slate-100). */}
                              <button
                                type="button"
                                aria-expanded={isNodeExpanded}
                                aria-label={t('ticketItem.toggleNode', { id: node.id })}
                                data-testid={`node-toggle-expand-${node.id}`}
                                className="text-slate-500 dark:text-slate-400 shrink-0 rounded focus:outline-none focus-visible:ring-2 focus-visible:ring-blue-500 dark:focus-visible:ring-blue-400"
                              >
                                {isNodeExpanded
                                  ? <ChevronDown className="w-4 h-4" aria-hidden="true" />
                                  : <ChevronRight className="w-4 h-4" aria-hidden="true" />}
                              </button>
                              {/* DFLT-00162: this row's hover background is
                                  slate-100 / slate-700, where slate-500 /
                                  slate-400 text drops to 4.34:1 / 4.04:1, so
                                  the sequence number (and the update time
                                  below) use slate-600 / slate-300 to keep
                                  WCAG 1.4.3's 4.5:1 in both states. The node
                                  id uses the same pair for the same reason
                                  (DFLT-00164). */}
                              <span className="font-mono text-slate-600 dark:text-slate-300 w-4 shrink-0">{index + 1}</span>
                              <span className="font-mono font-bold text-slate-600 dark:text-slate-300 shrink-0 whitespace-nowrap">
                                {node.id}
                              </span>
                              <NodeTypeBadge type={node.type} theme="light" className="shrink-0" />
                              <span className="font-semibold text-slate-800 dark:text-slate-200 truncate min-w-0">
                                {node.name}
                              </span>
                              {node.iteration_count > 0 && (
                                <span className="text-amber-600 dark:text-amber-400 font-mono text-[11px] shrink-0 whitespace-nowrap">
                                  {t('ticketItem.retryCount', { count: node.iteration_count })}
                                </span>
                              )}
                              {/* approval_gate already identifies itself as a
                                  human-only node via its type badge above, so
                                  this generic is_manual badge is reserved for
                                  other manual node types (e.g. release) to
                                  avoid showing two overlapping badges. */}
                              {node.is_manual && node.type !== 'approval_gate' && (
                                <span className="text-pink-600 dark:text-pink-400 font-bold text-[10px] px-1.5 py-0.2 border border-pink-300 dark:border-pink-800 bg-pink-50 dark:bg-pink-950 rounded shrink-0 whitespace-nowrap">
                                  {t('ticketItem.manualApproval')}
                                </span>
                              )}
                              {nodeArtifacts.length > 0 && (
                                <span className="text-[10px] px-1.5 py-0.5 rounded-full bg-indigo-50 dark:bg-indigo-950 text-indigo-700 dark:text-indigo-300 font-semibold border border-indigo-200 dark:border-indigo-800 flex items-center gap-1 shrink-0 whitespace-nowrap">
                                  <Layers aria-hidden="true" className="w-3 h-3" />
                                  {t('ticketItem.artifactsCount', { count: nodeArtifacts.length })}
                                </span>
                              )}
                            </div>

                            <div className="flex items-center gap-3 shrink-0">
                              {/* Approve/Reject show up for whichever
                                  approval_gate the ticket is actually
                                  stuck on (pendingApprovalNodeIds), even
                                  without expanding the node's row -- see
                                  DFLT ticket for blinking-gate fix. */}
                              {pendingApprovalNodeIds.has(node.id) && rejectingNodeId !== node.id && (
                                <div className="flex items-center gap-1.5" onClick={e => e.stopPropagation()}>
                                  <button
                                    type="button"
                                    onClick={() => handleApprovalDecision(node.id, true)}
                                    disabled={isApprovalSubmitting}
                                    aria-busy={isApprovalSubmitting || undefined}
                                    data-testid={`node-approve-${node.id}`}
                                    className="px-2 py-1 bg-emerald-600 hover:bg-emerald-500 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded text-[11px] font-bold flex items-center gap-1 transition"
                                  >
                                    {isApprovalSubmitting ? (
                                      <Loader2 aria-hidden="true" className="w-3 h-3 animate-spin" />
                                    ) : (
                                      <Check aria-hidden="true" className="w-3 h-3" />
                                    )}
                                    {t('ticketItem.approvalGate.approve')}
                                    {/* DFLT-00176: "submitting" in the accessible name (see RejectReasonPrompt). */}
                                    {isApprovalSubmitting && <span className="sr-only">{t('ticketItem.approvalGate.submitting')}</span>}
                                  </button>
                                  <button
                                    type="button"
                                    onClick={() => startRejecting(node.id)}
                                    disabled={isApprovalSubmitting}
                                    aria-busy={isApprovalSubmitting || undefined}
                                    data-testid={`node-reject-${node.id}`}
                                    className="px-2 py-1 bg-white dark:bg-slate-900 hover:bg-red-50 dark:hover:bg-red-950 disabled:opacity-50 disabled:cursor-not-allowed text-red-600 dark:text-red-400 border border-red-300 dark:border-red-800 rounded text-[11px] font-bold flex items-center gap-1 transition"
                                  >
                                    <X aria-hidden="true" className="w-3 h-3" />
                                    {t('ticketItem.approvalGate.reject')}
                                    {isApprovalSubmitting && <span className="sr-only">{t('ticketItem.approvalGate.submitting')}</span>}
                                  </button>
                                </div>
                              )}
                              {getNodeBadge(getDisplayStatus(node))}
                              {/* slate-600 / slate-300 for the hover background (DFLT-00162, see the sequence number above). */}
                              <span className="text-[11px] text-slate-600 dark:text-slate-300 font-mono">
                                {formatTime(node.updated_at, i18n.language)}
                              </span>
                            </div>
                          </div>

                          {/* Reject-with-reason prompt (DFLT-00016). A free-
                              text reason is required -- the confirm button
                              stays disabled until it's non-empty, which is
                              this flow's confirmation step (no window.confirm
                              dialog). Kept outside the clickable header row
                              so typing/clicking here doesn't toggle the
                              artifacts accordion. Only while the gate is
                              still pending, so a ticket that turns CLOSED
                              (or a gate judged elsewhere) mid-edit doesn't
                              keep offering a reject that must fail
                              (DFLT-00157). */}
                          {rejectingNodeId === node.id && pendingApprovalNodeIds.has(node.id) && (
                            <RejectReasonPrompt
                              nodeId={node.id}
                              draft={rejectReasonDraft}
                              onDraftChange={setRejectReasonDraft}
                              onConfirm={() => handleApprovalDecision(node.id, false, rejectReasonDraft)}
                              onCancel={cancelRejecting}
                              isSubmitting={isApprovalSubmitting}
                              onMount={handlePromptMount}
                              onUnmount={handlePromptUnmount}
                            />
                          )}

                          {/* approval_gate approve/reject error (kept outside
                              the clickable header row so it doesn't toggle
                              the artifacts accordion when clicked/read). */}
                          {approvalErrors[node.id] && (
                            <div className="px-3 pb-2 -mt-1 text-[11px] text-red-600 dark:text-red-400 font-medium">
                              {approvalErrors[node.id]}
                            </div>
                          )}

                          {/* Node artifacts accordion body */}
                          {isNodeExpanded && (
                            <div className="border-t border-slate-200 dark:border-slate-700 bg-white dark:bg-slate-900 p-3 space-y-3">
                              {nodeArtifacts.length === 0 ? (
                                <div className="text-slate-500 dark:text-slate-400 italic text-[11px]">
                                  {t('ticketItem.noArtifactsForNode')}
                                </div>
                              ) : (
                                nodeArtifacts.map(art => (
                                  <div key={art.id} className="p-2.5 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800 space-y-2">
                                    <div className="flex items-center justify-between font-bold text-slate-700 dark:text-slate-300 text-xs">
                                      <span className="flex items-center gap-1.5">
                                        {art.type === 'gherkin' && <FileCode aria-hidden="true" className="w-3.5 h-3.5 text-amber-500" />}
                                        {art.type === 'html' && <Globe aria-hidden="true" className="w-3.5 h-3.5 text-cyan-500" />}
                                        {art.type === 'text' && <FileText aria-hidden="true" className="w-3.5 h-3.5 text-indigo-500" />}
                                        {art.name}
                                      </span>
                                      <span className="flex items-center gap-2">
                                        {downloadLink(art)}
                                        <span className="text-[10px] uppercase font-mono px-1.5 py-0.5 bg-slate-200 dark:bg-slate-700 text-slate-600 dark:text-slate-300 rounded">
                                          {art.type}
                                        </span>
                                      </span>
                                    </div>

                                    {/* Inline display based on type */}
                                    {art.type === 'gherkin' && art.content && (
                                      <div className="space-y-1">
                                        <div className="flex justify-end">{openInNewTabLink(art)}</div>
                                        <GherkinViewer
                                          content={art.content}
                                          scrollable
                                          label={artifactScrollLabel(art)}
                                        />
                                      </div>
                                    )}

                                    {art.type === 'html' && (
                                      <div className="space-y-1">
                                        <div className="flex justify-end">{openInNewTabLink(art)}</div>
                                        <iframe
                                          src={(art.content || art.file_path || art.has_content) ? `/api/artifacts/${art.id}/content` : undefined}
                                          title={art.name}
                                          sandbox="allow-scripts"
                                          className="w-full h-48 bg-white rounded border border-slate-300 dark:border-slate-600"
                                        />
                                      </div>
                                    )}

                                    {art.type === 'text' && art.content && (
                                      <div className="space-y-1">
                                        <div className="flex justify-end">{openInNewTabLink(art)}</div>
                                        <MarkdownViewer
                                          content={art.content}
                                          scrollable
                                          label={artifactScrollLabel(art)}
                                        />
                                      </div>
                                    )}
                                  </div>
                                ))
                              )}
                            </div>
                          )}
                        </div>
                      );
                    })}
                  </div>
                )}

                {/* 2. Gherkin Tab with Syntax Highlighting */}
                {activeTab === 'gherkin' && (
                  <div className="space-y-4">
                    {gherkinArtifacts.length === 0 ? (
                      <div className="text-slate-500 dark:text-slate-400 text-center py-8 text-xs">{t('ticketItem.noGherkinYet')}</div>
                    ) : (
                      gherkinArtifacts.map(g => (
                        <div key={g.id} className="rounded-lg border border-amber-200 dark:border-amber-900 bg-amber-50/40 dark:bg-amber-950/20 p-4">
                          <div className="font-bold text-amber-900 dark:text-amber-300 text-xs mb-2 flex items-center justify-between">
                            <span>{g.name}</span>
                            <span className="flex items-center gap-3">
                              {openInNewTabLink(g)}
                              <span className="text-[10px] text-slate-500 dark:text-slate-400">{formatDateTime(g.created_at, i18n.language)}</span>
                            </span>
                          </div>
                          {g.content && (
                            <GherkinViewer
                              content={g.content}
                              scrollable
                              label={artifactScrollLabel(g)}
                            />
                          )}
                        </div>
                      ))
                    )}
                  </div>
                )}

                {/* 3. HTML Tab */}
                {activeTab === 'html' && (
                  <div className="space-y-4">
                    {htmlArtifacts.length === 0 ? (
                      <div className="text-slate-500 dark:text-slate-400 text-center py-8 text-xs">{t('ticketItem.noHtmlYet')}</div>
                    ) : (
                      htmlArtifacts.map(h => (
                        <div key={h.id} className="rounded-lg border border-slate-200 dark:border-slate-700 p-3 bg-white dark:bg-slate-800 shadow-xs">
                          <div className="flex items-center justify-between mb-2">
                            <span className="font-bold text-xs text-cyan-700 dark:text-cyan-400">{h.name}</span>
                            {/* Served from the DB via
                                GET /api/artifacts/{id}/content, not a local
                                file path, so this preview works the same
                                from any machine (DFLT-00006). Uses the same
                                sandboxed-preview-page link as every other tab
                                (openInNewTabLink) rather than a duplicate
                                inline implementation -- see DFLT-00053. */}
                            {openInNewTabLink(h)}
                          </div>
                          <iframe
                            src={(h.content || h.file_path || h.has_content) ? `/api/artifacts/${h.id}/content` : undefined}
                            title={h.name}
                            sandbox="allow-scripts"
                            className="w-full h-64 bg-white rounded border border-slate-300 dark:border-slate-600"
                          />
                        </div>
                      ))
                    )}
                  </div>
                )}

                {/* 4. All Artifacts Tab */}
                {activeTab === 'artifacts' && (
                  <div className="space-y-2">
                    {ticket.artifacts.length === 0 ? (
                      <div className="text-slate-500 dark:text-slate-400 text-center py-8 text-xs">{t('ticketItem.noArtifactsYet')}</div>
                    ) : (
                      ticket.artifacts.map(a => (
                        <div
                          key={a.id}
                          className="p-3 rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800 text-xs"
                        >
                          <div className="flex items-center justify-between font-semibold text-slate-800 dark:text-slate-200 mb-1">
                            <span className="flex items-center gap-2">
                              <FileText aria-hidden="true" className="w-4 h-4 text-indigo-500" />
                              {a.name}
                            </span>
                            <span className="flex items-center gap-2">
                              {downloadLink(a)}
                              <span className="text-[10px] px-2 py-0.5 rounded bg-slate-200 dark:bg-slate-700 text-slate-600 dark:text-slate-300 uppercase font-mono">
                                {a.type}
                              </span>
                            </span>
                          </div>
                          {a.type === 'gherkin' && a.content ? (
                            <div className="space-y-1">
                              <div className="flex justify-end">{openInNewTabLink(a)}</div>
                              <GherkinViewer
                                content={a.content}
                                scrollable
                                label={artifactScrollLabel(a)}
                              />
                            </div>
                          ) : a.type === 'text' && a.content ? (
                            <div className="space-y-1">
                              <div className="flex justify-end">{openInNewTabLink(a)}</div>
                              <MarkdownViewer
                                content={a.content}
                                scrollable
                                label={artifactScrollLabel(a)}
                              />
                            </div>
                          ) : a.type === 'image' ? (
                            // Served from the DB via
                            // GET /api/artifacts/{id}/content -- a.content
                            // here is base64 image data, never meant to be
                            // dumped as text (DFLT-00006).
                            (a.content || a.file_path || a.has_content) ? (
                              <img
                                src={`/api/artifacts/${a.id}/content`}
                                alt={a.name}
                                className="max-h-40 rounded border border-slate-200 dark:border-slate-700 mt-2"
                              />
                            ) : null
                          ) : a.type === 'html' ? (
                            <div className="mt-2">{openInNewTabLink(a)}</div>
                          ) : a.content ? (
                            <pre className="font-mono text-[11px] text-slate-700 dark:text-slate-300 max-h-32 overflow-y-auto whitespace-pre-wrap mt-2 p-2 bg-white dark:bg-slate-900 rounded border border-slate-200 dark:border-slate-700">
                              {a.content}
                            </pre>
                          ) : null}
                        </div>
                      ))
                    )}
                  </div>
                )}
              </div>
            </div>
          </div>

          {/* Action Footer: Claude Execution Panel */}
          <div className="bg-white dark:bg-slate-900 p-4 rounded-xl border border-slate-200 dark:border-slate-800 shadow-xs">
            <div className="flex items-center justify-between gap-3 mb-3">
              <div className="flex items-center gap-2">
                <span className="text-xs font-bold text-slate-700 dark:text-slate-300">{t('ticketItem.actions.label')}</span>
                <button
                  onClick={() => handleRunClaude(t('claudePrompts.refineTicket', { ticketId: ticket.id }), ticket.id)}
                  disabled={isRunning || ticket.status === 'DONE' || ticket.status === 'CLOSED'}
                  className="px-3 py-1.5 bg-white dark:bg-slate-800 hover:bg-slate-50 dark:hover:bg-slate-700 text-slate-700 dark:text-slate-300 border border-slate-300 dark:border-slate-600 rounded-lg text-xs font-semibold flex items-center gap-1.5 shadow-xs transition disabled:opacity-50 disabled:cursor-not-allowed disabled:hover:bg-white dark:disabled:hover:bg-slate-800"
                >
                  {isRunning ? <Loader2 aria-hidden="true" className="w-3.5 h-3.5 animate-spin" /> : <ClipboardEdit aria-hidden="true" className="w-3.5 h-3.5 text-indigo-600" />}
                  {t('ticketItem.actions.refine')}
                </button>
                <button
                  onClick={() => handleRunClaude(t('claudePrompts.processTicket', { ticketId: ticket.id }), ticket.id)}
                  disabled={isRunning || ticket.status === 'DONE' || ticket.status === 'CLOSED'}
                  className="px-3 py-1.5 bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 disabled:cursor-not-allowed text-white rounded-lg text-xs font-semibold flex items-center gap-1.5 shadow-xs transition"
                >
                  {isRunning ? <Loader2 aria-hidden="true" className="w-3.5 h-3.5 animate-spin" /> : <Play aria-hidden="true" className="w-3.5 h-3.5" />}
                  {t('ticketItem.actions.run')}
                </button>
              </div>
            </div>

            {/* Autopilot starts (DFLT-00142): refine through release without
                a person, in terminals of their own. */}
            <div className="mb-3">
              <AutopilotControls
                ticketId={ticket.id}
                status={ticket.status}
                view={autopilot}
                onSettled={onAutopilotChanged}
              />
            </div>

            {/* Custom Prompt Box */}
            <div className="flex gap-2">
              <textarea
                rows={2}
                value={promptText}
                onChange={e => setPromptText(e.target.value)}
                onKeyDown={e => {
                  if (isSubmitShortcut(e)) {
                    e.preventDefault();
                    handleSendPrompt();
                  }
                }}
                placeholder={t('ticketItem.promptPlaceholder')}
                className="flex-1 bg-slate-50 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded-lg p-2.5 text-xs text-slate-900 dark:text-slate-100 focus:outline-none focus:border-indigo-500 focus:bg-white dark:focus:bg-slate-800 resize-none font-sans"
              />
              <button
                onClick={handleSendPrompt}
                disabled={isRunning || !promptText.trim()}
                className="px-4 bg-indigo-600 hover:bg-indigo-500 disabled:opacity-50 text-white rounded-lg text-xs font-bold flex items-center justify-center gap-1.5 shadow-xs transition"
              >
                {isRunning ? <Loader2 aria-hidden="true" className="w-4 h-4 animate-spin" /> : <Send aria-hidden="true" className="w-4 h-4" />}
                {t('ticketItem.send')}
              </button>
            </div>

            {/* Launch status: the actual session runs in an external terminal now.
                読み上げは常時マウントの live region が担当する（SC 4.1.3）。 */}
            <StatusLiveRegion message={statusMessage || ''} />
            {statusMessage && (
              <div
                aria-hidden="true"
                className="mt-3 p-2.5 bg-slate-50 dark:bg-slate-800 text-slate-600 dark:text-slate-300 text-[11px] rounded-lg border border-slate-200 dark:border-slate-700"
              >
                {statusMessage}
              </div>
            )}
          </div>
        </div>
      )}
    </div>
  );
};
