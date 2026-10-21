import { signal } from '@preact/signals';
import { connection, now, receivedAt, stale, state } from './api';
import { age } from './ui';
import { Cluster, Events, Overview } from './screens';

const routes = {
	'': { title: 'Overview', view: Overview },
	cluster: { title: 'Cluster & Leader', view: Cluster },
	events: { title: 'Events', view: Events },
} as const;
type Route = keyof typeof routes;

function current(): Route {
	const r = location.hash.replace(/^#\/?/, '');
	return r in routes ? (r as Route) : '';
}
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
				{(Object.keys(routes) as Route[]).map((k) => (
					<a href={`#/${k}`} class={route.value === k ? 'on' : ''} aria-current={route.value === k ? 'page' : undefined}>
						{routes[k].title}
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
			</div>
		</div>
	);
}
