-- Keep the compatible superset on downgrade. Restoring a STORED generated
-- column would rewrite all test data and remove project separation. Migration
-- 28 down removes the columns/functions when a full rollback is requested.
SELECT 1;
