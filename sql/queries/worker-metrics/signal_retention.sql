-- name: DeleteExpiredUnappliedSignals :execrows
-- Retention sweep (ADR 0008 D1/D8): delete one batch of signals whose
-- expires_at has passed and which the Applier never applied. Applied rows
-- and rows with no expiry (expires_at NULL) are never touched. A row an
-- event or an intake item still cites is kept: both FKs are ON DELETE SET
-- NULL, and the event log is append-only. The caller repeats until a batch
-- deletes nothing; the fixed LIMIT bounds each statement's lock footprint
-- on idx_signals_expires.
DELETE FROM signals
WHERE expires_at < ?
  AND applied_at IS NULL
  AND NOT EXISTS (
    SELECT 1 FROM events e WHERE e.triggered_by_signal_id = signals.id
  )
  AND NOT EXISTS (
    SELECT 1 FROM intake_items i WHERE i.signal_id = signals.id
  )
ORDER BY expires_at
LIMIT 1000;
