// Demonstrates Cypress's default testIsolation: true behavior on a real
// page. The first test sets a localStorage value while on the kitchen-
// sink homepage; the second test re-visits and asserts the value is NOT
// visible. If isolation breaks (or Cypress's defaults change), the
// second test fails loudly and the demo's flaky-or-isolation diagnosis
// gets a concrete failure to point at.

describe('isolation', () => {
    it('sets a localStorage value while visiting the homepage', () => {
        cy.visit('/');
        cy.window().then((win) => {
            win.localStorage.setItem('fixture-marker', 'set-by-first-test');
        });
        cy.window()
            .its('localStorage')
            .invoke('getItem', 'fixture-marker')
            .should('eq', 'set-by-first-test');
    });

    it('does not see the previous test\'s localStorage value', () => {
        cy.visit('/');
        cy.window()
            .its('localStorage')
            .invoke('getItem', 'fixture-marker')
            .should('be.null');
    });
});
