import { defineConfig, devices } from '@playwright/test';

// The tests start the console and its fixture nodes themselves
// (tests/console.spec.ts); `make web-test` builds the binary first.
export default defineConfig({
	testDir: 'tests',
	timeout: 60_000,
	retries: 0,
	workers: 1,
	reporter: [['list']],
	use: { trace: 'retain-on-failure' },
	projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
});
