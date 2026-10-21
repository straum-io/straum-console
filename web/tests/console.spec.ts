// Smoke tests of the web pages against the real console binary and three
// fixture nodes (the core's own renders, testdata/node*.prom), which the
// tests can stop or make report leading.
import { test, expect, type Page } from '@playwright/test';
import { spawn, type ChildProcess } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import { connect, createServer as createNetServer, type AddressInfo, type Socket } from 'node:net';

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

async function open(page: Page, path = '/', at = base) {
	await page.exposeFunction('reportViolation', (v: string) => problems.push(v));
	await page.addInitScript(() =>
		document.addEventListener('securitypolicyviolation', (e) =>
			(window as unknown as { reportViolation(v: string): void }).reportViolation(`${e.violatedDirective} ${e.blockedURI}`),
		),
	);
	page.on('pageerror', (e) => problems.push(`pageerror: ${e.message}`));
	await page.goto(at + path);
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

/**
 * A TCP proxy in front of the console, so a test can cut the browser's
 * network for real: every open connection closed, new ones refused.
 * (Playwright's offline mode leaves an open EventSource connected.)
 */
async function proxy(target: number) {
	let cut = false;
	const sockets = new Set<Socket>();
	const server = createNetServer((client) => {
		if (cut) return client.destroy();
		const upstream = connect(target, '127.0.0.1');
		sockets.add(client).add(upstream);
		client.pipe(upstream).pipe(client);
		const drop = () => {
			client.destroy();
			upstream.destroy();
			sockets.delete(client);
			sockets.delete(upstream);
		};
		client.on('error', drop).on('close', drop);
		upstream.on('error', drop).on('close', drop);
	});
	await new Promise<void>((ok) => server.listen(0, '127.0.0.1', ok));
	return {
		url: `http://127.0.0.1:${(server.address() as AddressInfo).port}`,
		cut() {
			cut = true;
			for (const s of sockets) s.destroy();
		},
		restore() {
			cut = false;
		},
		close: () => server.close(),
	};
}

test('after a 10 s network cut, the events missed are replayed, not lost', async ({ page }) => {
	const net = await proxy(consolePort);
	try {
		await open(page, '/#/events', net.url);
		await expect(page.locator('h1')).toHaveText('Events');
		// The newest event the console has before the cut.
		const before = ((await (await fetch(`${base}/api/v1/events`)).json()) as { events: { id: number }[] }).events.at(-1)?.id ?? 0;
		net.cut();
		nodes[2]!.down = true; // an event while the browser cannot hear it
		// The cut is real: the stream stops, so the view goes not current.
		await expect(page.locator('.app')).toHaveClass(/not-current/, { timeout: 8000 });
		await page.waitForTimeout(10_000);
		net.restore();
		await expect(page.locator('.pill.live')).toBeVisible({ timeout: 20_000 });
		// The event raised during the cut, not an older one of the same name.
		const missed = page.locator('table.events tr[data-name="console.node.unreachable"][data-node="2"]');
		await expect
			.poll(async () => Math.max(0, ...(await missed.evaluateAll((rows) => rows.map((r) => Number(r.getAttribute('data-id')))))))
			.toBeGreaterThan(before);
		await expect(page.locator('tr.gap')).toHaveCount(0); // replayed, so no gap
	} finally {
		nodes[2]!.down = false;
		net.close();
	}
});

test('hotkeys: numbers open pages, ? lists the keys, t switches the theme', async ({ page }) => {
	await open(page);
	await page.keyboard.press('2');
	await expect(page.locator('h1')).toHaveText('Cluster & Leader');
	await page.keyboard.press('6');
	await expect(page.locator('h1')).toHaveText('Not exposed by the core');
	await page.keyboard.press('1');
	await expect(page.locator('h1')).toHaveText('Overview');

	await page.keyboard.press('?');
	await expect(page.locator('dialog#keys')).toBeVisible();
	await page.keyboard.press('Escape');
	await expect(page.locator('dialog#keys')).toBeHidden();

	const before = await page.evaluate(() => document.documentElement.dataset['theme']);
	await page.keyboard.press('t');
	await expect.poll(() => page.evaluate(() => document.documentElement.dataset['theme'])).not.toBe(before);
	await page.keyboard.press('t');
});

test('search filters what each page lists, lives in the URL, and keys do not fire while typing', async ({ page }) => {
	await open(page);
	// Keys typed into the field are text, never page keys or the theme key.
	const theme = await page.evaluate(() => document.documentElement.dataset['theme']);
	await page.keyboard.press('/');
	await expect(page.locator('#search')).toBeFocused();
	await page.keyboard.type('2t');
	await expect(page.locator('h1')).toHaveText('Overview');
	expect(await page.evaluate(() => document.documentElement.dataset['theme'])).toBe(theme);
	await expect(page.locator('#search')).toHaveValue('2t');
	await page.locator('#search').fill('');
	await page.locator('#search').blur();
	// With a modifier, a digit is the browser's, not the console's.
	await page.keyboard.press('Control+2');
	await page.keyboard.press('Alt+2');
	await expect(page.locator('h1')).toHaveText('Overview');
	// While the help is open, page keys do nothing.
	await page.keyboard.press('?');
	await page.keyboard.press('3');
	await expect(page.locator('h1')).toHaveText('Overview');
	await page.keyboard.press('Escape');

	await page.keyboard.press('/');
	await page.keyboard.type('node 1');
	await expect(page.locator('.card')).toHaveCount(1);
	await expect(page.locator('.card')).toHaveAttribute('data-node', '1');
	await expect(page.locator('.filtered')).toContainText('1 of 3 nodes');
	expect(page.url()).toContain('q=node+1');

	// The filter follows to other pages.
	await page.getByRole('link', { name: /Cluster & Leader/ }).click();
	await expect(page.locator('table.grid tbody tr')).toHaveCount(1);

	// Esc in the box clears it.
	await page.locator('#search').focus();
	await page.keyboard.press('Escape');
	await expect(page.locator('table.grid tbody tr')).toHaveCount(3);

	// A node that stops answering is found by its state.
	nodes[2]!.down = true;
	await page.goto(`${base}/#/?q=unknown`);
	await expect(page.locator('.card')).toHaveCount(1, { timeout: 5000 });
	await expect(page.locator('.card')).toHaveAttribute('data-node', '2');
	nodes[2]!.down = false;
});

test('search finds events by name and node', async ({ page }) => {
	await open(page, '/#/events?q=leading');
	await expect(page.locator('h1')).toHaveText('Events');
	// A leading change of this console's own epoch to find. Cluster events
	// need the same nodes observed in both rounds, so first all three.
	await expect(page.locator('header .pill', { hasText: 'observed' })).toHaveText('3 / 3 observed', { timeout: 10_000 });
	await page.waitForTimeout(1500);
	const [a, b] = nodes[0]!.leading ? [nodes[0]!, nodes[1]!] : [nodes[1]!, nodes[0]!];
	a.leading = 0;
	b.leading = 1;
	const rows = page.locator('table.events tbody tr:not(.gap)');
	await expect(rows.first()).toBeVisible();
	for (const text of await rows.allTextContents()) expect(text).toContain('leading');
	await page.locator('#search').fill('no such event anywhere');
	await expect(page.locator('main')).toContainText('No event matches the filter.');
});

test('no page is wider than the window, and the help dialog sits in the middle', async ({ page }) => {
	// Narrow enough that the widest table does not fit: it must scroll, not the page.
	await page.setViewportSize({ width: 1024, height: 720 });
	for (const path of ['', 'cluster', 'durability', 'events', 'config', 'not-exposed']) {
		await page.goto(`${base}/#/${path}`);
		await page.reload(); // a hash change alone keeps the old page
		await expect(page.locator('.pill.live')).toBeVisible();
		const [scroll, inner] = await page.evaluate(() => [document.documentElement.scrollWidth, innerWidth]);
		expect(scroll, `#/${path}`).toBeLessThanOrEqual(inner);
	}
	await page.keyboard.press('?');
	const box = (await page.locator('dialog#keys').boundingBox())!;
	expect(Math.abs(box.x + box.width / 2 - 512)).toBeLessThan(20);
});
