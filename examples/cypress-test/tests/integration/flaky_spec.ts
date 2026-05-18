// Non-deterministic flaky tests. Each test picks a fresh random
// integer and asserts its parity. The two tests are complementary —
// one passes only on even numbers, one passes only on odd — so a
// single `cypress run` invocation has:
//
//   - ~25% both pass
//   - ~25% even passes, odd fails
//   - ~25% odd passes, even fails
//   - ~25% both fail
//
// Spec aggregate: the orchestrator marks the spec `failed` whenever any
// of its tests fails (~75% of single invocations), which means the
// retest path is exercised more often than a single 50/50 test would.
// With retest_on_fail enabled at begin run, the orchestrator dispatches
// a fresh lease whose run draws new random numbers; across enough demo
// invocations every retest variant (recover on first retest, recover
// on second, exhaust budget) shows up.
//
// Per-test retries: 0 disables Cypress's in-process retry loop so the
// outcome of a single draw reaches orchestration unchanged.

describe('flaky', () => {
    it('passes only on even random numbers', { retries: 0 }, () => {
        const n = Math.floor(Math.random() * 1000);
        cy.log(`even-test draw: ${n}`);
        expect(n % 2, `random ${n} is ${n % 2 === 0 ? 'even' : 'odd'}`).to.equal(0);
    });

    it('passes only on odd random numbers', { retries: 0 }, () => {
        const n = Math.floor(Math.random() * 1000);
        cy.log(`odd-test draw: ${n}`);
        expect(n % 2, `random ${n} is ${n % 2 === 0 ? 'even' : 'odd'}`).to.equal(1);
    });
});
