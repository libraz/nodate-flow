/**
 * AutoActionSettingsPage — configure the autonomous auto-action executor.
 *
 * Suspense-ready: reads the current settings via
 * `useAutoActionSettingsQuery` (suspense mode). Submits changes via
 * PATCH on "Save" click. Disables interval and threshold inputs when
 * the feature is toggled off.
 *
 * The lower half renders a per-rule configuration table backed by
 * `useAutoActionRulesQuery` / `useUpdateAutoActionRules`.
 */

import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import AutonomyMatrix from '../ai/autonomy/autonomy-matrix';
import {
  type AutoActionRule,
  type PatchAutoActionRule,
  useAutoActionRulesQuery,
  useUpdateAutoActionRules,
} from './auto-action-rules-api';
import {
  type PatchAutoActionSettingsInput,
  useAutoActionSettingsQuery,
  useUpdateAutoActionSettings,
} from './auto-action-settings-api';

export interface AutoActionSettingsPageProps {
  workspaceId: string;
}

/** Map a numeric threshold to a qualitative i18n key suffix. */
function thresholdTier(value: number): 'low' | 'medium' | 'high' {
  if (value <= 0.5) return 'low';
  if (value >= 0.8) return 'high';
  return 'medium';
}

export default function AutoActionSettingsPage({
  workspaceId,
}: AutoActionSettingsPageProps): ReactElement {
  const { t } = useTranslation('settings');
  const { data: settings } = useAutoActionSettingsQuery(workspaceId);
  const updateMut = useUpdateAutoActionSettings();

  const [enabled, setEnabled] = useState(settings.enabled);
  const [intervalMinutes, setIntervalMinutes] = useState(settings.intervalMinutes);
  const [threshold, setThreshold] = useState(settings.threshold);
  const [submitting, setSubmitting] = useState(false);

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    setSubmitting(true);

    const patch: PatchAutoActionSettingsInput = {};
    if (enabled !== settings.enabled) {
      patch.enabled = enabled;
    }
    if (intervalMinutes !== settings.intervalMinutes) {
      patch.intervalMinutes = intervalMinutes;
    }
    if (threshold !== settings.threshold) {
      patch.threshold = threshold;
    }

    try {
      await updateMut.mutateAsync({ workspaceId, patch });
      toaster.show({
        tone: 'success',
        message: t('workspace.auto_actions.saved'),
      });
    } catch {
      toaster.show({
        tone: 'danger',
        message: t('workspace.auto_actions.errors.update_failed'),
      });
    } finally {
      setSubmitting(false);
    }
  };

  const tier = thresholdTier(threshold);

  return (
    <form
      onSubmit={(e) => {
        void handleSubmit(e);
      }}
      style={{ display: 'flex', flexDirection: 'column', gap: '1.25rem' }}
    >
      <header style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
        <h1 style={{ margin: 0, fontSize: 'var(--nf-text-2xl)' }}>
          {t('workspace.auto_actions.title')}
        </h1>
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: 'var(--nf-text-sm)' }}>
          {t('workspace.auto_actions.description')}
        </p>
      </header>

      {/* Enabled toggle */}
      <FormField
        label={t('workspace.auto_actions.enabled.label')}
        description={t('workspace.auto_actions.enabled.description')}
      >
        {(control) => (
          <input
            {...control}
            type="checkbox"
            role="switch"
            aria-checked={enabled}
            checked={enabled}
            onChange={(e) => {
              setEnabled(e.target.checked);
            }}
            style={{ inlineSize: '1.25rem', blockSize: '1.25rem' }}
          />
        )}
      </FormField>

      {/* Interval (minutes) */}
      <FormField
        label={t('workspace.auto_actions.interval.label')}
        description={t('workspace.auto_actions.interval.description')}
      >
        {(control) => (
          <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem' }}>
            <Input
              {...control}
              type="number"
              min={0}
              max={1440}
              step={1}
              value={String(intervalMinutes)}
              disabled={!enabled}
              onChange={(e) => {
                const v = Number.parseInt(e.target.value, 10);
                if (!Number.isNaN(v)) {
                  setIntervalMinutes(Math.max(0, Math.min(1440, v)));
                }
              }}
              style={{ inlineSize: '6rem' }}
            />
            <span
              style={{
                fontSize: 'var(--nf-text-sm)',
                color: 'var(--nf-color-fg-muted)',
              }}
            >
              {intervalMinutes === 0
                ? t('workspace.auto_actions.interval.disabled')
                : t('workspace.auto_actions.interval.minutes')}
            </span>
          </div>
        )}
      </FormField>

      {/* Threshold slider */}
      <FormField
        label={t('workspace.auto_actions.threshold.label')}
        description={t('workspace.auto_actions.threshold.description')}
      >
        {(control) => (
          <div style={{ display: 'flex', flexDirection: 'column', gap: '0.375rem' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: '0.75rem' }}>
              <input
                {...control}
                type="range"
                min={0}
                max={1}
                step={0.05}
                value={threshold}
                disabled={!enabled}
                onChange={(e) => {
                  setThreshold(Number.parseFloat(e.target.value));
                }}
                style={{ flex: 1 }}
              />
              <span
                style={{
                  fontSize: 'var(--nf-text-sm)',
                  fontVariantNumeric: 'tabular-nums',
                  minInlineSize: '3rem',
                  textAlign: 'end',
                }}
              >
                {threshold.toFixed(2)}
              </span>
            </div>
            <span
              style={{
                fontSize: '0.8125rem',
                color: 'var(--nf-color-fg-muted)',
                fontWeight: 500,
              }}
            >
              {t(`workspace.auto_actions.threshold.${tier}`)}
            </span>
          </div>
        )}
      </FormField>

      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <Button type="submit" variant="primary" disabled={submitting}>
          {submitting ? t('workspace.auto_actions.saving') : t('workspace.auto_actions.save')}
        </Button>
      </div>

      <hr style={{ border: 'none', borderBlockStart: '1px solid var(--nf-color-border)' }} />

      <AutoActionRulesSection workspaceId={workspaceId} masterEnabled={enabled} />

      <hr style={{ border: 'none', borderBlockStart: '1px solid var(--nf-color-border)' }} />

      {/*
       * Per-signal-kind autonomy matrix (Phase 4 / A2). Sits beneath the
       * legacy four-rule table and lets operators set an autonomyLevel
       * override per signal kind that fans out into all four rule rows.
       */}
      <AutonomyMatrix workspaceId={workspaceId} />
    </form>
  );
}

/* ------------------------------------------------------------------ */
/*  Rule kinds and their i18n key suffixes                            */
/* ------------------------------------------------------------------ */

const RULE_KINDS = [
  'escalate_overdue',
  'assign_owner',
  'nudge_assignee',
  'close_stale_review',
] as const;

type RuleKind = (typeof RULE_KINDS)[number];

/** Local draft state for a single rule row. */
interface RuleDraft {
  enabled: boolean;
  confidence: number;
  idleHours: number;
}

/** Build a kind-keyed map from the API response array. */
function toRuleDraftMap(rules: AutoActionRule[]): Record<RuleKind, RuleDraft> {
  const map = {} as Record<RuleKind, RuleDraft>;
  for (const r of rules) {
    map[r.kind as RuleKind] = {
      enabled: r.enabled,
      confidence: r.confidence,
      idleHours: r.idleHours,
    };
  }
  // Ensure every expected kind has a default even if the API omits it.
  for (const kind of RULE_KINDS) {
    if (!(kind in map)) {
      map[kind] = { enabled: false, confidence: 0.7, idleHours: 48 };
    }
  }
  return map;
}

/** Compute the sparse patch array — only rules that actually changed. */
function buildRulesPatch(
  original: AutoActionRule[],
  drafts: Record<RuleKind, RuleDraft>,
): PatchAutoActionRule[] {
  const origMap = new Map(original.map((r) => [r.kind, r]));
  const patches: PatchAutoActionRule[] = [];

  for (const kind of RULE_KINDS) {
    const draft = drafts[kind];
    const orig = origMap.get(kind);
    if (!orig) continue;

    const p: PatchAutoActionRule = { kind };
    let changed = false;

    if (draft.enabled !== orig.enabled) {
      p.enabled = draft.enabled;
      changed = true;
    }
    if (draft.confidence !== orig.confidence) {
      p.confidence = draft.confidence;
      changed = true;
    }
    if (draft.idleHours !== orig.idleHours) {
      p.idleHours = draft.idleHours;
      changed = true;
    }

    if (changed) {
      patches.push(p);
    }
  }

  return patches;
}

/* ------------------------------------------------------------------ */
/*  AutoActionRulesSection                                            */
/* ------------------------------------------------------------------ */

interface AutoActionRulesSectionProps {
  workspaceId: string;
  masterEnabled: boolean;
}

function AutoActionRulesSection({
  workspaceId,
  masterEnabled,
}: AutoActionRulesSectionProps): ReactElement {
  const { t } = useTranslation('settings');
  const { data: rules } = useAutoActionRulesQuery(workspaceId);
  const updateRulesMut = useUpdateAutoActionRules();

  const [drafts, setDrafts] = useState<Record<RuleKind, RuleDraft>>(() => toRuleDraftMap(rules));
  const [savingRules, setSavingRules] = useState(false);

  const handleRulesSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    // Prevent the parent form from submitting — the rules section lives
    // inside the same <form> but has its own submit flow.
    e.preventDefault();
    e.stopPropagation();

    const patches = buildRulesPatch(rules, drafts);
    if (patches.length === 0) return;

    setSavingRules(true);
    try {
      await updateRulesMut.mutateAsync({ workspaceId, rules: patches });
      toaster.show({
        tone: 'success',
        message: t('workspace.auto_actions.rules.saved'),
      });
    } catch {
      toaster.show({
        tone: 'danger',
        message: t('workspace.auto_actions.rules.errors.update_failed'),
      });
    } finally {
      setSavingRules(false);
    }
  };

  const updateDraft = (kind: RuleKind, patch: Partial<RuleDraft>): void => {
    setDrafts((prev) => ({
      ...prev,
      [kind]: { ...prev[kind], ...patch },
    }));
  };

  const thStyle: React.CSSProperties = {
    textAlign: 'start',
    padding: '0.5rem 0.75rem',
    fontSize: '0.8125rem',
    fontWeight: 600,
    color: 'var(--nf-color-fg-muted)',
    whiteSpace: 'nowrap',
  };

  const tdStyle: React.CSSProperties = {
    padding: '0.5rem 0.75rem',
    verticalAlign: 'middle',
  };

  return (
    // Use a nested fieldset so the rules "Save" button does not interfere
    // with the global settings submit handler above.  We attach the
    // submit handler to the wrapping div via a button with form attribute
    // pointing at a hidden inner form — but since the section is already
    // inside the outer <form>, we rely on the button's onClick instead.
    <section style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
        <h2 style={{ margin: 0, fontSize: 'var(--nf-text-lg)' }}>
          {t('workspace.auto_actions.rules.title')}
        </h2>
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: 'var(--nf-text-sm)' }}>
          {t('workspace.auto_actions.rules.description')}
        </p>
      </header>

      <div style={{ overflowX: 'auto' }}>
        <table
          style={{
            inlineSize: '100%',
            borderCollapse: 'collapse',
            fontSize: 'var(--nf-text-sm)',
          }}
        >
          <thead>
            <tr
              style={{
                borderBlockEnd: '1px solid var(--nf-color-border)',
              }}
            >
              <th style={thStyle}>{t('workspace.auto_actions.rules.column.rule')}</th>
              <th style={thStyle}>{t('workspace.auto_actions.rules.column.enabled')}</th>
              <th style={thStyle}>{t('workspace.auto_actions.rules.column.confidence')}</th>
              <th style={thStyle}>{t('workspace.auto_actions.rules.column.idle_hours')}</th>
            </tr>
          </thead>
          <tbody>
            {RULE_KINDS.map((kind) => {
              const draft = drafts[kind];
              const rowDisabled = !masterEnabled;
              const fieldsDisabled = rowDisabled || !draft.enabled;
              const idleDisabled = kind === 'escalate_overdue';

              return (
                <tr
                  key={kind}
                  style={{
                    borderBlockEnd: '1px solid var(--nf-color-border)',
                  }}
                >
                  {/* Rule name + description */}
                  <td style={tdStyle}>
                    <div style={{ display: 'flex', flexDirection: 'column', gap: '0.125rem' }}>
                      <span style={{ fontWeight: 500 }}>
                        {t(`workspace.auto_actions.rules.${kind}.label`)}
                      </span>
                      <span
                        style={{
                          fontSize: 'var(--nf-text-xs)',
                          color: 'var(--nf-color-fg-muted)',
                        }}
                      >
                        {t(`workspace.auto_actions.rules.${kind}.description`)}
                      </span>
                    </div>
                  </td>

                  {/* Enabled toggle */}
                  <td style={{ ...tdStyle, textAlign: 'center' }}>
                    <input
                      type="checkbox"
                      role="switch"
                      aria-checked={draft.enabled}
                      checked={draft.enabled}
                      disabled={rowDisabled}
                      onChange={(e) => {
                        updateDraft(kind, { enabled: e.target.checked });
                      }}
                      aria-label={t(`workspace.auto_actions.rules.${kind}.label`)}
                      style={{ inlineSize: '1.125rem', blockSize: '1.125rem' }}
                    />
                  </td>

                  {/* Confidence */}
                  <td style={tdStyle}>
                    <Input
                      type="number"
                      min={0}
                      max={1}
                      step={0.05}
                      value={String(draft.confidence)}
                      disabled={fieldsDisabled}
                      onChange={(e) => {
                        const v = Number.parseFloat(e.target.value);
                        if (!Number.isNaN(v)) {
                          updateDraft(kind, { confidence: Math.max(0, Math.min(1, v)) });
                        }
                      }}
                      aria-label={t('workspace.auto_actions.rules.column.confidence')}
                      style={{
                        inlineSize: '5rem',
                        fontVariantNumeric: 'tabular-nums',
                      }}
                    />
                  </td>

                  {/* Idle hours */}
                  <td style={tdStyle}>
                    {idleDisabled ? (
                      <span
                        style={{
                          fontSize: '0.8125rem',
                          color: 'var(--nf-color-fg-muted)',
                          fontStyle: 'italic',
                        }}
                      >
                        {t('workspace.auto_actions.rules.idle_na')}
                      </span>
                    ) : (
                      <Input
                        type="number"
                        min={1}
                        step={1}
                        value={String(draft.idleHours)}
                        disabled={fieldsDisabled}
                        onChange={(e) => {
                          const v = Number.parseInt(e.target.value, 10);
                          if (!Number.isNaN(v)) {
                            updateDraft(kind, { idleHours: Math.max(1, v) });
                          }
                        }}
                        aria-label={t('workspace.auto_actions.rules.column.idle_hours')}
                        style={{
                          inlineSize: '5rem',
                          fontVariantNumeric: 'tabular-nums',
                        }}
                      />
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <Button
          type="button"
          variant="primary"
          disabled={savingRules || !masterEnabled}
          onClick={(e) => {
            // Synthesise a FormEvent-like call so we can reuse handleRulesSubmit.
            void handleRulesSubmit(e as unknown as FormEvent<HTMLFormElement>);
          }}
        >
          {savingRules
            ? t('workspace.auto_actions.rules.saving')
            : t('workspace.auto_actions.rules.save')}
        </Button>
      </div>
    </section>
  );
}
