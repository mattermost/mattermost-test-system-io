// Always fails after visiting a real kitchen-sink page so Cypress
// captures a screenshot of the rendered DOM (not blank). The orchestration
// demo collects everything under tests/screenshots/ at queue-empty and
// uploads via the /reports/upload/.../screenshots flow, exercising the
// screenshot end-to-end path the dashboard's Reports tab consumes.
//
// Per-test retries: 0 keeps the demo loop fast — Cypress's in-process
// retry would just produce 3 attempts of the same screenshot.

describe('screenshot demo', () => {
    it(
        'captures a screenshot when an assertion fails on the actions page',
        { retries: 0 },
        () => {
            cy.visit('/commands/actions');
            // The selector below is intentionally not on the page. Cypress
            // polls until the per-test timeout elapses, fails, and writes
            // a screenshot of /commands/actions to tests/screenshots/.
            cy.get('#orchestration-fixture-screenshot-target', { timeout: 500 }).should(
                'be.visible',
            );
        },
    );
});
