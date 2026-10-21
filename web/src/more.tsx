// Phase E: Replication & Durability, Configuration and Not exposed by the
// core. Every number is a stable series or computed from them, and a
// computed one says so (CON-D-1 A).
import { state, ageMs } from './api';
import { Fact, Filtered, StateBadge, age, reading } from './ui';
import { matches, nodeMatches } from './search';

/** The stable series, as the core declares them (D-141 A). */
const stable = [
	'straum_node',
	'straum_slot',
	'straum_leading',
	'straum_serving',
	'straum_commit',
	'straum_applied',
	'straum_log_end',
	'straum_log_durable',
	'straum_lag',
	'straum_outbox_entries',
	'straum_outbox_capacity',
];

export function Durability() {
	const s = state.value;
	if (!s) return null;
	const nodes = s.nodes.filter(nodeMatches);
	return (
		<>
			<p class="note">
				The cluster has one durability mode: a PUBACK is sent, and a message delivered, only after its record is committed,
				that is durable on a majority of the nodes (the core's ADR-0005). That is the broker's decision, shown here as text,
				not something the console measures.
			</p>
			<Filtered shown={nodes.length} total={s.nodes.length} what="nodes" />
			<table class="grid durability">
				<thead>
					<tr>
						<th rowSpan={2}>node</th>
						<th rowSpan={2}>state</th>
						<th colSpan={2} class="group">Local: fsync on this node</th>
						<th colSpan={2} class="group">Quorum: majority durable, as this node knows it</th>
						<th rowSpan={2} class="num">applied</th>
						<th rowSpan={2} class="num">own lag</th>
						<th rowSpan={2}>outbox</th>
						<th rowSpan={2} class="num">age</th>
					</tr>
					<tr>
						<th class="num">durable through</th>
						<th class="num">not yet synced*</th>
						<th class="num">commit</th>
						<th class="num">not yet committed*</th>
					</tr>
				</thead>
				<tbody>
					{nodes.map((n) => {
						const r = reading(n);
						const m = r?.metrics;
						const v = (k: string) => m?.[k] ?? 0;
						const cap = v('straum_outbox_capacity');
						return (
							<tr key={n.id} class={r?.current ? '' : 'stale'} data-node={n.id}>
								<td>
									{n.id}
									{s.cluster.leading.includes(n.id) && <span class="tag">leading</span>}
								</td>
								<td>
									<StateBadge state={n.state} />
								</td>
								{m ? (
									<>
										<td class="num">{v('straum_log_durable')}</td>
										<td class="num">{v('straum_log_end') - v('straum_log_durable')}</td>
										<td class="num">{v('straum_commit')}</td>
										<td class="num">{Math.max(0, v('straum_log_end') - v('straum_commit'))}</td>
										<td class="num">{v('straum_applied')}</td>
										<td class="num">{v('straum_lag')}</td>
										<td>
											<span class="mono">
												{v('straum_outbox_entries')} / {cap}
											</span>
											<div class="bar">
												<i style={{ width: `${cap ? Math.min(100, (100 * v('straum_outbox_entries')) / cap) : 0}%` }} />
											</div>
										</td>
										<td class="num">{r && age(ageMs(r.readAt))}</td>
									</>
								) : (
									<td colSpan={8} class="none">
										no answer yet in this epoch
									</td>
								)}
							</tr>
						);
					})}
				</tbody>
			</table>
			<p class="note foot-note">
				* Computed by the console from the node's own numbers: <span class="mono">log_end − log_durable</span> and{' '}
				<span class="mono">log_end − commit</span>. A locally durable position does not mean the record is committed: commit
				needs a majority. Positions are per node and never added up.
			</p>
		</>
	);
}

export function Configuration() {
	const s = state.value;
	if (!s) return null;
	const nodes = s.nodes.filter(nodeMatches);
	return (
		<>
			<div class="facts">
				<Fact value={s.nodes.length} label="nodes configured" />
				<Fact value={age(s.interval_ms)} label="between polls" />
				<Fact value={age(s.timeout_ms)} label="to wait for each node" />
			</div>
			<p class="note">
				The console's own configuration (<span class="mono">--node</span>, <span class="mono">--interval</span>). The broker
				exposes none of its own; the console only checks that each node reports the id it was given.
			</p>
			<Filtered shown={nodes.length} total={s.nodes.length} what="nodes" />
			<table class="grid">
				<thead>
					<tr>
						<th>configured id</th>
						<th>metrics address</th>
						<th>reported id</th>
						<th>identity</th>
						<th>stable series rendered</th>
					</tr>
				</thead>
				<tbody>
					{nodes.map((n) => {
						const r = reading(n);
						const reported = r?.metrics['straum_node'];
						const rendered = r ? stable.filter((k) => k in r.metrics).length : 0;
						return (
							<tr key={n.id} class={r?.current ? '' : 'stale'} data-node={n.id}>
								<td>{n.id}</td>
								<td class="mono">{n.address}</td>
								<td class="num">{reported ?? '—'}</td>
								<td>{reported === undefined ? 'not read yet' : reported === n.id ? 'matches' : 'reports another id'}</td>
								<td class="num">{r ? `${rendered} / ${stable.length}` : '—'}</td>
							</tr>
						);
					})}
				</tbody>
			</table>
			<h3>The stable series</h3>
			<p class="note">
				The console reads only these, the series the core declares stable (D-141 A); a node that does not render all of them
				is unknown.
			</p>
			<ul class="series">
				{stable.map((k) => (
					<li class="mono">{k}</li>
				))}
			</ul>
		</>
	);
}

/**
 * What the console cannot show, grouped by the screen it would be on, why,
 * and the broker change that would provide it (plan §5, §16.2).
 */
const missing: [string, [string, string, string][]][] = [
	[
		'Cluster & Leader',
		[
			['Takeovers started, slots led, step-downs', 'Unstable counters', 'Stabilise them (C1, straum#32)'],
			['Successions', 'An unstable counter', 'Stabilise it (no C-number yet)'],
			['Takeover duration', 'An unstable histogram', 'Stabilise it (no C-number yet)'],
			['Peer link up or down', 'Known to the link tasks, not exposed', 'A per-peer link gauge (C3)'],
		],
	],
	[
		'Replication & Durability',
		[
			['Per-replica lag (the sequencer\u2019s view)', 'An unstable, labelled gauge', 'Stabilise it (C1, straum#32)'],
			['Catch-up progress: lag rate, progress age, bytes and positions in flight', 'Unstable series', 'Stabilise them (no C-number yet)'],
			['Proposal → commit latency', 'An unstable histogram', 'Stabilise it (C1, straum#32)'],
			['WAL write → locally durable latency', 'An unstable histogram', 'Stabilise it (no C-number yet)'],
			['Read-blocked spells (backpressure)', 'Unstable series', 'Stabilise them (no C-number yet)'],
			['Position history and charts', 'Schema v1 keeps no history', 'A history endpoint in a later schema'],
		],
	],
	[
		'Throughput & Latency',
		[
			['Publish, PUBACK and delivery rates', 'Rendered only as unstable counters', 'Stabilise them (C1, straum#32)'],
			['Bytes in and out', 'Not counted', 'Byte counters (C6)'],
			['Publish → delivery latency', 'Not measured anywhere', 'A new measurement (no C-number yet)'],
		],
	],
	[
		'MQTT Gateway',
		[
			['MQTT connections', 'An unstable gauge', 'Stabilise it (C1, straum#32)'],
			['Pending messages, pending and waiting bytes (backlog)', 'Unstable gauges', 'Stabilise them (no C-number yet)'],
			['Sessions, subscriptions, retained messages', 'Held in the core, not exposed', 'Gauges from the applied state (C2)'],
			['Clients and sessions, one by one', 'No inspection API', 'An inspection API with authentication (C8, D-141 C)'],
		],
	],
	[
		'Errors & Pressure',
		[
			['MQTT closes, all', 'An unstable counter', 'Stabilise it (C1, straum#32)'],
			['Keep-alive and commit-timeout closes, at-most-once deliveries dropped', 'Unstable counters', 'Stabilise them (no C-number yet)'],
			['Own proposals refused at a full outbox, or given up', 'Unstable counters', 'Stabilise them (no C-number yet)'],
			['Forwards refused or dropped, link drops', 'Unstable counters', 'Stabilise them (no C-number yet)'],
			['Refused logins and topics', 'The gateway answers with reason codes; nothing counts them', 'A counter (no C-number yet)'],
			['Storage errors', 'The node stops, so it shows here only as unknown', 'A signal before it stops (no C-number yet)'],
		],
	],
	[
		'System',
		[
			['Version, uptime, restarts', 'No build or start-time series', 'Build info and start time (C4)'],
			['The effective configuration', 'Logged once at startup, not exposed', 'A configuration digest (C4); the full settings have no C-number'],
			['CPU, memory, file descriptors, disk', 'No process series', 'Standard process metrics (C5)'],
			['The broker\u2019s own events and logs', 'Plain text on the node\u2019s stdout', 'Structured logs or an event ring (C7)'],
			['Administration (handoff, …)', 'No admin API; handoff is SIGUSR1 on the host', 'The admin API with authentication (C10, D-141 C)'],
		],
	],
];

export function NotExposed() {
	const groups = missing
		.map(([screen, rows]) => [screen, rows.filter((r) => matches(screen, ...r))] as const)
		.filter(([, rows]) => rows.length > 0);
	const total = missing.reduce((n, [, rows]) => n + rows.length, 0);
	const shown = groups.reduce((n, [, rows]) => n + rows.length, 0);
	return (
		<>
			<p class="note">
				The console shows only what the broker reports in its stable series. These are the rest, by the screen they would
				be on, with the broker change that would let the console show them. C-numbers are those of the console's plan;
				straum#32 asks the core for C1.
			</p>
			<Filtered shown={shown} total={total} what="rows" />
			<table class="grid not-exposed">
				<thead>
					<tr>
						<th>what an operator would want</th>
						<th>why the console cannot show it</th>
						<th>what would provide it</th>
					</tr>
				</thead>
				{groups.map(([screen, rows]) => (
					<tbody data-screen={screen}>
						<tr class="group">
							<th colSpan={3}>{screen}</th>
						</tr>
						{rows.map(([what, why, fix]) => (
							<tr>
								<td>{what}</td>
								<td>{why}</td>
								<td>{fix}</td>
							</tr>
						))}
					</tbody>
				))}
			</table>
		</>
	);
}
