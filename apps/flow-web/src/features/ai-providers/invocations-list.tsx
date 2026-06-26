/**
 * InvocationsList — workspace AI activity audit panel (2.WEB-2).
 *
 * Renders the most recent redacted LLM calls as a compact table. All
 * prompt / response bodies are already redacted at write time; this
 * component is a thin presentation layer.
 */

import Badge from '@nodate-flow/ui/primitives/badge';
import Card from '@nodate-flow/ui/primitives/card';
import type { ReactElement } from 'react';
import { useTranslation } from 'react-i18next';

import { type AiInvocation, useAiInvocationsQuery } from './invocations-api';

function toneForStatus(status: string): 'accent' | 'warning' | 'danger' {
  if (status === 'ok') return 'accent';
  if (status === 'blocked') return 'warning';
  return 'danger';
}

function formatWhen(unix: number): string {
  return new Date(unix * 1000).toLocaleString();
}

function InvocationRow({ row }: { row: AiInvocation }): ReactElement {
  const { t } = useTranslation('settings');
  return (
    <li>
      <Card style={{ padding: '0.75rem 0.875rem' }}>
        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.375rem' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', flexWrap: 'wrap' }}>
            <Badge tone={toneForStatus(row.status)}>{row.status}</Badge>
            <strong style={{ fontSize: '0.8125rem' }}>{row.purpose}</strong>
            <span
              style={{
                fontFamily: 'var(--font-mono)',
                fontSize: 'var(--nf-text-xs)',
                color: 'var(--nf-color-fg-muted)',
              }}
            >
              {row.model}
            </span>
            <span
              style={{
                marginInlineStart: 'auto',
                fontSize: 'var(--nf-text-xs)',
                color: 'var(--nf-color-fg-muted)',
              }}
            >
              {formatWhen(row.invokedAt)}
            </span>
          </div>
          <p
            style={{
              margin: 0,
              fontSize: 'var(--nf-text-xs)',
              color: 'var(--nf-color-fg-muted)',
              whiteSpace: 'pre-wrap',
              overflow: 'hidden',
              display: '-webkit-box',
              // biome-ignore lint/style/useNamingConvention: vendor-prefixed CSS props
              WebkitLineClamp: 2,
              // biome-ignore lint/style/useNamingConvention: vendor-prefixed CSS props
              WebkitBoxOrient: 'vertical',
            }}
          >
            {row.promptRedacted}
          </p>
          {row.errorCode ? (
            <span style={{ fontSize: 'var(--nf-text-xs)', color: 'var(--nf-color-danger)' }}>
              {t('ai_activity.error_code', { code: row.errorCode })}
            </span>
          ) : null}
        </div>
      </Card>
    </li>
  );
}

export default function InvocationsList({ workspaceId }: { workspaceId: string }): ReactElement {
  const { t } = useTranslation('settings');
  const { data } = useAiInvocationsQuery(workspaceId);
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
      <header style={{ display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
        <h1 style={{ margin: 0, fontSize: 'var(--nf-text-2xl)' }}>{t('ai_activity.title')}</h1>
        <p style={{ margin: 0, color: 'var(--nf-color-fg-muted)', fontSize: 'var(--nf-text-sm)' }}>
          {t('ai_activity.description')}
        </p>
      </header>
      {data.length === 0 ? (
        <div
          style={{
            padding: '3rem 1rem',
            textAlign: 'center',
            color: 'var(--nf-color-fg-muted)',
            border: '1px dashed var(--nf-color-border)',
            borderRadius: '0.75rem',
            background: 'var(--nf-color-bg-sunken)',
            fontSize: 'var(--nf-text-sm)',
          }}
        >
          {t('ai_activity.empty')}
        </div>
      ) : (
        <ul
          style={{
            listStyle: 'none',
            padding: 0,
            margin: 0,
            display: 'flex',
            flexDirection: 'column',
            gap: '0.5rem',
          }}
        >
          {data.map((row) => (
            <InvocationRow key={row.id} row={row} />
          ))}
        </ul>
      )}
    </div>
  );
}
