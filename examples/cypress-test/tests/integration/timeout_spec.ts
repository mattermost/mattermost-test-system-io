// Exercises Cypress's timeout primitives. The cy.wait assertion
// measures wall-clock elapsed time and would fail if cy.wait returned
// early — proving the fixed-wait primitive actually blocks. The per-
// command `{ timeout }` option on cy.get proves the override flows
// through to the retry loop.

describe('timeout', () => {
    it(
        'cy.wait blocks for at least the requested duration',
        { defaultCommandTimeout: 5000, retries: 0 },
        () => {
            cy.visit('/commands/waiting');
            const start = Date.now();
            cy.wait(500).then(() => {
                const elapsed = Date.now() - start;
                expect(
                    elapsed,
                    `cy.wait(500) elapsed ${elapsed}ms`,
                ).to.be.gte(500);
            });
            cy.get('h1', { timeout: 8000 }).should('contain.text', 'Waiting');
        },
    );
});
