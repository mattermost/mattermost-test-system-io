// Two nested describe blocks, each visiting a different section of
// the kitchen-sink site. Verifies that the dispatcher's parser walks
// the suite tree correctly and that `full_title` joins the suite path
// through both nesting levels (e.g. "navigation > reload > keeps the
// URL stable across cy.reload()").

describe('navigation', () => {
    describe('reload', () => {
        it('keeps the URL stable across cy.reload()', () => {
            cy.visit('/commands/navigation');
            cy.location('pathname').should('eq', '/commands/navigation');
            cy.reload();
            cy.location('pathname').should('eq', '/commands/navigation');
        });
    });

    describe('history', () => {
        it('navigates to a sibling page and back via cy.go', () => {
            cy.visit('/commands/navigation');
            cy.location('pathname').should('eq', '/commands/navigation');
            cy.visit('/');
            cy.location('pathname').should('eq', '/');
            cy.go('back');
            cy.location('pathname').should('eq', '/commands/navigation');
        });
    });
});
