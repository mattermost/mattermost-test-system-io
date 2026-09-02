-- G: MM-T2007 — only 3/20 on master (15%) but it lands on SIX different PRs.
--    Old ranking (master failure count) buries it below the 40% flakes.
--    New ranking (blast radius) puts it first: it cost six developers time.
DO $$
DECLARE i int; st text;
BEGIN
  FOR i IN 0..19 LOOP
    st := CASE WHEN i IN (3, 9, 15) THEN 'failed' ELSE 'passed' END;
    PERFORM seed_run('main','g'||lpad(i::text,39,'0'), NULL, 'run-g-'||i,
      'MM-T2007','MM-T2007 global header renders',
      'e2e-tests/playwright/specs/functional/channels/global_header.spec.ts',
      st,
      CASE WHEN st='failed' THEN 'expect(header).toBeVisible: Timeout 15000ms exceeded' ELSE NULL END,
      CASE WHEN st='failed' THEN 'at global_header.spec.ts:22' ELSE NULL END,
      now() - make_interval(days => 25 - i));
  END LOOP;
END $$;
-- six distinct PRs, each hit once (global_header runs in every PR's smoke shard)
SELECT seed_run('feat/pr-'||p, 'g5'||p||'aaa', 5100+p, 'run-pr51'||p||'-1',
  'MM-T2007','MM-T2007 global header renders',
  'e2e-tests/playwright/specs/functional/channels/global_header.spec.ts','failed',
  'expect(header).toBeVisible: Timeout 15000ms exceeded','at global_header.spec.ts:22',
  now() - make_interval(hours => 20 - p)) FROM generate_series(1,6) p;
