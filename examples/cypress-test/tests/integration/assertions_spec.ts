// /commands/assertions documents Cypress's BDD/TDD assertion styles
// against deterministic page elements. The page exposes a heading and
// a stable code-block tree; assertions below check structural elements
// that have been part of the kitchen-sink page since v0.x and are not
// at risk of cosmetic churn.

describe('assertions', () => {
    it('renders the assertions page heading', () => {
        cy.visit('/commands/assertions');
        cy.get('h1').first().should('contain.text', 'Assertions');
    });

    it('exposes a code block on the assertions page', () => {
        cy.visit('/commands/assertions');
        cy.get('pre').should('exist');
    });
});
