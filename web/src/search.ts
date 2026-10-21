// The filter every page applies to what it lists. The query lives in the
// URL (#/events?q=leading), so a filtered view can be shared or reloaded.
import { signal } from '@preact/signals';
import type { ConsoleEvent, Entry, NodeState } from './api';

function fromHash(): string {
	const i = location.hash.indexOf('?');
	return i < 0 ? '' : (new URLSearchParams(location.hash.slice(i + 1)).get('q') ?? '');
}

export const query = signal(fromHash());
addEventListener('hashchange', () => (query.value = fromHash()));

/** The hash for a page, keeping the current query. */
export function hashFor(path: string, q = query.value): string {
	return `#/${path}` + (q ? `?${new URLSearchParams({ q })}` : '');
}

export function setQuery(q: string) {
	query.value = q;
	const path = location.hash.replace(/^#\/?/, '').split('?')[0] ?? '';
	// replaceState fires no hashchange, and typing leaves no history trail.
	history.replaceState(null, '', hashFor(path, q));
}

/**
 * The query, as typed and case-insensitive, appears somewhere in parts. A
 * query ending in a digit must end a number there too: "node 1" matches
 * node 1, not node 12.
 */
export function matches(...parts: (string | number | null | undefined)[]): boolean {
	const q = query.value.trim().toLowerCase();
	if (!q) return true;
	const text = parts.filter((p) => p !== null && p !== undefined).join(' · ').toLowerCase();
	if (!/\d$/.test(q)) return text.includes(q);
	const escaped = q.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
	return new RegExp(`${escaped}(?!\\d)`).test(text);
}

export const nodeMatches = (n: NodeState) => matches(`node ${n.id}`, n.address, n.state, n.error);

export function eventMatches(e: ConsoleEvent, description: string) {
	return matches(e.name, description, e.node === null ? 'cluster' : `node ${e.node}`);
}

/**
 * The entries a filtered list shows: the matching events, and every gap
 * marker, so a filtered view never looks complete when events were missed.
 */
export function filterEntries(list: Entry[], describe: (e: ConsoleEvent) => string): Entry[] {
	if (!query.value.trim()) return list;
	return list.filter((x) => x.kind === 'gap' || eventMatches(x.event, describe(x.event)));
}

export const eventCount = (list: Entry[]) => list.filter((x) => x.kind === 'event').length;
