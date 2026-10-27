-- Rejects of the validated transform run, grouped by reason.
SELECT source_table, reason, count(*) AS reject_rows
FROM buisy_nexus_staging.migration_rejects
WHERE run_id = current_setting('migrate.transform_run_id')::uuid
GROUP BY source_table, reason
ORDER BY source_table, reject_rows DESC;
