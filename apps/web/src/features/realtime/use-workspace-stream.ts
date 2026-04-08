/**
 * use-workspace-stream — realtime SSE subscription for a single
 * workspace (ADR 0005).
 *
 * Mounts once per active workspace near the authenticated tree root.
 * Opens a long-lived fetch-based SSE connection to
 * `GET /workspaces/{wsId}/stream`, parses the event stream line by
 * line, and calls `queryClient.invalidateQueries` for every key
 * prefix listed in `event-to-keys.ts`.
 *
 * No feature hook needs to know SSE exists: `useAutoActionsQuery`,
 * `useRemindersQuery`, `useStateSuggestionsQuery`,
 * `useAiSuggestionsQuery` and friends keep their existing shape and
 * simply lose their `refetchInterval` polling. If the stream has
 * been disconnected for longer than `FALLBACK_AFTER_MS`, the hook
 * flips `streamHealthyStore.healthy` to `false` and the affected
 * hooks re-enable polling as a safety net.
 */

import { useQueryClient } from '@tanstack/react-query';
import { useEffect } from 'react';

import { apiBaseUrl } from '../../lib/sdk';
import { authStore } from '../auth/auth-store';
import { type StreamEvent, keysForEvent } from './event-to-keys';
import { setStreamHealthy } from './stream-health';

const RECONNECT_BASE_MS = 1_000;
const RECONNECT_MAX_MS = 30_000;
const FALLBACK_AFTER_MS = 90_000;

/**
 * useWorkspaceStream opens (and cleans up) a realtime SSE
 * subscription for the given workspace. Pass `undefined` to disable
 * the subscription (unauthenticated tree, no active workspace).
 */
export function useWorkspaceStream(workspaceId: string | undefined): void {
  const queryClient = useQueryClient();

  useEffect(() => {
    if (!workspaceId) {
      setStreamHealthy(true);
      return;
    }

    const controller = new AbortController();
    let cancelled = false;
    let reconnectDelayMs = RECONNECT_BASE_MS;
    let fallbackTimer: ReturnType<typeof setTimeout> | null = null;

    const armFallback = (): void => {
      if (fallbackTimer) clearTimeout(fallbackTimer);
      fallbackTimer = setTimeout(() => {
        setStreamHealthy(false);
      }, FALLBACK_AFTER_MS);
    };

    const clearFallback = (): void => {
      if (fallbackTimer) {
        clearTimeout(fallbackTimer);
        fallbackTimer = null;
      }
      setStreamHealthy(true);
    };

    const handleEvent = (evt: StreamEvent): void => {
      for (const key of keysForEvent(evt)) {
        void queryClient.invalidateQueries({ queryKey: key });
      }
    };

    const parseFrame = (frame: string): StreamEvent | null => {
      let dataLine = '';
      for (const line of frame.split('\n')) {
        if (line.startsWith('data:')) {
          dataLine = line.slice(5).trim();
          break;
        }
      }
      if (!dataLine) return null;
      try {
        const parsed = JSON.parse(dataLine) as StreamEvent;
        return parsed;
      } catch {
        return null;
      }
    };

    const connect = async (): Promise<void> => {
      while (!cancelled) {
        const token = authStore.getState().accessToken;
        if (!token) {
          // No access token yet. Wait a beat and retry.
          await sleep(reconnectDelayMs);
          reconnectDelayMs = Math.min(reconnectDelayMs * 2, RECONNECT_MAX_MS);
          continue;
        }

        armFallback();
        try {
          const res = await fetch(`${apiBaseUrl}/workspaces/${workspaceId}/stream`, {
            method: 'GET',
            headers: new Headers([
              ['accept', 'text/event-stream'],
              ['authorization', `Bearer ${token}`],
            ]),
            credentials: 'include',
            signal: controller.signal,
          });

          if (!res.ok || !res.body) {
            throw new Error(`stream: status ${res.status}`);
          }

          clearFallback();
          reconnectDelayMs = RECONNECT_BASE_MS;

          const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
          let buffer = '';
          while (!cancelled) {
            const { value, done } = await reader.read();
            if (done) break;
            buffer += value;
            // SSE frames are separated by a blank line.
            let sepIndex = buffer.indexOf('\n\n');
            while (sepIndex !== -1) {
              const frame = buffer.slice(0, sepIndex);
              buffer = buffer.slice(sepIndex + 2);
              if (!frame.startsWith(':')) {
                const evt = parseFrame(frame);
                if (evt) handleEvent(evt);
              }
              sepIndex = buffer.indexOf('\n\n');
            }
          }
        } catch (err) {
          if (cancelled) return;
          // Swallow fetch aborts and network blips — the retry loop
          // will reconnect with backoff.
          void err;
        }

        if (cancelled) return;
        await sleep(reconnectDelayMs);
        reconnectDelayMs = Math.min(reconnectDelayMs * 2, RECONNECT_MAX_MS);
      }
    };

    void connect();

    return () => {
      cancelled = true;
      controller.abort();
      if (fallbackTimer) clearTimeout(fallbackTimer);
      setStreamHealthy(true);
    };
  }, [workspaceId, queryClient]);
}

function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
