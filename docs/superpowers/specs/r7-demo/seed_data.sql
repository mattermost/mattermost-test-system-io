-- ===================================================================
-- Round 7 live demo seed. Four scenarios, real rows, real API.
-- "now" anchor: all runs inside the 30d history window.
-- master runs have gh_pr_number NULL; PR runs have it set.
-- ===================================================================

-- --- A: MM-T2001 — genuinely flaky on master (40%), flaked ONCE on PR 5001.
--       Goal 1 "it's flaky, not me" -> expect check SUCCESS.
DO $$
DECLARE i int; st text; ts timestamptz;
BEGIN
  FOR i IN 0..19 LOOP
    -- alternating F/P for the first 16 -> 8 failures, many flips, rate 0.40
    st := CASE WHEN i < 16 AND i % 2 = 0 THEN 'failed' ELSE 'passed' END;
    ts := now() - make_interval(days => 25 - i);
    PERFORM seed_run('main', 'a'||lpad(i::text,39,'0'), NULL, 'run-a-'||i,
      'MM-T2001', 'MM-T2001 channel switcher opens',
      'e2e-tests/playwright/specs/functional/channels/channel_switcher.spec.ts',
      st,
      CASE WHEN st='failed' THEN 'locator.click: Timeout 30000ms exceeded' ELSE NULL END,
      CASE WHEN st='failed' THEN 'at channel_switcher.spec.ts:44' ELSE NULL END,
      ts);
  END LOOP;
END $$;

-- PR 5001: 3 runs, 1 failed (ordinary flake for a 40% test; p=0.784, NOT shifted)
SELECT seed_run('feat/search-tweak','pr5001aaa','5001','run-pr5001-1','MM-T2001','MM-T2001 channel switcher opens','e2e-tests/playwright/specs/functional/channels/channel_switcher.spec.ts','passed',NULL,NULL, now() - interval '3 hours');
SELECT seed_run('feat/search-tweak','pr5001bbb','5001','run-pr5001-2','MM-T2001','MM-T2001 channel switcher opens','e2e-tests/playwright/specs/functional/channels/channel_switcher.spec.ts','passed',NULL,NULL, now() - interval '2 hours');
SELECT seed_run('feat/search-tweak','pr5001ccc','5001','run-pr5001-3','MM-T2001','MM-T2001 channel switcher opens','e2e-tests/playwright/specs/functional/channels/channel_switcher.spec.ts','failed','locator.click: Timeout 30000ms exceeded','at channel_switcher.spec.ts:44', now() - interval '1 hour');

-- --- B: MM-T2002 — spotless on master (0/20), 3-of-3 on PR 5002 which edits drafts.
--       Goal 1 "your PR broke it" -> expect check FAILURE.
DO $$
DECLARE i int;
BEGIN
  FOR i IN 0..19 LOOP
    PERFORM seed_run('main','b'||lpad(i::text,39,'0'), NULL, 'run-b-'||i,
      'MM-T2002','MM-T2002 message draft persists across reload',
      'e2e-tests/playwright/specs/functional/channels/drafts.spec.ts',
      'passed', NULL, NULL, now() - make_interval(days => 25 - i));
  END LOOP;
END $$;
SELECT seed_run('feat/draft-refactor','pr5002aaa','5002','run-pr5002-'||g,'MM-T2002','MM-T2002 message draft persists across reload','e2e-tests/playwright/specs/functional/channels/drafts.spec.ts','failed',
  'expect(draft).toHaveText: Expected "hello" Received ""',
  'at webapp/channels/src/components/drafts/drafts.tsx:88'||E'\n'||'at drafts.spec.ts:31',
  now() - make_interval(hours => 4 - g)) FROM generate_series(1,3) g;

-- --- C: MM-T5824 — the ABAC case. 40% flaky on master AND 3-of-3 on PR 5003,
--       whose diff is CI-only (.github/**). Round 6 waived this. Rate shift
--       p=0.064 -> expect check FAILURE.
DO $$
DECLARE i int; st text;
BEGIN
  FOR i IN 0..19 LOOP
    st := CASE WHEN i < 16 AND i % 2 = 0 THEN 'failed' ELSE 'passed' END;
    PERFORM seed_run('main','c'||lpad(i::text,39,'0'), NULL, 'run-c-'||i,
      'MM-T5824','MM-T5824 ABAC file access policy renders',
      'e2e-tests/playwright/specs/functional/system_console/abac/file_access/file_permissions_render.spec.ts',
      st,
      CASE WHEN st='failed' THEN 'policy "sensitive-files" should appear after search — Expected: true, Received: false' ELSE NULL END,
      CASE WHEN st='failed' THEN 'at file_permissions_render.spec.ts:76' ELSE NULL END,
      now() - make_interval(days => 25 - i));
  END LOOP;
END $$;
SELECT seed_run('cherry-pick-abac','pr5003aaa','5003','run-pr5003-'||g,'MM-T5824','MM-T5824 ABAC file access policy renders','e2e-tests/playwright/specs/functional/system_console/abac/file_access/file_permissions_render.spec.ts','failed',
  'policy "sensitive-files" should appear after search — Expected: true, Received: false',
  'at file_permissions_render.spec.ts:76',
  now() - make_interval(hours => 4 - g)) FROM generate_series(1,3) g;

-- --- D: MM-T2004 — passed on master, then broke and stayed broken.
--       Goal 3 "it came from master, blame the author" -> master stays RED,
--       failing_since_commit recorded; bystander PR 5004 waives as pre-existing.
DO $$
DECLARE i int; st text;
BEGIN
  FOR i IN 0..19 LOOP
    st := CASE WHEN i < 14 THEN 'passed' ELSE 'failed' END;
    PERFORM seed_run('main','d'||lpad(i::text,39,'0'), NULL, 'run-d-'||i,
      'MM-T2004','MM-T2004 system console loads plugin list',
      'e2e-tests/playwright/specs/functional/system_console/plugins.spec.ts',
      st,
      CASE WHEN st='failed' THEN 'expect(pluginRow).toBeVisible: Timeout 15000ms exceeded' ELSE NULL END,
      CASE WHEN st='failed' THEN 'at plugins.spec.ts:52' ELSE NULL END,
      now() - make_interval(days => 25 - i));
  END LOOP;
END $$;
-- bystander PR that merely hits the already-broken master test
SELECT seed_run('feat/unrelated-typo','pr5004aaa','5004','run-pr5004-1','MM-T2004','MM-T2004 system console loads plugin list','e2e-tests/playwright/specs/functional/system_console/plugins.spec.ts','failed',
  'expect(pluginRow).toBeVisible: Timeout 15000ms exceeded','at plugins.spec.ts:52', now() - interval '1 hour');
