// The /api/v1 documents (docs/state-json.md, schema v1) and the live store
// fed by the stream.
import { signal, computed } from '@preact/signals';

export type Metrics = Record<string, number>;

export interface NodeState {
	id: number;
	address: string;
	state: 'observed' | 'unknown';
	observed_at?: string;
	age_ms?: number;
	latency_ms: number;
	error?: string;
	metrics?: Metrics;
	last?: { observed_at: string; metrics: Metrics };
}

export interface State {
	schema_version: 1;
	epoch: string;
	round: number;
	generated_at: string;
	interval_ms: number;
	cluster: { configured: number; observed: number; max_slot: number | null; leading: number[] };
	nodes: NodeState[];
}

export interface ConsoleEvent {
	schema_version: 1;
	id: number;
	epoch: string;
	at: string;
	source: 'console';
	name: string;
	node: number | null;
	data: Record<string, unknown>;
}

/** An entry of the event list: an event, or a marker for events missed. */
export type Entry =
	| { kind: 'event'; event: ConsoleEvent }
	| { kind: 'gap'; reason: 'evicted' | 'console_restarted' | 'unread'; at: number };

export type Connection = 'connecting' | 'live' | 'reconnecting' | 'disconnected';

const keep = 500; // entries the browser holds; the console keeps 2000

export const state = signal<State | null>(null);
/** When the latest state arrived, by the browser's clock. */
export const receivedAt = signal(0);
export const entries = signal<Entry[]>([]);
export const connection = signal<Connection>('connecting');
/** Ticks every second, so ages and staleness re-render. */
export const now = signal(Date.now());
setInterval(() => (now.value = Date.now()), 1000);

/** The view is not current after two intervals without a state. */
export const stale = computed(() => {
	const s = state.value;
	return s !== null && now.value - receivedAt.value > 2 * s.interval_ms;
});

/**
 * How old a reading is now: the console's own age at generated_at plus the
 * time since that state arrived. Uses no wall clock of the browser, which
 * may differ from the console host's.
 */
export function ageMs(readAt: string): number {
	const s = state.value;
	if (!s) return 0;
	return Date.parse(s.generated_at) - Date.parse(readAt) + (now.value - receivedAt.value);
}

let epoch = '';
let lastId = 0;
let failures = 0;
/** Stream events held while a backfill runs, so none overtakes it. */
let held: ConsoleEvent[] | null = null;

function gap(list: Entry[], reason: 'evicted' | 'console_restarted' | 'unread') {
	list.push({ kind: 'gap', reason, at: Date.now() });
}

/**
 * Merges events by id, oldest first. Missed events show as a gap: a jump
 * in ids means the console evicted them; a new epoch (seen on the state
 * first) means it restarted. This covers the stream's own `gap` message,
 * and also a reconnect that carried no Last-Event-ID.
 */
function merge(events: ConsoleEvent[]) {
	const list = [...entries.value];
	for (const e of events) {
		if (e.epoch !== epoch || e.id <= lastId) continue; // another epoch's, or shown
		if (lastId > 0 && e.id > lastId + 1) gap(list, 'evicted');
		list.push({ kind: 'event', event: e });
		lastId = e.id;
	}
	entries.value = list.slice(-keep);
}

function onState(s: State) {
	if (s.epoch !== epoch) {
		if (epoch !== '') {
			const list = [...entries.value];
			gap(list, 'console_restarted');
			entries.value = list.slice(-keep);
		}
		epoch = s.epoch;
		lastId = 0;
	}
	state.value = s;
	receivedAt.value = Date.now();
}

async function backfill() {
	if (held) return; // one already runs, and holds what arrives
	held = [];
	let fetched: ConsoleEvent[] | null = null;
	try {
		const r = await fetch(`/api/v1/events?after=${lastId}`);
		if (r.ok) fetched = ((await r.json()) as { events: ConsoleEvent[] }).events;
	} catch {
		// shown as a gap below
	}
	const live = held;
	held = null;
	if (fetched === null) {
		const list = [...entries.value];
		gap(list, 'unread');
		entries.value = list.slice(-keep);
	} else {
		merge(fetched);
	}
	merge(live);
}

/** Opens the stream; the browser's EventSource retries on its own. */
export function connect() {
	const es = new EventSource('/api/v1/stream');
	es.addEventListener('state', (m) => {
		const reconnected = connection.value !== 'live';
		onState(JSON.parse((m as MessageEvent).data) as State);
		connection.value = 'live';
		failures = 0;
		if (reconnected) void backfill(); // what came while away, by id
	});
	es.addEventListener('event', (m) => {
		const e = JSON.parse((m as MessageEvent).data) as ConsoleEvent;
		if (held) held.push(e);
		else merge([e]);
	});
	es.onerror = () => {
		failures++;
		connection.value = failures >= 3 ? 'disconnected' : 'reconnecting';
		if (es.readyState === EventSource.CLOSED) {
			// Refused outright (for example 503, too many streams): the
			// browser gives up, so retry ourselves, at most every 30 s.
			setTimeout(connect, Math.min(30_000, 2_000 * failures));
		}
	};
}
