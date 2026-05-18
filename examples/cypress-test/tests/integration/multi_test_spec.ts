// Five tests in one spec file, all visiting /commands/actions and each
// exercising a different stable selector. Drives the per-spec
// test_cases array shape with multiple DOM-touching entries — useful
// for verifying that the dispatcher's parser collects every test in a
// single Mochawesome JSON output and assigns sensible ordinal values.

describe('multi-test on actions page', () => {
    beforeEach(() => {
        cy.visit('/commands/actions');
    });

    it('sees the email input', () => {
        cy.get('.action-email').should('be.visible');
    });

    it('sees the disabled input is disabled', () => {
        cy.get('.action-disabled').should('be.disabled');
    });

    it('sees the focus button', () => {
        cy.get('.action-focus').should('be.visible');
    });

    it('sees the blur button', () => {
        cy.get('.action-blur').should('be.visible');
    });

    it('clears the email input value', () => {
        cy.get('.action-clear')
            .type('to-be-cleared')
            .should('have.value', 'to-be-cleared')
            .clear()
            .should('have.value', '');
    });
});
