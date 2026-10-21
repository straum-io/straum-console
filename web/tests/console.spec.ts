// Smoke tests of the web pages against the real console binary and three
// fixture nodes (the core's own renders, testdata/node*.prom), which the
// tests can stop or make report leading.
import { test, expect, type Page } from '@playwright/test';
import { spawn, type ChildProcess } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import type { AddressInfo } from 'node:net';

const root = new URL('../../', import.meta.url).pathname;
const consolePort = 18180;
const base = `http://127.0.0.1:${consolePort}`;

interface Fixture { server: Server; port: number; down: boolean; leading: number; set: Record<string, number> }
const nodes: Fixture[] = [];
let consoleProc: ChildProcess | null = null;

function body(i: number, f: Fixture): string {
	let text = readFileSync(`${root}testdata/node${i}.prom`, 'utf8').replace(/^straum_leading \d+$/m, `straum_leading ${f.leading}`);
	for (const [k, v] of Object.entries(f.set)) text = text.replace(new RegExp(`^${k} \\d+$`, 'm'), `${k} ${v}`);
	return text;
}

async function startNodes() {
	for (let i = 0; i < 3; i++) {
		const f: Fixture = { server: createServer(), port: 0, down: false, leading: i === 0 ? 1 : 0, set: {} };
		f.server.on('request', (_req, res) => {
			if (f.down) {
				res.writeHead(500).end('down');
				return;
			}
			res.writeHead(200, { 'Content-Type': 'text/plain; version=0.0.4' }).end(body(i, f));
		});
		await new Promise<void>((ok) => f.server.listen(0, '127.0.0.1', ok));
		f.port = (f.server.address() as AddressInfo).port;
		nodes.push(f);
	}
}

async function startConsole() {
	const spec = nodes.map((f, i) => `${i}=127.0.0.1:${f.port}`).join(',');
	consoleProc = spawn(`${root}bin/straum-console`, ['--node', spec, '--interval', '1s', '--listen', `127.0.0.1:${consolePort}`], {
		stdio: 'ignore',
	});
	for (let i = 0; i < 50; i++) {
		try {
			if ((await fetch(`${base}/readyz`)).ok) return;
		} catch {
			// not listening yet
		}
		await new Promise((r) => setTimeout(r, 100));
	}
	throw new Error('the console did not become ready');
}

async function stopConsole() {
	if (!consoleProc) return;
	const p = consoleProc;
	consoleProc = null;
	await new Promise<void>((ok) => {
		p.once('exit', () => ok());
		p.kill('SIGKILL');
	});
}

test.describe.configure({ mode: 'serial' });

test.beforeAll(async () => {
	await startNodes();
	await startConsole();
});

test.afterAll(async () => {
	await stopConsole();
	for (const f of nodes) f.server.close();
});

// CSP violations and page errors, from every test: none is allowed.
const problems: string[] = [];
test.afterEach(() => {
	const seen = problems.splice(0);
	expect(seen).toEqual([]);
});

async function open(page: Page, path = '/') {
	await page.exposeFunction('reportViolation', (v: string) => problems.push(v));
	await page.addInitScript(() =>
		document.addEventListener('securitypolicyviolation', (e) =>
			(window as unknown as { reportViolation(v: string): void }).reportViolation(`${e.violatedDirective} ${e.blockedURI}`),
		),
	);
	page.on('pageerror', (e) => problems.push(`pageerror: ${e.message}`));
	await page.goto(base + path);
	await expect(page.locator('.pill.live')).toBeVisible();
}

test('the overview shows every node, and navigation works', async ({ page }) => {
	await open(page);
	await expect(page.locator('.card')).toHaveCount(3);
	await expect(page.locator('.fact', { hasText: 'nodes observed' })).toContainText('3 / 3');
	await expect(page.locator('.fact', { hasText: 'reports leading' })).toContainText('node 0');
	await expect(page.locator('.card[data-node="0"] .tag')).toHaveText('leading');

	await page.getByRole('link', { name: 'Cluster & Leader' }).click();
	await expect(page.locator('h1')).toHaveText('Cluster & Leader');
	await expect(page.locator('table.grid tbody tr')).toHaveCount(3);

	await page.getByRole('link', { name: 'Events' }).click();
	await expect(page.locator('h1')).toHaveText('Events');
});

test('fonts and styles come from the console itself', async ({ page }) => {
	await open(page);
	const loaded = await page.evaluate(async () => {
		await document.fonts.ready;
		return [...document.fonts].filter((f) => f.status === 'loaded').map((f) => f.family.replace(/"/g, ''));
	});
	expect(loaded).toEqual(expect.arrayContaining(['Inter', 'Inter Tight', 'JetBrains Mono']));
});

test('a change of who reports leading shows within a round', async ({ page }) => {
	await open(page);
	nodes[0]!.leading = 0;
	nodes[1]!.leading = 1;
	// Within one round (1 s) plus the browser's share.
	await expect(page.locator('.fact', { hasText: 'reports leading' })).toContainText('node 1', { timeout: 2500 });
	await expect(page.locator('.card[data-node="1"] .tag')).toHaveText('leading');
	await expect(page.locator('table.events')).toContainText('cluster.leading.changed');
	// Times read as a clock, hours to milliseconds.
	await expect(page.locator('table.events td.t').first()).toHaveText(/^\d{2}:\d{2}:\d{2}[.,]\d{3}$/);
});

test('a node that stops answering is unknown, with its last values and their age', async ({ page }) => {
	await open(page);
	nodes[2]!.down = true;
	const card = page.locator('.card[data-node="2"]');
	await expect(card.locator('.state')).toHaveText('unknown', { timeout: 5000 });
	await expect(card).toHaveClass(/stale/);
	await expect(card).toContainText('last values, read');
	await expect(card.locator('.err')).toContainText('HTTP 500');
	await expect(page.locator('table.events')).toContainText('console.node.unreachable');
	nodes[2]!.down = false;
	await expect(card.locator('.state')).toHaveText('observed', { timeout: 5000 });
});

test('a console that goes away shows disconnected, then a gap, then recovers', async ({ page }) => {
	await open(page, '/#/events');
	await stopConsole();
	await expect(page.locator('.banner')).toContainText('does not answer', { timeout: 15000 });
	await expect(page.locator('.banner')).toContainText(/received \d/);
	// Lost the console: nothing looks current, not even a node it last saw observed.
	await expect(page.locator('.app')).toHaveClass(/not-current/);
	await startConsole();
	await expect(page.locator('.pill.live')).toBeVisible({ timeout: 15000 });
	await expect(page.locator('.banner')).toHaveCount(0);
	await expect(page.locator('.app')).not.toHaveClass(/not-current/);
	await expect(page.locator('tr.gap')).toContainText('the console restarted');
});

test('replication and durability keep local and quorum apart, and say what is computed', async ({ page }) => {
	// Three different positions, so each derived column has one answer.
	nodes[0]!.set = { straum_log_end: 15, straum_log_durable: 13, straum_commit: 10, straum_applied: 10 };
	await open(page, '/#/durability');
	await expect(page.locator('h1')).toHaveText('Replication & Durability');
	await expect(page.locator('th.group')).toHaveText(['Local: fsync on this node', 'Quorum: majority durable, as this node knows it']);
	const row = page.locator('table.durability tr[data-node="0"] td');
	await expect(row.nth(2)).toHaveText('13', { timeout: 5000 }); // durable through
	await expect(row.nth(3)).toHaveText('2'); // not yet synced: 15 − 13
	await expect(row.nth(4)).toHaveText('10'); // commit
	await expect(row.nth(5)).toHaveText('5'); // not yet committed: 15 − 10
	nodes[0]!.set = {};
	await expect(page.locator('.foot-note')).toContainText('Computed by the console');
	await expect(page.locator('.note').first()).toContainText('ADR-0005');
	nodes[1]!.down = true;
	await expect(page.locator('table.durability tr[data-node="1"]')).toHaveClass(/stale/, { timeout: 5000 });
	nodes[1]!.down = false;
});

test('configuration shows the console\'s own settings and each node\'s identity', async ({ page }) => {
	await open(page, '/#/config');
	await expect(page.locator('.fact', { hasText: 'between polls' })).toContainText('1.0 s');
	await expect(page.locator('.fact', { hasText: 'to wait for each node' })).toContainText('500 ms');
	for (const id of [0, 1, 2]) {
		const cells = page.locator(`table.grid tr[data-node="${id}"] td`);
		await expect(cells.nth(3)).toHaveText('matches');
		await expect(cells.nth(4)).toHaveText('11 / 11');
	}
	await expect(page.locator('ul.series li')).toHaveCount(11);
});

test('not exposed lists what is missing and what would provide it', async ({ page }) => {
	await open(page, '/#/not-exposed');
	// The screens the stable series cannot fill are here, each with its rows.
	for (const [screen, row] of [
		['Throughput & Latency', 'Publish, PUBACK and delivery rates'],
		['MQTT Gateway', 'MQTT connections'],
		['Errors & Pressure', 'Own proposals refused at a full outbox'],
	]) {
		await expect(page.locator(`tbody[data-screen="${screen}"]`)).toContainText(row!);
	}
	await expect(page.locator('table.not-exposed')).toContainText('straum#32');
});
