// Flaky on randomness, but exercises baseUrl first so a failure
// screenshot captures the rendered kitchen-sink actions page (not a
// blank viewport). Pairs with flaky_navigation_screenshot_spec.ts so
// the demo's screenshot upload set varies across runs — sometimes
// neither fails, sometimes one, sometimes both.
//
//   - Visits /commands/actions and confirms a stable selector renders.
//   - Picks a random integer, passes only on even draws.
//   - On odd draws, Cypress captures tests/screenshots/<spec>/<title>
//     (failed).png with the actions page in view; the local demo's
//     queue-empty upload pushes that PNG via /reports/upload/.../screenshots.

describe('flaky actions screenshot', () => {
    it(
        'passes only on even draws after visiting the actions page',
        { retries: 0 },
        () => {
            cy.visit('/commands/actions');
            cy.get('.action-email').should('be.visible');
            const n = Math.floor(Math.random() * 1000);
            cy.log(`actions-flaky draw: ${n}`);
            expect(
                n % 2,
                `random ${n} is ${n % 2 === 0 ? 'even' : 'odd'}`,
            ).to.equal(0);
        },
    );
});
