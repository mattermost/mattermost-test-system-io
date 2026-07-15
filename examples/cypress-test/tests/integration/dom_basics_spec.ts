// Querying patterns against the kitchen-sink /commands/querying page.
// The page exposes stable selectors (#query-btn, .query-list, etc.)
// designed for Cypress demos.

describe('querying', () => {
    it('finds an element by id', () => {
        cy.visit('/commands/querying');
        cy.get('#query-btn').should('contain.text', 'Button');
    });

    it('finds an element by text via cy.contains', () => {
        cy.visit('/commands/querying');
        cy.contains('Submit').should('be.visible');
    });
});
