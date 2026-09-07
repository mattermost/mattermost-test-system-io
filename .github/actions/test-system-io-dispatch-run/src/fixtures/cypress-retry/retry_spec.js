describe("Cypress retry contract", () => {
  it("fails once then passes", function () {
    expect(this.test.currentRetry(), "retry contract").to.equal(1);
  });
  it("passes first try", () => {
    expect(true).to.equal(true);
  });
  it.skip("is skipped", () => {});
});
