/**
 * WebhooksSettingsPage — `/workspaces/{wsId}/settings/webhooks`. Workspace
 * admin pane for outbound webhook subscriptions: list + create + delete +
 * toggle + send-test + view delivery history.
 *
 * What the page deliberately does not offer, and why:
 *   - No edit and no per-delivery retry: the flow-api surface has no
 *     PUT/PATCH on the subscription body (only a toggle), no per-delivery
 *     retry, and no rotate-secret. The page offers create, delete,
 *     toggle, send-test, and a read-only deliveries drawer. To change
 *     url / description / eventTypes the operator deletes and recreates.
 *   - The signing secret is also returned by GET detail, so the create
 *     reveal is informational rather than single-shot. The "Show secret"
 *     menu action uses the same detail endpoint for later retrieval.
 *   - The "last delivery status" column was dropped from the list to
 *     avoid an N-row deliveries fan-out; per-row status is reachable via
 *     the deliveries drawer instead.
 *   - {@link useWebhooksQuery} is non-suspense so the table can re-render
 *     after toggle / delete / test without throwing into Suspense.
 *
 * Hooks consumed (all in `./api.ts`):
 *   - {@link useWebhooksQuery}             — list (poll-friendly useQuery)
 *   - {@link useWebhookQuery}              — single detail (for secret)
 *   - {@link useDeliveriesQuery}           — read-only delivery history
 *   - {@link useCreateWebhookMutation}     — POST webhooks
 *   - {@link useDeleteWebhookMutation}     — DELETE webhooks/{id}
 *   - {@link useToggleWebhookMutation}     — PATCH webhooks/{id}/toggle
 *   - {@link useTestWebhookMutation}       — POST webhooks/{id}/test
 */

import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Card from '@nodate-flow/ui/primitives/card';
import Chip from '@nodate-flow/ui/primitives/chip';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import Drawer from '@nodate-flow/ui/primitives/drawer';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Popover from '@nodate-flow/ui/primitives/popover';
import Spinner from '@nodate-flow/ui/primitives/spinner';
import Switch from '@nodate-flow/ui/primitives/switch';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import { getRouteApi } from '@tanstack/react-router';
import { MoreHorizontal } from 'lucide-react';
import { type ChangeEvent, type FormEvent, type ReactElement, useState } from 'react';
import { useTranslation } from 'react-i18next';

import { ApiError, formatApiError, isNetworkError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import {
  useCreateWebhookMutation,
  useDeleteWebhookMutation,
  useDeliveriesQuery,
  useTestWebhookMutation,
  useToggleWebhookMutation,
  useWebhookQuery,
  useWebhooksQuery,
  type Webhook,
  type WebhookDelivery,
} from './api';
import styles from './webhooks-settings-page.module.css';

const routeApi = getRouteApi('/_authenticated/workspaces/$id/settings/webhooks');

/**
 * Coerce the `unknown`-typed eventTypes payload from the SDK into a
 * displayable string array. The backend stores it as a JSON array of
 * strings; we defensively filter to strings only.
 */
function normaliseEventTypes(value: unknown): string[] {
  if (!Array.isArray(value)) return [];
  const out: string[] = [];
  for (const item of value) {
    if (typeof item === 'string' && item.length > 0) out.push(item);
  }
  return out;
}

/** Map a delivery `status` string to a Badge tone. */
function deliveryTone(status: string): BadgeTone {
  switch (status) {
    case 'delivered':
      return 'success';
    case 'failed':
      return 'danger';
    case 'dead':
      return 'danger';
    case 'pending':
      return 'info';
    default:
      return 'neutral';
  }
}

/** Resolve the i18n key for a delivery `status` pill. */
function deliveryStatusKey(
  status: string,
):
  | 'settings.webhooks.deliveries.status.pending'
  | 'settings.webhooks.deliveries.status.delivered'
  | 'settings.webhooks.deliveries.status.failed'
  | 'settings.webhooks.deliveries.status.dead' {
  switch (status) {
    case 'delivered':
      return 'settings.webhooks.deliveries.status.delivered';
    case 'failed':
      return 'settings.webhooks.deliveries.status.failed';
    case 'dead':
      return 'settings.webhooks.deliveries.status.dead';
    default:
      return 'settings.webhooks.deliveries.status.pending';
  }
}

/** Format a unix-second timestamp using the active locale. */
function formatTimestamp(epochSec: number | undefined | null, locale: string): string {
  if (!epochSec) return '—';
  try {
    return new Intl.DateTimeFormat(locale, {
      dateStyle: 'medium',
      timeStyle: 'short',
    }).format(new Date(epochSec * 1000));
  } catch {
    return String(epochSec);
  }
}

interface WebhookRowProps {
  webhook: Webhook;
  togglePending: boolean;
  onToggle: (webhook: Webhook, next: boolean) => void;
  onDelete: (webhook: Webhook) => void;
  onTest: (webhook: Webhook) => void;
  onShowSecret: (webhook: Webhook) => void;
  onViewDeliveries: (webhook: Webhook) => void;
}

/** Single row in the webhooks table. */
function WebhookRow({
  webhook,
  togglePending,
  onToggle,
  onDelete,
  onTest,
  onShowSecret,
  onViewDeliveries,
}: WebhookRowProps): ReactElement {
  const { t } = useTranslation('settings');
  const [menuOpen, setMenuOpen] = useState(false);
  const eventTypes = normaliseEventTypes(webhook.eventTypes);

  const handleMenuItem = (action: () => void): void => {
    setMenuOpen(false);
    action();
  };

  return (
    <tr>
      <td className={styles.urlCell} title={webhook.url}>
        {webhook.url}
      </td>
      <td className={styles.descriptionCell} title={webhook.description}>
        {webhook.description}
      </td>
      <td>
        <div className={styles.eventTypesCell}>
          {eventTypes.length === 0 ? (
            <span className={styles.eventTypesList}>{'—'}</span>
          ) : (
            eventTypes.map((eventType) => (
              <Chip key={eventType} tone="neutral">
                {eventType}
              </Chip>
            ))
          )}
        </div>
      </td>
      <td className={styles.activeCell}>
        <Switch
          checked={webhook.isActive}
          disabled={togglePending}
          onCheckedChange={(next) => {
            onToggle(webhook, next);
          }}
          aria-label={
            webhook.isActive
              ? t('settings.webhooks.toggle.deactivate')
              : t('settings.webhooks.toggle.activate')
          }
        />
      </td>
      <td className={styles.actionsCell}>
        <Popover
          open={menuOpen}
          onOpenChange={setMenuOpen}
          placement="bottom-end"
          content={
            <div
              role="menu"
              aria-label={t('settings.webhooks.action.menu_label')}
              className={styles.menuList}
            >
              <button
                type="button"
                role="menuitem"
                className={styles.menuItem}
                onClick={() => {
                  handleMenuItem(() => {
                    onViewDeliveries(webhook);
                  });
                }}
              >
                {t('settings.webhooks.action.deliveries')}
              </button>
              <button
                type="button"
                role="menuitem"
                className={styles.menuItem}
                onClick={() => {
                  handleMenuItem(() => {
                    onTest(webhook);
                  });
                }}
              >
                {t('settings.webhooks.action.test')}
              </button>
              <button
                type="button"
                role="menuitem"
                className={styles.menuItem}
                onClick={() => {
                  handleMenuItem(() => {
                    onShowSecret(webhook);
                  });
                }}
              >
                {t('settings.webhooks.action.show_secret')}
              </button>
              <button
                type="button"
                role="menuitem"
                className={`${styles.menuItem} ${styles.menuItemDanger}`}
                onClick={() => {
                  handleMenuItem(() => {
                    onDelete(webhook);
                  });
                }}
              >
                {t('settings.webhooks.action.delete')}
              </button>
            </div>
          }
        >
          <button
            type="button"
            aria-label={t('settings.webhooks.action.menu_label')}
            aria-haspopup="menu"
            aria-expanded={menuOpen}
            className={styles.menuTrigger}
          >
            <MoreHorizontal size={16} aria-hidden />
          </button>
        </Popover>
      </td>
    </tr>
  );
}

interface CreateDialogProps {
  workspaceId: string;
  open: boolean;
  onClose: () => void;
}

type CreateStage = 'form' | 'reveal';

/**
 * Two-stage create dialog. Stage 1 collects url / description /
 * eventTypes (comma separated) and submits. Stage 2 renders the signing
 * secret with a Copy affordance; the same secret is also retrievable
 * later via the Show secret menu action so callers do not have to
 * memorise it from this dialog.
 */
function CreateWebhookDialog({ workspaceId, open, onClose }: CreateDialogProps): ReactElement {
  const { t } = useTranslation('settings');
  const create = useCreateWebhookMutation();

  const [stage, setStage] = useState<CreateStage>('form');
  const [url, setUrl] = useState('');
  const [description, setDescription] = useState('');
  const [eventTypesText, setEventTypesText] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [secret, setSecret] = useState('');
  const [copied, setCopied] = useState(false);

  const reset = (): void => {
    setStage('form');
    setUrl('');
    setDescription('');
    setEventTypesText('');
    setSubmitting(false);
    setSecret('');
    setCopied(false);
  };

  const handleClose = (): void => {
    reset();
    onClose();
  };

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault();
    if (url.trim() === '' || description.trim() === '') return;

    const eventTypes = eventTypesText
      .split(',')
      .map((s) => s.trim())
      .filter((s) => s.length > 0);

    setSubmitting(true);
    create.mutate(
      {
        wsId: workspaceId,
        body: {
          url: url.trim(),
          description: description.trim(),
          eventTypes,
        },
      },
      {
        onSuccess: (detail) => {
          setSecret(detail.secret);
          setStage('reveal');
          setSubmitting(false);
        },
        onError: (err) => {
          setSubmitting(false);
          // A transport failure is an ApiError too, but its message is
          // the English literal the requester was given, not anything a
          // server said — so it takes the translated line.
          const message =
            !isNetworkError(err) && err instanceof ApiError && err.message
              ? err.message
              : t('settings.webhooks.create.error');
          toaster.show({ tone: 'danger', message });
        },
      },
    );
  };

  const handleCopy = async (): Promise<void> => {
    if (secret === '') return;
    try {
      await navigator.clipboard.writeText(secret);
      setCopied(true);
      toaster.show({
        tone: 'success',
        message: t('settings.webhooks.create.secret.copied'),
      });
    } catch {
      // error-toast-exempt: the clipboard write never reaches the API. What it
      // rejects with is a browser DOMException whose message is untranslated UA
      // text, so surfacing it would be worse than the localized fallback.
      toaster.show({
        tone: 'danger',
        message: t('settings.webhooks.create.secret.copy_failed'),
      });
    }
  };

  const submitDisabled = submitting || url.trim() === '' || description.trim() === '';

  const title =
    stage === 'form'
      ? t('settings.webhooks.create.title')
      : t('settings.webhooks.create.success.title');

  return (
    <Dialog open={open} onClose={handleClose} title={title}>
      {stage === 'form' ? (
        <form className={styles.dialogForm} onSubmit={handleSubmit}>
          <FormField label={t('settings.webhooks.create.url.label')} required>
            {(control) => (
              <Input
                {...control}
                type="url"
                value={url}
                placeholder={t('settings.webhooks.create.url.placeholder')}
                onChange={(e: ChangeEvent<HTMLInputElement>) => {
                  setUrl(e.target.value);
                }}
              />
            )}
          </FormField>
          <FormField label={t('settings.webhooks.create.description.label')} required>
            {(control) => (
              <Input
                {...control}
                value={description}
                placeholder={t('settings.webhooks.create.description.placeholder')}
                onChange={(e: ChangeEvent<HTMLInputElement>) => {
                  setDescription(e.target.value);
                }}
              />
            )}
          </FormField>
          <FormField
            label={t('settings.webhooks.create.event_types.label')}
            description={t('settings.webhooks.create.event_types.hint')}
          >
            {(control) => (
              <Textarea
                {...control}
                value={eventTypesText}
                rows={2}
                onChange={(e: ChangeEvent<HTMLTextAreaElement>) => {
                  setEventTypesText(e.target.value);
                }}
              />
            )}
          </FormField>
          <div className={styles.dialogActions}>
            <Button type="button" variant="ghost" onClick={handleClose}>
              {t('settings.webhooks.create.cancel')}
            </Button>
            <Button type="submit" variant="primary" disabled={submitDisabled}>
              {t('settings.webhooks.create.submit')}
            </Button>
          </div>
        </form>
      ) : (
        <div className={styles.dialogForm}>
          <div className={styles.secretPanel}>
            <p className={styles.secretWarning}>{t('settings.webhooks.create.secret.warning')}</p>
            <FormField label={t('settings.webhooks.create.secret.label')}>
              {(control) => (
                <Input
                  {...control}
                  className={styles.secretValue}
                  value={secret}
                  readOnly
                  spellCheck={false}
                  onFocus={(e) => {
                    e.currentTarget.select();
                  }}
                />
              )}
            </FormField>
            <div className={styles.secretRow}>
              <Button
                type="button"
                variant="ghost"
                onClick={() => {
                  void handleCopy();
                }}
              >
                {copied
                  ? t('settings.webhooks.create.secret.copied')
                  : t('settings.webhooks.create.secret.copy')}
              </Button>
            </div>
          </div>
          <div className={styles.dialogActions}>
            <Button type="button" variant="primary" onClick={handleClose}>
              {t('settings.webhooks.create.done')}
            </Button>
          </div>
        </div>
      )}
    </Dialog>
  );
}

interface ShowSecretDialogProps {
  workspaceId: string;
  webhookId: string;
  open: boolean;
  onClose: () => void;
}

/**
 * Lightweight dialog that fetches the detail (which includes the signing
 * secret) and renders it with a Copy affordance. No mask / reveal toggle:
 * the API surfaces the secret freely on every detail GET.
 */
function ShowSecretDialog({
  workspaceId,
  webhookId,
  open,
  onClose,
}: ShowSecretDialogProps): ReactElement {
  const { t } = useTranslation('settings');
  const { t: tCommon } = useTranslation('common');
  const detailQuery = useWebhookQuery(workspaceId, open ? webhookId : '');
  const [copied, setCopied] = useState(false);

  const handleClose = (): void => {
    setCopied(false);
    onClose();
  };

  const handleCopy = async (): Promise<void> => {
    const secret = detailQuery.data?.secret;
    if (!secret) return;
    try {
      await navigator.clipboard.writeText(secret);
      setCopied(true);
      toaster.show({
        tone: 'success',
        message: t('settings.webhooks.create.secret.copied'),
      });
    } catch {
      // error-toast-exempt: the clipboard write never reaches the API. What it
      // rejects with is a browser DOMException whose message is untranslated UA
      // text, so surfacing it would be worse than the localized fallback.
      toaster.show({
        tone: 'danger',
        message: t('settings.webhooks.create.secret.copy_failed'),
      });
    }
  };

  return (
    <Dialog open={open} onClose={handleClose} title={t('settings.webhooks.show_secret.title')}>
      <div className={styles.dialogForm}>
        {detailQuery.isLoading || !detailQuery.data ? (
          <div className={styles.spinnerRow}>
            <Spinner label={tCommon('common.loading')} />
          </div>
        ) : (
          <div className={styles.secretPanel}>
            <FormField label={t('settings.webhooks.create.secret.label')}>
              {(control) => (
                <Input
                  {...control}
                  className={styles.secretValue}
                  value={detailQuery.data.secret}
                  readOnly
                  spellCheck={false}
                  onFocus={(e) => {
                    e.currentTarget.select();
                  }}
                />
              )}
            </FormField>
            <div className={styles.secretRow}>
              <Button
                type="button"
                variant="ghost"
                onClick={() => {
                  void handleCopy();
                }}
              >
                {copied
                  ? t('settings.webhooks.create.secret.copied')
                  : t('settings.webhooks.create.secret.copy')}
              </Button>
            </div>
          </div>
        )}
        <div className={styles.dialogActions}>
          <Button type="button" variant="primary" onClick={handleClose}>
            {t('settings.webhooks.show_secret.close')}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

interface DeliveriesDrawerProps {
  workspaceId: string;
  webhookId: string;
  open: boolean;
  onClose: () => void;
}

interface DeliveryItemProps {
  delivery: WebhookDelivery;
  locale: string;
}

/** Render a single delivery row inside the drawer. */
function DeliveryItem({ delivery, locale }: DeliveryItemProps): ReactElement {
  const { t } = useTranslation('settings');
  const ts = delivery.deliveredAt ?? delivery.failedAt ?? delivery.createdAt;
  return (
    <li className={styles.deliveryItem}>
      <div className={styles.deliveryHeader}>
        <Badge tone={deliveryTone(delivery.status)}>{t(deliveryStatusKey(delivery.status))}</Badge>
        <code className={styles.deliveryEvent}>{delivery.eventType}</code>
        <span className={styles.deliveryTimestamp}>{formatTimestamp(ts, locale)}</span>
      </div>
      <div className={styles.deliveryMeta}>
        <span>
          {t('settings.webhooks.deliveries.col.http')} {delivery.httpStatus ?? '—'}
        </span>
        <span>
          {t('settings.webhooks.deliveries.col.attempts')} {delivery.attempts}/
          {delivery.maxAttempts}
        </span>
      </div>
    </li>
  );
}

/**
 * Side drawer that lists the delivery history for a webhook subscription.
 * The list is rendered as a plain `<ul>` rather than a virtualised list:
 * the deliveries endpoint returns at most ~100 rows by default and the
 * @tanstack/react-virtual integration is reserved for the much larger
 * task spreadsheet view. Future enhancement: swap to `useVirtualizer`
 * once the deliveries endpoint supports cursor pagination beyond the
 * default page.
 */
function DeliveriesDrawer({
  workspaceId,
  webhookId,
  open,
  onClose,
}: DeliveriesDrawerProps): ReactElement {
  const { t, i18n } = useTranslation('settings');
  const { t: tCommon } = useTranslation('common');
  const locale = i18n.resolvedLanguage ?? 'en';
  const deliveriesQuery = useDeliveriesQuery(workspaceId, open ? webhookId : '');

  return (
    <Drawer
      open={open}
      onClose={onClose}
      title={t('settings.webhooks.deliveries.title')}
      side="inline-end"
    >
      {deliveriesQuery.isLoading ? (
        <div className={styles.spinnerRow}>
          <Spinner label={tCommon('common.loading')} />
        </div>
      ) : (deliveriesQuery.data ?? []).length === 0 ? (
        <p className={styles.deliveriesEmpty}>{t('settings.webhooks.deliveries.empty')}</p>
      ) : (
        <ul className={styles.deliveriesList}>
          {(deliveriesQuery.data ?? []).map((delivery) => (
            <DeliveryItem key={delivery.id} delivery={delivery} locale={locale} />
          ))}
        </ul>
      )}
    </Drawer>
  );
}

/** Page component mounted by the lazy route. */
export default function WebhooksSettingsPage(): ReactElement {
  const { t } = useTranslation('settings');
  const { t: tCommon } = useTranslation('common');
  const { id: workspaceId } = routeApi.useParams();

  const webhooksQuery = useWebhooksQuery(workspaceId);
  const toggle = useToggleWebhookMutation();
  const del = useDeleteWebhookMutation();
  const test = useTestWebhookMutation();

  const [createOpen, setCreateOpen] = useState(false);
  const [secretWebhookId, setSecretWebhookId] = useState<string | null>(null);
  const [deliveriesWebhookId, setDeliveriesWebhookId] = useState<string | null>(null);

  const webhooks = webhooksQuery.data ?? [];

  const handleToggle = (webhook: Webhook, next: boolean): void => {
    toggle.mutate(
      { wsId: workspaceId, webhookId: webhook.id, isActive: next },
      {
        onSuccess: () => {
          toaster.show({
            tone: 'success',
            message: next
              ? t('settings.webhooks.toggle.activate')
              : t('settings.webhooks.toggle.deactivate'),
          });
        },
        onError: (err) => {
          toaster.show({
            tone: 'danger',
            message: formatApiError(err, t, 'settings.webhooks.toggle.error'),
          });
        },
      },
    );
  };

  const handleDelete = (webhook: Webhook): void => {
    void (async (): Promise<void> => {
      const ok = await confirmAction({
        title: t('settings.webhooks.delete.confirm.title'),
        message: t('settings.webhooks.delete.confirm.message'),
        tone: 'danger',
      });
      if (!ok) return;
      del.mutate(
        { wsId: workspaceId, webhookId: webhook.id },
        {
          onSuccess: () => {
            toaster.show({
              tone: 'success',
              message: t('settings.webhooks.delete.success'),
            });
          },
          onError: (err) => {
            toaster.show({
              tone: 'danger',
              message: formatApiError(err, t, 'settings.webhooks.delete.error'),
            });
          },
        },
      );
    })();
  };

  const handleTest = (webhook: Webhook): void => {
    test.mutate(
      { wsId: workspaceId, webhookId: webhook.id },
      {
        onSuccess: ({ deliveryId }) => {
          toaster.show({
            tone: 'success',
            message: t('settings.webhooks.test.success', { id: deliveryId }),
          });
          setDeliveriesWebhookId(webhook.id);
        },
        onError: (err) => {
          toaster.show({
            tone: 'danger',
            message: formatApiError(err, t, 'settings.webhooks.test.error'),
          });
        },
      },
    );
  };

  const handleShowSecret = (webhook: Webhook): void => {
    setSecretWebhookId(webhook.id);
  };

  const handleViewDeliveries = (webhook: Webhook): void => {
    setDeliveriesWebhookId(webhook.id);
  };

  return (
    <section className={styles.page}>
      <div className={styles.header}>
        <div className={styles.headerText}>
          <h1 className={styles.title}>{t('settings.webhooks.title')}</h1>
          <p className={styles.description}>{t('settings.webhooks.description')}</p>
        </div>
        <Button
          type="button"
          variant="primary"
          onClick={() => {
            setCreateOpen(true);
          }}
        >
          {t('settings.webhooks.new')}
        </Button>
      </div>

      <Card>
        <section className={styles.section}>
          {webhooksQuery.isLoading ? (
            <div className={styles.spinnerRow}>
              <Spinner label={tCommon('common.loading')} />
            </div>
          ) : webhooks.length === 0 ? (
            <p className={styles.deliveriesEmpty}>{t('settings.webhooks.empty')}</p>
          ) : (
            <div className={styles.tableWrap}>
              <table className={styles.table}>
                <thead>
                  <tr>
                    <th>{t('settings.webhooks.col.url')}</th>
                    <th>{t('settings.webhooks.col.description')}</th>
                    <th>{t('settings.webhooks.col.event_types')}</th>
                    <th>{t('settings.webhooks.col.active')}</th>
                    <th className={styles.actionsCell}>{t('settings.webhooks.col.actions')}</th>
                  </tr>
                </thead>
                <tbody>
                  {webhooks.map((webhook) => (
                    <WebhookRow
                      key={webhook.id}
                      webhook={webhook}
                      togglePending={toggle.isPending}
                      onToggle={handleToggle}
                      onDelete={handleDelete}
                      onTest={handleTest}
                      onShowSecret={handleShowSecret}
                      onViewDeliveries={handleViewDeliveries}
                    />
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      </Card>

      <CreateWebhookDialog
        workspaceId={workspaceId}
        open={createOpen}
        onClose={() => {
          setCreateOpen(false);
        }}
      />

      <ShowSecretDialog
        workspaceId={workspaceId}
        webhookId={secretWebhookId ?? ''}
        open={secretWebhookId !== null}
        onClose={() => {
          setSecretWebhookId(null);
        }}
      />

      <DeliveriesDrawer
        workspaceId={workspaceId}
        webhookId={deliveriesWebhookId ?? ''}
        open={deliveriesWebhookId !== null}
        onClose={() => {
          setDeliveriesWebhookId(null);
        }}
      />
    </section>
  );
}
