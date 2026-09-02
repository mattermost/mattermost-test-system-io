-- E: MM-T2005 — mildly flaky on master (1/20 = 5%, within the 10% tolerance).
--    On PR 5005 it FAILED THEN RECOVERED ON RETRY (status=flaky) -> measured flake.
DO $$
DECLARE i int; st text;
BEGIN
  FOR i IN 0..19 LOOP
    st := CASE WHEN i = 7 THEN 'failed' ELSE 'passed' END;
    PERFORM seed_run('main','e'||lpad(i::text,39,'0'), NULL, 'run-e-'||i,
      'MM-T2005','MM-T2005 emoji picker opens',
      'e2e-tests/playwright/specs/functional/channels/emoji_picker.spec.ts',
      st,
      CASE WHEN st='failed' THEN 'locator.click: Timeout 30000ms exceeded' ELSE NULL END,
      CASE WHEN st='failed' THEN 'at emoji_picker.spec.ts:19' ELSE NULL END,
      now() - make_interval(days => 25 - i));
  END LOOP;
END $$;
SELECT seed_run('feat/emoji-tweak','pr5005aaa','5005','run-pr5005-1','MM-T2005','MM-T2005 emoji picker opens','e2e-tests/playwright/specs/functional/channels/emoji_picker.spec.ts','flaky',
  'locator.click: Timeout 30000ms exceeded','at emoji_picker.spec.ts:19', now() - interval '1 hour', 1);

-- F: MM-T2006 — flaky at exactly the 10% tolerance with 4 flips: the deterministic
--    FLAKY_TEST pre-tag fires AND amnesty is still within budget.
DO $$
DECLARE i int; st text;
BEGIN
  FOR i IN 0..19 LOOP
    st := CASE WHEN i IN (5, 12) THEN 'failed' ELSE 'passed' END;
    PERFORM seed_run('main','f'||lpad(i::text,39,'0'), NULL, 'run-f-'||i,
      'MM-T2006','MM-T2006 thread reply posts',
      'e2e-tests/playwright/specs/functional/channels/threads.spec.ts',
      st,
      CASE WHEN st='failed' THEN 'expect(reply).toBeVisible: Timeout 15000ms exceeded' ELSE NULL END,
      CASE WHEN st='failed' THEN 'at threads.spec.ts:63' ELSE NULL END,
      now() - make_interval(days => 25 - i));
  END LOOP;
END $$;
-- PR 5006: 1 failure in 3 runs -> p high, NOT shifted
SELECT seed_run('feat/profile-popover','pr5006aaa','5006','run-pr5006-1','MM-T2006','MM-T2006 thread reply posts','e2e-tests/playwright/specs/functional/channels/threads.spec.ts','passed',NULL,NULL, now() - interval '3 hours');
SELECT seed_run('feat/profile-popover','pr5006bbb','5006','run-pr5006-2','MM-T2006','MM-T2006 thread reply posts','e2e-tests/playwright/specs/functional/channels/threads.spec.ts','passed',NULL,NULL, now() - interval '2 hours');
SELECT seed_run('feat/profile-popover','pr5006ccc','5006','run-pr5006-3','MM-T2006','MM-T2006 thread reply posts','e2e-tests/playwright/specs/functional/channels/threads.spec.ts','failed',
  'expect(reply).toBeVisible: Timeout 15000ms exceeded','at threads.spec.ts:63', now() - interval '1 hour');
