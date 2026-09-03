/**
 * AutonomyMatrix — per-signal-kind autonomy override picker.
 *
 * Renders one row per signal_kind from the SDK registry. Each row exposes
 * a {@link SegmentedControl} with three options (suggest / draft / auto)
 * that maps to the operator-picked `autonomyLevel` override on the
 * underlying auto-action rules.
 *
 * Wire model
 * ----------
 * The DB schema is keyed by `(kind, signalKind)` where `kind` is one of
 * the four rule families (escalate_overdue / assign_owner /
 * nudge_assignee / close_stale_review). Operators only care about a
 * single "autonomy mode" per signal kind, so saving a row fans out into
 * four PATCH entries — one per rule kind — all carrying the same
 * `signalKind` + `autonomyLevel`.
 *
 * Reading current state: for a given signal_kind we look at the matching
 * rule rows and pick a single mode. If they disagree (e.g. manual SQL
 * edits) we pick the most common, then alphabetical rule-kind tie-break.
 * If no rule has an override at all the row renders with no active
 * segment and a muted "Default: <yamlDefault>" hint.
 *
 * Save flow uses an optimistic-update mutation mirroring
 * `apps/flow-web/src/features/settings/avatar-upload.tsx`: snapshot →
 * setQueryData → rollback on error → invalidate on settled.
 */

import { lookupSignalKind, SIGNAL_KINDS, type SignalKindDefinition } from '@nodate-flow/sdk';
import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import SegmentedControl from '@nodate-flow/ui/primitives/segmented-control';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import {
  type CSSProperties,
  type ReactElement,
  useCallback,
  useEffect,
  useRef,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';

import { apiRequest } from '../../../lib/api';
import { formatApiError } from '../../../lib/api-error';
import {
  type AutoActionRule,
  type AutoActionRuleKind,
  type AutonomyLevel,
  autoActionRulesKeys,
  type PatchAutoActionRule,
  useAutoActionRulesQuery,
} from '../../workspaces/auto-action-rules-api';

/* ------------------------------------------------------------------ */
/*  Constants                                                          */
/* ------------------------------------------------------------------ */

/**
 * All four rule kinds. When saving a single signal-kind row we fan out
 * a PATCH entry per rule kind so the underlying `(kind, signalKind)`
 * pairs all carry the same autonomy override.
 */
const RULE_KINDS: readonly AutoActionRuleKind[] = [
  'assign_owner',
  'close_stale_review',
  'escalate_overdue',
  'nudge_assignee',
] as const;

/** All three autonomy levels in display order. */
const AUTONOMY_LEVELS: readonly AutonomyLevel[] = ['suggest', 'draft', 'auto'] as const;

/** Debounce window (ms) for the aria-live "changed" announcement. */
const ANNOUNCE_DEBOUNCE_MS = 1000;

/* ------------------------------------------------------------------ */
/*  Helpers                                                            */
/* ------------------------------------------------------------------ */

/**
 * Pick the effective server-side autonomy mode for a signal kind by
 * scanning every rule row that carries this `signalKind`. When the rows
 * disagree (e.g. manual SQL editing) we pick the most common, breaking
 * ties on alphabetical rule-kind order so the result is deterministic.
 *
 * Returns undefined when no rule row for this signal kind has an
 * override set — the UI then renders the "unset" state.
 */
function resolveServerMode(
  rules: readonly AutoActionRule[],
  signalKind: string,
): AutonomyLevel | undefined {
  const matching = rules.filter((r) => r.signalKind === signalKind && r.autonomyLevel);
  if (matching.length === 0) return undefined;

  const counts = new Map<AutonomyLevel, number>();
  for (const r of matching) {
    if (!r.autonomyLevel) continue;
    counts.set(r.autonomyLevel, (counts.get(r.autonomyLevel) ?? 0) + 1);
  }

  let best: AutonomyLevel | undefined;
  let bestCount = -1;
  // Iterate AUTONOMY_LEVELS in fixed order so ties resolve deterministically
  // by autonomy-level alphabetical order (auto < draft < suggest).
  const sortedLevels = [...AUTONOMY_LEVELS].sort();
  for (const level of sortedLevels) {
    const c = counts.get(level) ?? 0;
    if (c > bestCount) {
      best = level;
      bestCount = c;
    }
  }
  return best;
}

/**
 * Fan out a single (signal_kind, autonomy_level) change into one PATCH
 * entry per rule kind. The shape matches PatchAutoActionRuleItem on the
 * wire — the four entries differ only in `kind`.
 */
function buildFourRuleKindPatches(signalKind: string, level: AutonomyLevel): PatchAutoActionRule[] {
  return RULE_KINDS.map((kind) => ({
    kind,
    signalKind,
    autonomyLevel: level,
  }));
}

/* ------------------------------------------------------------------ */
/*  Mutation                                                           */
/* ------------------------------------------------------------------ */

interface SaveArgs {
  workspaceId: string;
  patches: PatchAutoActionRule[];
  /** Optimistic overrides keyed by signalKind for cache mutation. */
  optimistic: Map<string, AutonomyLevel>;
}

interface SaveContext {
  previous: AutoActionRule[] | undefined;
}

/**
 * Optimistic PATCH mutation: snapshot the rules cache, apply the local
 * overrides immediately, then roll back on error / refetch on settled.
 */
function useSaveAutonomyOverrides() {
  const qc = useQueryClient();
  return useMutation<AutoActionRule[], Error, SaveArgs, SaveContext>({
    throwOnError: false,
    mutationFn: async ({ workspaceId, patches }): Promise<AutoActionRule[]> => {
      // Fan out a single batch PATCH containing every dirty row. The SDK
      // body type now natively carries signalKind + autonomyLevel after
      // the recent regen.
      const data = await apiRequest(
        (client) =>
          client.PATCH('/workspaces/{wsId}/ai/auto-action-rules', {
            params: { path: { wsId: workspaceId } },
            body: { rules: patches },
          }),
        'Failed to save autonomy overrides',
      );
      return (data.rules ?? []) as AutoActionRule[];
    },
    onMutate: async ({ workspaceId, optimistic }): Promise<SaveContext> => {
      const key = autoActionRulesKeys.rules(workspaceId);
      await qc.cancelQueries({ queryKey: key });
      const previous = qc.getQueryData<AutoActionRule[]>(key);
      if (previous) {
        const next = previous.map((r) => {
          if (r.signalKind && optimistic.has(r.signalKind)) {
            const level = optimistic.get(r.signalKind);
            return level ? { ...r, autonomyLevel: level } : r;
          }
          return r;
        });
        qc.setQueryData<AutoActionRule[]>(key, next);
      }
      return { previous };
    },
    onError: (_err, vars, ctx) => {
      if (ctx?.previous) {
        qc.setQueryData<AutoActionRule[]>(
          autoActionRulesKeys.rules(vars.workspaceId),
          ctx.previous,
        );
      }
    },
    onSettled: (_data, _err, vars) => {
      void qc.invalidateQueries({ queryKey: autoActionRulesKeys.rules(vars.workspaceId) });
    },
  });
}

/* ------------------------------------------------------------------ */
/*  Component                                                          */
/* ------------------------------------------------------------------ */

export interface AutonomyMatrixProps {
  workspaceId: string;
}

/** Default export: see file-level doc block. */
export default function AutonomyMatrix({ workspaceId }: AutonomyMatrixProps): ReactElement {
  const { t } = useTranslation(['ai', 'signal-kinds', 'settings']);
  const { data: rules } = useAutoActionRulesQuery(workspaceId);
  const saveMut = useSaveAutonomyOverrides();

  // Locally edited rows keyed by signal_kind. Cleared on successful save
  // or explicit Reset. Map (not record) so insertion order is preserved
  // and `size` is the dirty count.
  const [drafts, setDrafts] = useState<Map<string, AutonomyLevel>>(() => new Map());

  // Debounced announcer state: last-changed row + a counter that bumps
  // each keystroke so React renders even when announcing the same row
  // repeatedly with the same mode.
  const [announce, setAnnounce] = useState<{ kind: string; mode: AutonomyLevel; tick: number }>({
    kind: '',
    mode: 'suggest',
    tick: 0,
  });
  const announceTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(
    () => () => {
      if (announceTimer.current) clearTimeout(announceTimer.current);
    },
    [],
  );

  const dirtyCount = drafts.size;
  const saving = saveMut.isPending;

  const setLocal = useCallback((kind: string, level: AutonomyLevel): void => {
    setDrafts((prev) => {
      const next = new Map(prev);
      next.set(kind, level);
      return next;
    });
    // Debounced announce: schedule one second after the most recent change
    // so arrow-key scrubbing through the segments doesn't flood the
    // screen reader.
    if (announceTimer.current) clearTimeout(announceTimer.current);
    announceTimer.current = setTimeout(() => {
      setAnnounce((prev) => ({ kind, mode: level, tick: prev.tick + 1 }));
    }, ANNOUNCE_DEBOUNCE_MS);
  }, []);

  const handleReset = useCallback((): void => {
    setDrafts(new Map());
  }, []);

  const handleSave = useCallback((): void => {
    if (drafts.size === 0) return;
    const patches: PatchAutoActionRule[] = [];
    for (const [signalKind, level] of drafts) {
      patches.push(...buildFourRuleKindPatches(signalKind, level));
    }
    saveMut.mutate(
      { workspaceId, patches, optimistic: drafts },
      {
        onSuccess: () => {
          setDrafts(new Map());
          toaster.show({ tone: 'success', message: t('ai:autonomy.matrix.changed_toast') });
        },
        onError: (err) => {
          toaster.show({
            tone: 'danger',
            message: formatApiError(err, t, 'ai:autonomy.matrix.error_toast'),
          });
        },
      },
    );
  }, [drafts, saveMut, t, workspaceId]);

  // Empty signal-kind registry: defensive — the codegen should always
  // produce at least the bundled set, but if it ever returns an empty
  // list we show the muted empty-state hint. Widen the literal-typed
  // `length` to plain number so the comparison isn't elided.
  const signalKindCount: number = SIGNAL_KINDS.length;
  if (signalKindCount === 0) {
    return (
      <Card style={styles.card}>
        <header style={styles.header}>
          <h2 style={styles.title}>{t('ai:autonomy.matrix.title')}</h2>
          <p style={styles.description}>{t('ai:autonomy.matrix.description')}</p>
        </header>
        <p style={styles.empty}>{t('ai:autonomy.matrix.empty')}</p>
      </Card>
    );
  }

  return (
    <Card style={styles.card}>
      <header style={styles.header}>
        <h2 style={styles.title}>{t('ai:autonomy.matrix.title')}</h2>
        <p style={styles.description}>{t('ai:autonomy.matrix.description')}</p>
      </header>

      <div style={styles.scrollRegion}>
        <table aria-label={t('ai:autonomy.matrix.title')} style={styles.table}>
          <thead style={styles.thead}>
            <tr>
              <th scope="col" style={styles.thKind}>
                {t('ai:autonomy.matrix.header.kind')}
              </th>
              <th scope="col" style={styles.thMode}>
                {t('ai:autonomy.matrix.header.mode')}
              </th>
            </tr>
          </thead>
          <tbody>
            {SIGNAL_KINDS.map((def) => (
              <MatrixRow
                key={def.kind}
                def={def}
                rules={rules}
                draft={drafts.get(def.kind)}
                onChange={setLocal}
              />
            ))}
          </tbody>
        </table>
      </div>

      <footer style={styles.footer}>
        <Button
          type="button"
          variant="ghost"
          onClick={handleReset}
          disabled={saving || dirtyCount === 0}
        >
          {t('ai:autonomy.matrix.reset')}
        </Button>
        <Button
          type="button"
          variant="primary"
          onClick={handleSave}
          disabled={saving || dirtyCount === 0}
        >
          {dirtyCount === 0
            ? t('ai:autonomy.matrix.save_zero')
            : t('ai:autonomy.matrix.save', { count: dirtyCount })}
        </Button>
      </footer>

      {/*
       * Polite live region for screen readers; the `tick` counter in the
       * key forces React to re-render the node even when announcing the
       * same kind+mode in succession.
       */}
      <div aria-live="polite" aria-atomic="true" style={styles.srOnly}>
        {announce.tick > 0
          ? t('ai:autonomy.matrix.changed_announce', {
              kind: (() => {
                const def = lookupSignalKind(announce.kind);
                return def
                  ? t(`signal-kinds:${def.i18nKey}.label`, { keySeparator: false })
                  : announce.kind;
              })(),
              mode: t(`ai:autonomy.mode.${announce.mode}`),
            })
          : ''}
      </div>
    </Card>
  );
}

/* ------------------------------------------------------------------ */
/*  MatrixRow                                                          */
/* ------------------------------------------------------------------ */

interface MatrixRowProps {
  def: SignalKindDefinition;
  rules: readonly AutoActionRule[];
  draft: AutonomyLevel | undefined;
  onChange: (signalKind: string, level: AutonomyLevel) => void;
}

function MatrixRow({ def, rules, draft, onChange }: MatrixRowProps): ReactElement {
  const { t } = useTranslation(['ai', 'signal-kinds']);

  const serverValue = resolveServerMode(rules, def.kind);
  const effective = draft ?? serverValue;
  const isDirty = draft !== undefined && draft !== serverValue;
  const kindLabel = t(`signal-kinds:${def.i18nKey}.label`, { keySeparator: false });
  const kindDesc = t(`signal-kinds:${def.i18nKey}.description`, { keySeparator: false });

  const options = AUTONOMY_LEVELS.map((level) => ({
    value: level,
    label: t(`ai:autonomy.mode.${level}`),
    tone: toneFor(level),
  }));

  const rowStyle: CSSProperties = {
    borderBlockEnd: 'var(--nf-space-px) solid var(--nf-color-hairline)',
    borderInlineStart: isDirty ? '2px solid var(--nf-color-warning)' : '2px solid transparent',
  };

  return (
    <tr style={rowStyle}>
      <th scope="row" style={styles.tdKind}>
        <div style={styles.kindCell}>
          <span style={styles.kindName}>{kindLabel}</span>
          <span style={styles.kindDesc}>{kindDesc}</span>
        </div>
      </th>
      <td style={styles.tdMode}>
        <div style={styles.modeCell}>
          {/*
           * The SegmentedControl requires a controlled value. When no
           * override is set (no draft and no server value) we pass the
           * YAML default so the component still has a focal point, but
           * we render a muted hint below explaining the row is unset.
           */}
          <SegmentedControl
            ariaLabel={t('ai:autonomy.matrix.row_aria', { kind: kindLabel })}
            size="sm"
            fullWidth
            colourful
            value={effective ?? def.autonomyDefault}
            onChange={(v) => {
              onChange(def.kind, v);
            }}
            options={options}
          />
          {effective === undefined ? (
            <span style={styles.defaultHint}>
              {t('ai:autonomy.matrix.default_hint', {
                mode: t(`ai:autonomy.mode.${def.autonomyDefault}`),
              })}
            </span>
          ) : null}
        </div>
      </td>
    </tr>
  );
}

/**
 * Map an autonomy level to a SegmentedControl tone.
 * - suggest = neutral (low risk, observation only)
 * - draft   = warning (medium risk, human-confirmed)
 * - auto    = success (high autonomy, hands-off)
 */
function toneFor(level: AutonomyLevel): 'neutral' | 'warning' | 'success' {
  switch (level) {
    case 'suggest':
      return 'neutral';
    case 'draft':
      return 'warning';
    case 'auto':
      return 'success';
  }
}

/* ------------------------------------------------------------------ */
/*  Styles (token-only inline styles)                                 */
/* ------------------------------------------------------------------ */

const styles = {
  card: {
    display: 'flex',
    flexDirection: 'column',
    gap: 'var(--nf-space-4)',
    padding: 'var(--nf-space-4)',
  } satisfies CSSProperties,
  header: {
    display: 'flex',
    flexDirection: 'column',
    gap: 'var(--nf-space-1)',
  } satisfies CSSProperties,
  title: {
    margin: 0,
    fontSize: 'var(--nf-text-xl)',
    fontWeight: 'var(--nf-weight-semibold)',
    color: 'var(--nf-color-fg)',
    lineHeight: 'var(--nf-leading-tight)',
  } satisfies CSSProperties,
  description: {
    margin: 0,
    fontSize: 'var(--nf-text-sm)',
    color: 'var(--nf-color-fg-muted)',
    lineHeight: 'var(--nf-leading-normal)',
  } satisfies CSSProperties,
  scrollRegion: {
    maxBlockSize: '60vh',
    overflowY: 'auto',
    borderRadius: 'var(--nf-radius-md)',
    border: 'var(--nf-space-px) solid var(--nf-color-border)',
    background: 'var(--nf-color-surface)',
    boxShadow: 'var(--nf-shadow-sm)',
  } satisfies CSSProperties,
  table: {
    inlineSize: '100%',
    borderCollapse: 'collapse',
    fontSize: 'var(--nf-text-sm)',
    color: 'var(--nf-color-fg)',
    // Two-column grid via colgroup is overkill here; rely on td widths.
    tableLayout: 'fixed',
  } satisfies CSSProperties,
  thead: {
    position: 'sticky',
    insetBlockStart: 0,
    background: 'var(--nf-color-surface)',
    zIndex: 1,
  } satisfies CSSProperties,
  thKind: {
    textAlign: 'start',
    paddingBlock: 'var(--nf-space-2)',
    paddingInline: 'var(--nf-space-3)',
    fontSize: 'var(--nf-text-xs)',
    fontWeight: 'var(--nf-weight-semibold)',
    color: 'var(--nf-color-fg-muted)',
    textTransform: 'uppercase',
    letterSpacing: '0.04em',
    // nf-token-override: component dimension, not a spacing step
    minInlineSize: '220px',
    borderBlockEnd: 'var(--nf-space-px) solid var(--nf-color-border)',
  } satisfies CSSProperties,
  thMode: {
    textAlign: 'start',
    paddingBlock: 'var(--nf-space-2)',
    paddingInline: 'var(--nf-space-3)',
    fontSize: 'var(--nf-text-xs)',
    fontWeight: 'var(--nf-weight-semibold)',
    color: 'var(--nf-color-fg-muted)',
    textTransform: 'uppercase',
    letterSpacing: '0.04em',
    // nf-token-override: component dimension, not a spacing step
    inlineSize: '360px',
    borderBlockEnd: 'var(--nf-space-px) solid var(--nf-color-border)',
  } satisfies CSSProperties,
  tdKind: {
    paddingBlock: 'var(--nf-space-3)',
    paddingInline: 'var(--nf-space-3)',
    verticalAlign: 'middle',
    textAlign: 'start',
    fontWeight: 'var(--nf-weight-regular)',
  } satisfies CSSProperties,
  tdMode: {
    paddingBlock: 'var(--nf-space-3)',
    paddingInline: 'var(--nf-space-3)',
    verticalAlign: 'middle',
    // nf-token-override: component dimension, not a spacing step
    inlineSize: '360px',
  } satisfies CSSProperties,
  kindCell: {
    display: 'flex',
    flexDirection: 'column',
    gap: 'var(--nf-space-1)',
  } satisfies CSSProperties,
  kindName: {
    fontSize: 'var(--nf-text-sm)',
    fontWeight: 'var(--nf-weight-medium)',
    color: 'var(--nf-color-fg)',
    lineHeight: 'var(--nf-leading-tight)',
  } satisfies CSSProperties,
  kindDesc: {
    fontSize: 'var(--nf-text-xs)',
    color: 'var(--nf-color-fg-subtle)',
    lineHeight: 'var(--nf-leading-normal)',
  } satisfies CSSProperties,
  modeCell: {
    display: 'flex',
    flexDirection: 'column',
    gap: 'var(--nf-space-1)',
  } satisfies CSSProperties,
  defaultHint: {
    fontSize: 'var(--nf-text-xs)',
    color: 'var(--nf-color-fg-subtle)',
    fontStyle: 'italic',
  } satisfies CSSProperties,
  footer: {
    display: 'flex',
    justifyContent: 'flex-end',
    alignItems: 'center',
    gap: 'var(--nf-space-2)',
  } satisfies CSSProperties,
  empty: {
    margin: 0,
    paddingBlock: 'var(--nf-space-6)',
    textAlign: 'center',
    color: 'var(--nf-color-fg-muted)',
    fontSize: 'var(--nf-text-sm)',
  } satisfies CSSProperties,
  srOnly: {
    position: 'absolute',
    inlineSize: '1px',
    blockSize: '1px',
    padding: 0,
    margin: '-1px',
    overflow: 'hidden',
    clip: 'rect(0, 0, 0, 0)',
    whiteSpace: 'nowrap',
    borderWidth: 0,
  } satisfies CSSProperties,
};
