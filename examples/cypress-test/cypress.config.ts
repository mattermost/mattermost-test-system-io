import { defineConfig } from 'cypress';
import webpackPreprocessor from '@cypress/webpack-preprocessor';

// Local-test fixture configuration. Mirrors upstream e2e-tests/cypress
// conventions where they matter (specPattern, retries policy, reporter
// integration via reporter-config.json) and skips upstream's heavier
// pieces (support file, project-specific env vars) that aren't needed for
// fixture tests.
// Explicit babel preprocessor: Cypress's built-in TS handling can't
// resolve @babel/preset-typescript in this environment.
function preprocessor(on: Cypress.PluginEvents) {
    on(
        'file:preprocessor',
        webpackPreprocessor({
            webpackOptions: {
                resolve: { extensions: ['.ts', '.js'] },
                module: {
                    rules: [
                        {
                            test: /\.ts$/,
                            exclude: [/node_modules/],
                            use: [
                                {
                                    loader: require.resolve('babel-loader'),
                                    options: {
                                        presets: [
                                            require.resolve('@babel/preset-typescript'),
                                        ],
                                    },
                                },
                            ],
                        },
                    ],
                },
            },
        }),
    );
}

export default defineConfig({
    chromeWebSecurity: false,
    defaultCommandTimeout: 5000,
    video: false,
    screenshotsFolder: 'tests/screenshots',
    // Off: each lease spawns its own `cypress run`, and the default would
    // wipe prior leases' screenshots before the shard upload collects them.
    trashAssetsBeforeRuns: false,
    viewportWidth: 1280,
    viewportHeight: 720,
    e2e: {
        // The Mattermost convention: *_spec.{ts,js} under tests/integration.
        // The dispatcher action and orchestration-demo-cypress.js both
        // assume this layout when discovering specs. Override here if
        // a future fixture variant needs a different shape.
        specPattern: 'tests/integration/**/*_spec.{ts,js}',
        // Cypress's official kitchen-sink demo site — stable, public,
        // explicitly intended as a Cypress test target. Picks up
        // Cypress.config('baseUrl') in cy.visit('/...') calls so the
        // fixture's tests stay short and read like real consumer specs
        // without needing a local dev server. Works without TSIO running.
        baseUrl: 'https://example.cypress.io',
        // No support file — fixture tests are deliberately self-contained
        // to keep the demo loop fast.
        supportFile: false,
        setupNodeEvents(on) {
            preprocessor(on);
        },
        // Retries match the upstream policy. flaky_spec.ts disables them
        // per-test (retries: 0) so its random pass/fail outcome reaches
        // orchestration unchanged — the orchestration retest is what
        // recovers it across leases when retest_on_fail is set.
        retries: {
            runMode: 2,
            openMode: 0,
        },
        // testIsolation defaults to true in Cypress 15. isolation_spec.ts
        // demonstrates the behavior; do NOT override here.
    },
});
