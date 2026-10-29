/**
 * AutoActionSettingsPage — configure the autonomous auto-action executor.
 *
 * Suspense-ready: reads the current settings via
 * `useAutoActionSettingsQuery` (suspense mode). Submits changes via
 * PATCH on "Save" click. Disables interval and threshold inputs when
 * the feature is toggled off.
 *
 * Below it, a per-rule configuration table backed by
 * `useAutoActionRulesQuery` / `useUpdateAutoActionRules` saves on its
 * own. Each section keeps a baseline of what the server holds, so its
 * inline save status reflects unsaved edits and resets after a save.
 */

import Button from '@nodate-flow/ui/primitives/button';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Switch from '@nodate-flow/ui/primitives/switch';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { formatApiError } from '../../lib/api-error';
import AutonomyMatrix from '../ai/autonomy/autonomy-matrix';
import SaveStatus, { useSaveStatus } from '../settings/save-status';
import ui from '../settings/settings-ui.module.css';
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

  // The values the server holds; the form is dirty while any field differs.
  const [baseline, setBaseline] = useState(() => ({
    enabled: settings.enabled,
    intervalMinutes: settings.intervalMinutes,
    threshold: settings.threshold,
  }));
  const [enabled, setEnabled] = useState(baseline.enabled);
  const [intervalMinutes, setIntervalMinutes] = useState(baseline.intervalMinutes);
  const [threshold, setThreshold] = useState(baseline.threshold);
  const [submitting, setSubmitting] = useState(false);
  const dirty =
    enabled !== baseline.enabled ||
    intervalMinutes !== baseline.intervalMinutes ||
    threshold !== baseline.threshold;
  const saveStatus = useSaveStatus(dirty);

  const handleSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    setSubmitting(true);

    const patch: PatchAutoActionSettingsInput = {};
    if (enabled !== baseline.enabled) {
      patch.enabled = enabled;
    }
    if (intervalMinutes !== baseline.intervalMinutes) {
      patch.intervalMinutes = intervalMinutes;
    }
    if (threshold !== baseline.threshold) {
      patch.threshold = threshold;
    }

    try {
      await updateMut.mutateAsync({ workspaceId, patch });
      setBaseline({ enabled, intervalMinutes, threshold });
      saveStatus.markSaved();
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'workspace.auto_actions.errors.update_failed'),
      });
    } finally {
      setSubmitting(false);
    }
  };

  const tier = thresholdTier(threshold);

  return (
    <div className={ui.page}>
      <header className={ui.header}>
        <div className={ui.headerText}>
          <h1 className={ui.title}>{t('workspace.auto_actions.title')}</h1>
          <p className={ui.lede}>{t('workspace.auto_actions.description')}</p>
        </div>
      </header>

      <form
        onSubmit={(e) => {
          void handleSubmit(e);
        }}
        className={ui.section}
      >
        {/* Enabled toggle */}
        <FormField
          label={t('workspace.auto_actions.enabled.label')}
          description={t('workspace.auto_actions.enabled.description')}
        >
          {(control) => <Switch {...control} checked={enabled} onCheckedChange={setEnabled} />}
        </FormField>

        {/* Interval (minutes) */}
        <FormField
          label={t('workspace.auto_actions.interval.label')}
          description={t('workspace.auto_actions.interval.description')}
        >
          {(control) => (
            <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--nf-space-2)' }}>
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
                // nf-token-override: component dimension, not a spacing step
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
            <div style={{ display: 'flex', flexDirection: 'column', gap: 'var(--nf-space-1-5)' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 'var(--nf-space-3)' }}>
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
                    // nf-token-override: component dimension, not a spacing step
                    minInlineSize: '3rem',
                    textAlign: 'end',
                  }}
                >
                  {threshold.toFixed(2)}
                </span>
              </div>
              <span
                style={{
                  fontSize: 'var(--nf-text-supporting)',
                  color: 'var(--nf-color-fg-muted)',
                  fontWeight: 500,
                }}
              >
                {t(`workspace.auto_actions.threshold.${tier}`)}
              </span>
            </div>
          )}
        </FormField>

        <div className={ui.actions}>
          <SaveStatus saved={saveStatus.saved} dirty={dirty} />
          <Button
            type="submit"
            variant="primary"
            disabled={submitting}
            aria-busy={submitting || undefined}
          >
            {t('workspace.auto_actions.save')}
          </Button>
        </div>
      </form>

      <AutoActionRulesSection workspaceId={workspaceId} masterEnabled={enabled} />

      {/*
       * Per-signal-kind autonomy matrix. Sits beneath the
       * legacy four-rule table and lets operators set an autonomyLevel
       * override per signal kind that fans out into all four rule rows.
       */}
      <AutonomyMatrix workspaceId={workspaceId} />
    </div>
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

  // The rules the server holds; drafts are compared against them for the save status.
  const [baseline, setBaseline] = useState<AutoActionRule[]>(rules);
  const [drafts, setDrafts] = useState<Record<RuleKind, RuleDraft>>(() => toRuleDraftMap(rules));
  const [savingRules, setSavingRules] = useState(false);
  const pendingPatches = buildRulesPatch(baseline, drafts);
  const dirty = pendingPatches.length > 0;
  const saveStatus = useSaveStatus(dirty);

  const handleRulesSubmit = async (e: FormEvent<HTMLFormElement>): Promise<void> => {
    e.preventDefault();
    if (!dirty) return;

    setSavingRules(true);
    try {
      await updateRulesMut.mutateAsync({ workspaceId, rules: pendingPatches });
      setBaseline((prev) =>
        prev.map((rule) => ({ ...rule, ...(drafts[rule.kind as RuleKind] ?? {}) })),
      );
      saveStatus.markSaved();
    } catch (err) {
      toaster.show({
        tone: 'danger',
        message: formatApiError(err, t, 'workspace.auto_actions.rules.errors.update_failed'),
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

  return (
    <form
      className={ui.section}
      aria-labelledby="auto-action-rules-title"
      onSubmit={(e) => {
        void handleRulesSubmit(e);
      }}
    >
      <div className={ui.sectionHeader}>
        <h2 id="auto-action-rules-title" className={ui.sectionTitle}>
          {t('workspace.auto_actions.rules.title')}
        </h2>
        <p className={ui.sectionDescription}>{t('workspace.auto_actions.rules.description')}</p>
      </div>

      <div className={ui.tableWrap}>
        <table className={ui.table}>
          <thead>
            <tr>
              <th scope="col">{t('workspace.auto_actions.rules.column.rule')}</th>
              <th scope="col">{t('workspace.auto_actions.rules.column.enabled')}</th>
              <th scope="col">{t('workspace.auto_actions.rules.column.confidence')}</th>
              <th scope="col">{t('workspace.auto_actions.rules.column.idle_hours')}</th>
            </tr>
          </thead>
          <tbody>
            {RULE_KINDS.map((kind) => {
              const draft = drafts[kind];
              const rowDisabled = !masterEnabled;
              const fieldsDisabled = rowDisabled || !draft.enabled;
              const idleDisabled = kind === 'escalate_overdue';

              return (
                <tr key={kind}>
                  {/* Rule name + description */}
                  <td>
                    <div
                      style={{
                        display: 'flex',
                        flexDirection: 'column',
                        gap: 'var(--nf-space-0-5)',
                      }}
                    >
                      <span style={{ fontWeight: 500 }}>
                        {t(`workspace.auto_actions.rules.${kind}.label`)}
                      </span>
                      <span className={ui.muted} style={{ fontSize: 'var(--nf-text-xs)' }}>
                        {t(`workspace.auto_actions.rules.${kind}.description`)}
                      </span>
                    </div>
                  </td>

                  {/* Enabled toggle */}
                  <td>
                    <Switch
                      checked={draft.enabled}
                      disabled={rowDisabled}
                      onCheckedChange={(next) => {
                        updateDraft(kind, { enabled: next });
                      }}
                      aria-label={t(`workspace.auto_actions.rules.${kind}.label`)}
                    />
                  </td>

                  {/* Confidence */}
                  <td>
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
                        // nf-token-override: component dimension, not a spacing step
                        inlineSize: '5rem',
                        fontVariantNumeric: 'tabular-nums',
                      }}
                    />
                  </td>

                  {/* Idle hours */}
                  <td>
                    {idleDisabled ? (
                      <span className={ui.muted}>{t('workspace.auto_actions.rules.idle_na')}</span>
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
                          // nf-token-override: component dimension, not a spacing step
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

      <div className={ui.actions}>
        <SaveStatus saved={saveStatus.saved} dirty={dirty} />
        <Button
          type="submit"
          variant="primary"
          disabled={savingRules || !masterEnabled}
          aria-busy={savingRules || undefined}
        >
          {t('workspace.auto_actions.rules.save')}
        </Button>
      </div>
    </form>
  );
}
