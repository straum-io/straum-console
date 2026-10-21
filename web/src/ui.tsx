// Small parts the screens share. Every value is shown with the node it came
// from and how old it is; nothing here draws a health verdict.
import type { ComponentChildren } from 'preact';
import { ageMs, type ConsoleEvent, type Entry, type Metrics, type NodeState } from './api';

export function age(ms: number): string {
	if (ms < 1000) return `${Math.max(0, Math.round(ms))} ms`;
	if (ms < 60_000) return `${(ms / 1000).toFixed(1)} s`;
	if (ms < 3_600_000) return `${Math.floor(ms / 60_000)} min`;
	return `${Math.floor(ms / 3_600_000)} h`;
}

export function clock(iso: string): string {
	// Naming fractionalSecondDigits drops the default fields, so all are named.
	return new Date(iso).toLocaleTimeString(undefined, {
		hour: '2-digit',
		minute: '2-digit',
		second: '2-digit',
		fractionalSecondDigits: 3,
		hour12: false,
	});
}

export function Fact({ value, label }: { value: ComponentChildren; label: string }) {
	return (
		<div class="fact">
			<b>{value}</b>
			<span>{label}</span>
		</div>
	);
}

export function StateBadge({ state }: { state: NodeState['state'] }) {
	return <span class={`state ${state}`}>{state}</span>;
}

/** The values a node card lists, in the order an operator reads them. */
const rows: [string, string][] = [
	['leading', 'straum_leading'],
	['serving', 'straum_serving'],
	['slot', 'straum_slot'],
	['commit', 'straum_commit'],
	['applied', 'straum_applied'],
	['log end', 'straum_log_end'],
	['locally durable', 'straum_log_durable'],
	['lag', 'straum_lag'],
];

/** The values to show and when they were read: current, last known, or none. */
export function reading(n: NodeState): { metrics: Metrics; readAt: string; current: boolean } | null {
	if (n.state === 'observed' && n.metrics && n.observed_at) return { metrics: n.metrics, readAt: n.observed_at, current: true };
	if (n.last) return { metrics: n.last.metrics, readAt: n.last.observed_at, current: false };
	return null;
}

export function NodeCard({ node, leading }: { node: NodeState; leading: boolean }) {
	const r = reading(node);
	const m = r?.metrics;
	const entries = m ? (m['straum_outbox_entries'] ?? 0) : 0;
	const capacity = m ? (m['straum_outbox_capacity'] ?? 0) : 0;
	return (
		<article class={`card ${r?.current ? '' : 'stale'}`} data-node={node.id}>
			<h2>
				node {node.id} <StateBadge state={node.state} />
				{leading && <span class="tag">leading</span>}
			</h2>
			<div class="addr">{node.address}</div>
			{m ? (
				<dl>
					{rows.map(([label, key]) => (
						<>
							<dt>{label}</dt>
							<dd>{m[key]}</dd>
						</>
					))}
					<dt>outbox</dt>
					<dd>
						{entries} / {capacity}
					</dd>
				</dl>
			) : (
				<p class="none">no answer yet in this epoch</p>
			)}
			{m && (
				<div class="bar">
					<i style={{ width: `${capacity ? Math.min(100, (100 * entries) / capacity) : 0}%` }} />
				</div>
			)}
			<div class="foot">
				{node.state === 'unknown' && <span class="err">{node.error}</span>}
				{r && (
					<span>
						{r.current ? 'read' : 'last values, read'} {age(ageMs(r.readAt))} ago
						{r.current && ` · took ${node.latency_ms} ms`}
					</span>
				)}
			</div>
		</article>
	);
}

/** One line per event, in words, with the raw data on hover. */
export function describe(e: ConsoleEvent): string {
	const d = e.data;
	const node = e.node === null ? '' : `node ${e.node} · `;
	switch (e.name) {
		case 'console.node.unreachable':
			return `${node}${String(d['error'] ?? '')}`;
		case 'console.node.reachable':
			return `${node}unseen for ${age(Number(d['unseen_for_ms']))}`;
		case 'console.node.id_mismatch':
			return `${node}reports id ${String(d['reported'])}`;
		case 'console.collector.overrun':
			return `${String(d['skipped'])} poll ticks skipped`;
		case 'cluster.max_slot.changed':
			return `${String(d['from'])} → ${String(d['to'])}`;
		case 'cluster.leading.changed':
			return `[${String(d['from'])}] → [${String(d['to'])}] at slot ${String(d['max_slot'])}`;
		case 'node.serving.changed':
			return `${node}${String(d['from'])} → ${String(d['to'])}`;
		case 'node.lag.started':
			return `${node}lag ${String(d['lag'])}`;
		case 'node.lag.cleared':
			return `${node}was ${String(d['lag_was'])}`;
		case 'node.outbox.full':
			return `${node}${String(d['entries'])} / ${String(d['capacity'])}`;
		default:
			return `${node}${JSON.stringify(d)}`;
	}
}

export function EventRows({ list }: { list: Entry[] }) {
	if (list.length === 0) return <p class="none">No events yet: nothing has changed between two rounds.</p>;
	return (
		<table class="events">
			<tbody>
				{list.map((x) =>
					x.kind === 'gap' ? (
						<tr class="gap" key={`gap-${x.at}`}>
							<td colSpan={3}>
								{x.reason === 'evicted'
									? 'events missed here: the console no longer holds them'
									: x.reason === 'unread'
										? 'events before this could not be read from the console'
										: 'the console restarted: events before this are from its previous run'}
							</td>
						</tr>
					) : (
						<tr key={`${x.event.epoch}/${x.event.id}`} title={JSON.stringify(x.event.data)}>
							<td class="t">{clock(x.event.at)}</td>
							<td class="n">{x.event.name}</td>
							<td>{describe(x.event)}</td>
						</tr>
					),
				)}
			</tbody>
		</table>
	);
}
