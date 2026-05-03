// Exercises the Mochawesome `pending: true` flag against a real page.
// The dispatcher's parser must map it.skip → status: "skipped" so the
// orchestration's `skipped` aggregate-status path stays exercised.

describe('skipped', () => {
    it('runs against the kitchen-sink home page', () => {
        cy.visit('/');
        cy.title().should('include', 'Kitchen Sink');
    });

    it.skip('is intentionally skipped via it.skip', () => {
        // Body never executes; Mocha marks the case pending so
        // Mochawesome reports it without a state value. The dispatcher
        // maps that flag to the orchestration `skipped` enum value.
        throw new Error('this should never run');
    });
});
