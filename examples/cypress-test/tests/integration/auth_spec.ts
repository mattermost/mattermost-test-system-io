// /commands/actions exposes a stable form with deterministic class
// names. Both tests exercise real .type / .blur / .clear chains.

describe('actions', () => {
    it('types into the email input and blurs', () => {
        cy.visit('/commands/actions');
        cy.get('.action-email')
            .type('orchestration@example.com')
            .should('have.value', 'orchestration@example.com')
            .blur()
            .should('have.class', 'form-control');
    });

    it('clears a previously-typed value', () => {
        cy.visit('/commands/actions');
        cy.get('.action-clear')
            .type('to-be-cleared')
            .should('have.value', 'to-be-cleared')
            .clear()
            .should('have.value', '');
    });
});
