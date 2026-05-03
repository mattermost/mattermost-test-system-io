// Smoke test against the kitchen-sink homepage. Three small assertions
// covering page title, primary heading text, and the navigation banner —
// enough to prove the Cypress process boot cycle, the baseUrl override,
// and DOM querying all work end to end against a real public site.

describe('homepage', () => {
    it('lands on the kitchen-sink homepage', () => {
        cy.visit('/');
        cy.title().should('include', 'Kitchen Sink');
    });

    it('renders the primary heading', () => {
        cy.visit('/');
        cy.get('h1').first().should('contain.text', 'Kitchen Sink');
    });

    it('exposes the top navigation', () => {
        cy.visit('/');
        cy.get('nav').should('exist');
    });
});
