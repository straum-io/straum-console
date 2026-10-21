import { signal } from '@preact/signals';
import { connection, now, receivedAt, stale, state } from './api';
import { age } from './ui';
import { hashFor, query, setQuery } from './search';
import { Cluster, Events, Overview } from './screens';
import { Configuration, Durability, NotExposed } from './more';

const routes = {
	'': { title: 'Overview', view: Overview },
	cluster: { title: 'Cluster & Leader', view: Cluster },
	durability: { title: 'Replication & Durability', view: Durability },
	events: { title: 'Events', view: Events },
	config: { title: 'Configuration', view: Configuration },
	'not-exposed': { title: 'Not exposed by the core', view: NotExposed },
} as const;
type Route = keyof typeof routes;

function current(): Route {
	const r = location.hash.replace(/^#\/?/, '').split('?')[0] ?? '';
	return r in routes ? (r as Route) : '';
}
const order = Object.keys(routes) as Route[];

function openHelp() {
	(document.getElementById('keys') as HTMLDialogElement | null)?.showModal();
}

// Keys: 1–6 open the pages in sidebar order, / searches, t switches the
// theme, ? lists the keys. Never while typing, never with a modifier.
addEventListener('keydown', (e) => {
	if (e.ctrlKey || e.metaKey || e.altKey) return;
	if (document.querySelector('dialog[open]')) return; // the dialog handles Esc itself
	const target = e.target as HTMLElement;
	if (target.closest('input, textarea, select, [contenteditable]')) {
		if (e.key === 'Escape' && target.id === 'search') {
			setQuery('');
			target.blur();
		}
		return;
	}
	if (e.key === '/') {
		e.preventDefault();
		document.getElementById('search')?.focus();
	} else if (e.key === '?') {
		openHelp();
	} else if (e.key === 't') {
		toggleTheme();
	} else if (/^[1-9]$/.test(e.key) && order[Number(e.key) - 1] !== undefined) {
		location.hash = hashFor(order[Number(e.key) - 1]!);
	}
});
const route = signal<Route>(current());
addEventListener('hashchange', () => (route.value = current()));

type Theme = 'dark' | 'light';
function initialTheme(): Theme {
	try {
		const saved = localStorage.getItem('theme');
		if (saved === 'dark' || saved === 'light') return saved;
	} catch {
		// storage may be unavailable
	}
	return matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
}
const theme = signal<Theme>(initialTheme());
function applyTheme() {
	document.documentElement.dataset['theme'] = theme.value;
}
applyTheme();
function toggleTheme() {
	theme.value = theme.value === 'dark' ? 'light' : 'dark';
	applyTheme();
	try {
		localStorage.setItem('theme', theme.value);
	} catch {
		// the choice lasts for this page only
	}
}

function Status() {
	const c = connection.value;
	const s = state.value;
	if (c === 'live' && stale.value) return <span class="pill warn">not current</span>;
	if (c === 'live' && s) return <span class="pill live">live · {s.interval_ms / 1000} s</span>;
	return <span class={`pill ${c === 'disconnected' ? 'down' : 'warn'}`}>{c}</span>;
}

export function App() {
	const r = routes[route.value];
	const s = state.value;
	const View = r.view;
	// Lost the console: nothing on screen is current, whatever it said.
	const current = connection.value === 'live' && !stale.value;
	return (
		<div class={`app ${current ? '' : 'not-current'}`}>
			<nav>
				<div class="brand">
					<img src="/mark.svg" alt="" width={24} height={24} />
					straum-console
				</div>
				{order.map((k, i) => (
					<a href={hashFor(k)} class={route.value === k ? 'on' : ''} aria-current={route.value === k ? 'page' : undefined}>
						{routes[k].title}
						<kbd aria-hidden="true">{i + 1}</kbd>
					</a>
				))}
			</nav>
			<div class="main">
				<header>
					<h1>{r.title}</h1>
					<Status />
					{s && <span class="pill">round {s.round}</span>}
					{s && (
						<span class="pill">
							{s.cluster.observed} / {s.cluster.configured} observed
						</span>
					)}
					<span class="grow" />
					<input
						id="search"
						type="search"
						placeholder="Filter nodes and events"
						aria-label="Filter nodes and events"
						aria-keyshortcuts="/"
						value={query.value}
						onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
					/>
					<button class="theme" type="button" onClick={openHelp} aria-label="keyboard shortcuts" aria-keyshortcuts="?">
						?
					</button>
					<button class="theme" type="button" onClick={toggleTheme} aria-label={`${theme.value === 'dark' ? 'light' : 'dark'} theme`}>
						{theme.value === 'dark' ? 'light' : 'dark'}
					</button>
				</header>
				{connection.value === 'disconnected' && (
					<div class="banner" role="status">
						The console does not answer. What is shown is its last state, received{' '}
						{receivedAt.value ? `${age(now.value - receivedAt.value)} ago` : 'never'}; it reconnects on its own.
					</div>
				)}
				<main>{s ? <View /> : <p class="none">Waiting for the first round…</p>}</main>
				<dialog id="keys" aria-labelledby="keys-title">
					<h2 id="keys-title">Keyboard shortcuts</h2>
					<dl>
						<dt>
							<kbd>1</kbd> … <kbd>{order.length}</kbd>
						</dt>
						<dd>open a page, in sidebar order</dd>
						<dt>
							<kbd>/</kbd>
						</dt>
						<dd>filter nodes and events</dd>
						<dt>
							<kbd>Esc</kbd>
						</dt>
						<dd>clear the filter, or close this</dd>
						<dt>
							<kbd>t</kbd>
						</dt>
						<dd>switch between dark and light</dd>
						<dt>
							<kbd>?</kbd>
						</dt>
						<dd>this list</dd>
					</dl>
					<form method="dialog">
						<button class="theme">close</button>
					</form>
				</dialog>
			</div>
		</div>
	);
}
