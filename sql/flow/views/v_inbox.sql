-- v_inbox
-- Aggregated inbox: signals that have arrived in a workspace with optional
-- task linkage. Thin filter over v_inbox_all; column definitions live there.
-- Future received_at values are snoozed until their deadline; keeping that
-- rule here aligns workspace and cross-workspace inbox reads.
-- The calendar source is reserved for internal scheduler ticks: the judge
-- reads them, but they are not items a person triages, so they stay out.
CREATE OR REPLACE ALGORITHM=MERGE VIEW v_inbox AS
SELECT v.*
FROM v_inbox_all v
WHERE v.received_at <= UTC_TIMESTAMP(3)
  AND v.source <> 'calendar';
