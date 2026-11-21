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

import { usePresenceList } from '@nodate-flow/ui/hooks/use-presence-list';
import Badge, { type BadgeTone } from '@nodate-flow/ui/primitives/badge';
import Button from '@nodate-flow/ui/primitives/button';
import Chip from '@nodate-flow/ui/primitives/chip';
import Dialog from '@nodate-flow/ui/primitives/dialog';
import Drawer from '@nodate-flow/ui/primitives/drawer';
import EmptyState from '@nodate-flow/ui/primitives/empty-state';
import FormField from '@nodate-flow/ui/primitives/form-field';
import Input from '@nodate-flow/ui/primitives/input';
import Popover from '@nodate-flow/ui/primitives/popover';
import PresenceRow from '@nodate-flow/ui/primitives/presence-row';
import Skeleton from '@nodate-flow/ui/primitives/skeleton';
import Switch from '@nodate-flow/ui/primitives/switch';
import Textarea from '@nodate-flow/ui/primitives/textarea';
import { toaster } from '@nodate-flow/ui/primitives/toast';
import Tooltip from '@nodate-flow/ui/primitives/tooltip';
import { getRouteApi } from '@tanstack/react-router';
import { MoreHorizontal } from 'lucide-react';
import {
  type ButtonHTMLAttributes,
  type ChangeEvent,
  type FormEvent,
  type ReactElement,
  type Ref,
  useState,
} from 'react';
import { useTranslation } from 'react-i18next';
import PageHeader from '../../components/page-header';
import { ApiError, formatApiError, isNetworkError } from '../../lib/api-error';
import { confirmAction } from '../../lib/confirm-action';
import ColumnHelp from '../settings/column-help';
import SecretCallout from '../settings/secret-callout';
import ui from '../settings/settings-ui.module.css';
import TruncatedText from '../settings/truncated-text';
import AccessRestricted from '../workspaces/access-restricted';
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

/** The server's wildcard: subscribe to every event kind. */
const EVENT_TYPE_WILDCARD = '*';

/**
 * Client-side mirror of the server rule: at least one entry, each either
 * `*` alone or an exact event name. A pattern such as `task.*` is refused.
 */
function eventTypesError(
  entries: readonly string[],
):
  | 'settings.webhooks.create.event_types.required'
  | 'settings.webhooks.create.event_types.no_patterns'
  | null {
  if (entries.length === 0) return 'settings.webhooks.create.event_types.required';
  if (entries.some((e) => e !== EVENT_TYPE_WILDCARD && e.includes(EVENT_TYPE_WILDCARD))) {
    return 'settings.webhooks.create.event_types.no_patterns';
  }
  return null;
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

type MenuTriggerProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  ref?: Ref<HTMLElement>;
  label: string;
};

/**
 * The popover clones its child with a ref and interaction props; the tooltip
 * needs the button's own ref, so the popover anchors on a wrapping span.
 */
function MenuTrigger({ ref, label, ...buttonProps }: MenuTriggerProps): ReactElement {
  return (
    <span ref={ref} className={styles.menuAnchor}>
      <Tooltip content={label}>
        <button
          type="button"
          aria-label={label}
          aria-haspopup="menu"
          className={styles.menuTrigger}
          {...buttonProps}
        >
          <MoreHorizontal size={16} aria-hidden />
        </button>
      </Tooltip>
    </span>
  );
}

interface WebhookRowProps {
  webhook: Webhook;
  /** The state the switch shows: an in-flight toggle's target, else the stored value. */
  active: boolean;
  togglePending: boolean;
  /** True for a row that arrived after the list first painted. */
  arrived: boolean;
  /** False once the webhook is gone and the row is playing its exit. */
  present: boolean;
  onExited: () => void;
  onToggle: (webhook: Webhook, next: boolean) => void;
  onDelete: (webhook: Webhook) => void;
  onTest: (webhook: Webhook) => void;
  onShowSecret: (webhook: Webhook) => void;
  onViewDeliveries: (webhook: Webhook) => void;
}

/** Single row in the webhooks table. */
function WebhookRow({
  webhook,
  active,
  togglePending,
  arrived,
  present,
  onExited,
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
    <PresenceRow present={present} onExited={onExited} arrived={arrived}>
      <td>
        <TruncatedText text={webhook.url} className={`${styles.urlCell} ${ui.mono}`} />
      </td>
      <td>
        <TruncatedText text={webhook.description} className={styles.descriptionCell} />
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
          checked={active}
          disabled={togglePending}
          aria-busy={togglePending}
          onCheckedChange={(next) => {
            onToggle(webhook, next);
          }}
          aria-label={
            active
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
          <MenuTrigger label={t('settings.webhooks.action.menu_label')} aria-expanded={menuOpen} />
        </Popover>
      </td>
    </PresenceRow>
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
  const [eventTypesInvalid, setEventTypesInvalid] =
    useState<ReturnType<typeof eventTypesError>>(null);

  const reset = (): void => {
    setStage('form');
    setUrl('');
    setDescription('');
    setEventTypesText('');
    setSubmitting(false);
    setSecret('');
    setEventTypesInvalid(null);
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
    // The server refuses these anyway; saying so on the field beats a toast.
    const invalid = eventTypesError(eventTypes);
    if (invalid !== null) {
      setEventTypesInvalid(invalid);
      return;
    }

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

  const submitDisabled = submitting || url.trim() === '' || description.trim() === '';

  const title =
    stage === 'form'
      ? t('settings.webhooks.create.title')
      : t('settings.webhooks.create.success.title');

  return (
    <Dialog open={open} onClose={handleClose} title={title}>
      {stage === 'form' ? (
        <form className={styles.dialogForm} onSubmit={handleSubmit}>
          <FormField
            label={t('settings.webhooks.create.url.label')}
            description={t('settings.webhooks.create.url.hint')}
            required
          >
            {(control) => (
              <Input
                {...control}
                type="url"
                inputMode="url"
                autoComplete="off"
                spellCheck={false}
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
                autoComplete="off"
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
            required
            {...(eventTypesInvalid ? { error: t(eventTypesInvalid) } : {})}
          >
            {(control) => (
              <Textarea
                {...control}
                value={eventTypesText}
                rows={2}
                spellCheck={false}
                onChange={(e: ChangeEvent<HTMLTextAreaElement>) => {
                  setEventTypesText(e.target.value);
                  setEventTypesInvalid(null);
                }}
              />
            )}
          </FormField>
          <div className={styles.dialogActions}>
            <Button type="button" variant="ghost" onClick={handleClose}>
              {t('settings.webhooks.create.cancel')}
            </Button>
            <Button
              type="submit"
              variant="primary"
              disabled={submitDisabled}
              aria-busy={submitting}
            >
              {t('settings.webhooks.create.submit')}
            </Button>
          </div>
        </form>
      ) : (
        <div className={styles.dialogForm}>
          <SecretField secret={secret} message={t('settings.webhooks.create.secret.warning')} />
          <p className={styles.signingHint}>{t('settings.webhooks.create.secret.hint')}</p>
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

/**
 * The signing secret in the shared secret callout. It stays retrievable
 * through Show secret, so the callout asks to keep it private rather than
 * claiming it is shown once.
 */
function SecretField({ secret, message }: { secret: string; message: string }): ReactElement {
  const { t } = useTranslation('settings');
  return (
    <SecretCallout
      kind="keep"
      message={message}
      label={t('settings.webhooks.create.secret.label')}
      value={secret}
      copyLabel={t('settings.webhooks.create.secret.copy')}
      copiedLabel={t('settings.webhooks.create.secret.copied')}
      onCopyError={() => {
        // error-toast-exempt: the clipboard write never reaches the API. What it
        // rejects with is a browser DOMException whose message is untranslated UA
        // text, so surfacing it would be worse than the localized fallback.
        toaster.show({
          tone: 'danger',
          message: t('settings.webhooks.create.secret.copy_failed'),
        });
      }}
    />
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
  const detailQuery = useWebhookQuery(workspaceId, open ? webhookId : '');

  const handleClose = (): void => {
    onClose();
  };

  return (
    <Dialog open={open} onClose={handleClose} title={t('settings.webhooks.show_secret.title')}>
      <div className={styles.dialogForm}>
        {detailQuery.isLoading || !detailQuery.data ? (
          <Skeleton className={styles.skeletonSecret} aria-hidden="true" />
        ) : (
          <SecretField
            secret={detailQuery.data.secret}
            message={t('settings.webhooks.create.secret.hint')}
          />
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
  const locale = i18n.resolvedLanguage ?? 'en';
  const deliveriesQuery = useDeliveriesQuery(workspaceId, open ? webhookId : '');

  return (
    <Drawer
      open={open}
      onClose={onClose}
      title={t('settings.webhooks.deliveries.title')}
      side="inline-end"
    >
      <p className={styles.deliveriesNote}>{t('settings.webhooks.deliveries.status_note')}</p>
      {deliveriesQuery.isLoading ? (
        <div className={ui.skeletonStack} aria-hidden="true">
          <Skeleton className={styles.skeletonDelivery} />
          <Skeleton className={styles.skeletonDelivery} />
          <Skeleton className={styles.skeletonDelivery} />
        </div>
      ) : (deliveriesQuery.data ?? []).length === 0 ? (
        <EmptyState
          titleAs="p"
          title={t('settings.webhooks.deliveries.empty')}
          className={ui.empty}
        />
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
  const { id: workspaceId } = routeApi.useParams();

  const webhooksQuery = useWebhooksQuery(workspaceId);
  const toggle = useToggleWebhookMutation();
  const del = useDeleteWebhookMutation();
  const test = useTestWebhookMutation();

  const [createOpen, setCreateOpen] = useState(false);
  const [secretWebhookId, setSecretWebhookId] = useState<string | null>(null);
  const [deliveriesWebhookId, setDeliveriesWebhookId] = useState<string | null>(null);

  // Switch targets held until the refetched list agrees, so a toggle
  // reads as done on click rather than after the round trip.
  const [pendingActive, setPendingActive] = useState<Readonly<Record<string, boolean>>>({});
  // Webhooks present when the list first loaded; anything else fades in as it arrives.
  const [initialIds, setInitialIds] = useState<ReadonlySet<string> | null>(null);

  const webhooks = webhooksQuery.data ?? [];

  if (initialIds === null && webhooksQuery.data !== undefined) {
    setInitialIds(new Set(webhooksQuery.data.map((webhook) => webhook.id)));
  }
  // Derived-state update during render: drop targets the server now reports.
  const settledIds = webhooks
    .filter((webhook) => pendingActive[webhook.id] === webhook.isActive && !toggle.isPending)
    .map((webhook) => webhook.id);
  if (settledIds.length > 0) {
    setPendingActive((prev) => {
      const next = { ...prev };
      for (const id of settledIds) delete next[id];
      return next;
    });
  }

  const handleToggle = (webhook: Webhook, next: boolean): void => {
    setPendingActive((prev) => ({ ...prev, [webhook.id]: next }));
    toggle.mutate(
      { wsId: workspaceId, webhookId: webhook.id, isActive: next },
      {
        onError: (err) => {
          setPendingActive((prev) => {
            const rest = { ...prev };
            delete rest[webhook.id];
            return rest;
          });
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
        confirmLabel: t('settings.webhooks.action.delete'),
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

  const togglingId = toggle.isPending ? toggle.variables?.webhookId : undefined;
  const { entries, onExited } = usePresenceList(webhooks, (webhook) => webhook.id);

  const openCreate = (): void => {
    setCreateOpen(true);
  };

  const error = webhooksQuery.error;
  if (error instanceof ApiError && (error.httpStatus === 401 || error.httpStatus === 403)) {
    return <AccessRestricted sectionTitleKey="nav.webhooks" />;
  }

  return (
    <div className={ui.page}>
      <PageHeader
        title={t('settings.webhooks.title')}
        description={t('settings.webhooks.description')}
        help="workspace-settings-webhooks"
        actions={
          <Button type="button" variant="primary" onClick={openCreate}>
            {t('settings.webhooks.new')}
          </Button>
        }
      />

      {webhooksQuery.isPending ? (
        <div className={ui.skeletonStack} aria-hidden="true">
          <Skeleton className={styles.skeletonRow} />
          <Skeleton className={styles.skeletonRow} />
          <Skeleton className={styles.skeletonRow} />
        </div>
      ) : webhooksQuery.isError ? (
        <EmptyState
          titleAs="p"
          title={t('settings.webhooks.load_error')}
          className={ui.empty}
          action={
            <Button
              type="button"
              size="sm"
              onClick={() => {
                void webhooksQuery.refetch();
              }}
              aria-busy={webhooksQuery.isFetching}
            >
              {t('settings.webhooks.retry')}
            </Button>
          }
        />
      ) : webhooks.length === 0 ? (
        <EmptyState
          title={t('settings.webhooks.empty')}
          description={t('settings.webhooks.empty_hint')}
          className={ui.empty}
          action={
            <Button type="button" onClick={openCreate}>
              {t('settings.webhooks.new')}
            </Button>
          }
        />
      ) : (
        <div className={ui.tableWrap}>
          <table className={ui.table}>
            <thead>
              <tr>
                <th>{t('settings.webhooks.col.url')}</th>
                <th>{t('settings.webhooks.col.description')}</th>
                <th>{t('settings.webhooks.col.event_types')}</th>
                <th>
                  {t('settings.webhooks.col.active')}
                  <ColumnHelp text={t('settings.webhooks.col.active_help')} />
                </th>
                <th className={ui.alignEnd}>{t('settings.webhooks.col.actions')}</th>
              </tr>
            </thead>
            <tbody>
              {entries.map(({ key, item: webhook, present }) => (
                <WebhookRow
                  key={key}
                  webhook={webhook}
                  present={present}
                  onExited={() => {
                    onExited(key);
                  }}
                  active={pendingActive[webhook.id] ?? webhook.isActive}
                  togglePending={togglingId === webhook.id}
                  arrived={initialIds !== null && !initialIds.has(webhook.id)}
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
    </div>
  );
}
