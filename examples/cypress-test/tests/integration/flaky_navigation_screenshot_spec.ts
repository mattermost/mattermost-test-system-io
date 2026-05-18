// Flaky on randomness, exercises baseUrl, captures a screenshot of
// the navigation page on odd-draw failure. Asymmetric to
// flaky_actions_screenshot_spec.ts — that one passes on even, this
// one passes on odd — so the two specs' outcomes are uncorrelated and
// the demo's screenshot upload set genuinely varies.

describe('flaky navigation screenshot', () => {
    it(
        'passes only on odd draws after visiting the navigation page',
        { retries: 0 },
        () => {
            cy.visit('/commands/navigation');
            cy.get('h1').first().should('contain.text', 'Navigation');
            const n = Math.floor(Math.random() * 1000);
            cy.log(`navigation-flaky draw: ${n}`);
            expect(
                n % 2,
                `random ${n} is ${n % 2 === 0 ? 'even' : 'odd'}`,
            ).to.equal(1);
        },
    );
});
