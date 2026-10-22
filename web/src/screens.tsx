import { entries, state, ageMs, type State } from './api';
import { EventRows, Fact, Filtered, NodeCard, StateBadge, age, describe, reading } from './ui';
import { eventCount, filterEntries, nodeMatches } from './search';
import { Charts } from './chart';

function leadingText(s: State): string {
	const l = s.cluster.leading;
	if (l.length === 0) return 'none';
	return l.map((id) => `node ${id}`).join(', ');
}

export function Overview() {
	const s = state.value;
	if (!s) return null;
	const nodes = s.nodes.filter(nodeMatches);
	const recent = filterEntries(entries.value, describe).slice(-8).reverse();
	return (
		<>
			<div class="facts">
				<Fact value={`${s.cluster.observed} / ${s.cluster.configured}`} label="nodes observed this round" />
				<Fact value={leadingText(s)} label={s.cluster.leading.length > 1 ? 'report leading (both shown as reported)' : 'reports leading'} />
				<Fact value={s.cluster.max_slot ?? '—'} label="highest slot reported" />
			</div>
			<Charts
				charts={[
					{ series: 'commit_rate', title: 'Commit rate', unit: "records/s, as each node knows it; the leader's is the cluster's" },
				]}
			/>
			<Filtered shown={nodes.length} total={s.nodes.length} what="nodes" />
			<div class="cards">
				{nodes.map((n) => (
					<NodeCard key={n.id} node={n} leading={s.cluster.leading.includes(n.id)} />
				))}
			</div>
			<h3>Recent events</h3>
			<EventRows list={recent} />
		</>
	);
}

const columns: [string, string][] = [
	['slot', 'straum_slot'],
	['leading', 'straum_leading'],
	['serving', 'straum_serving'],
	['commit', 'straum_commit'],
	['applied', 'straum_applied'],
	['log end', 'straum_log_end'],
	['durable', 'straum_log_durable'],
	['lag', 'straum_lag'],
];

export function Cluster() {
	const s = state.value;
	if (!s) return null;
	const nodes = s.nodes.filter(nodeMatches);
	return (
		<>
			<div class="facts">
				<Fact value={leadingText(s)} label="reports straum_leading = 1" />
				<Fact value={s.cluster.max_slot ?? '—'} label="highest slot reported" />
				<Fact value={`${s.cluster.observed} / ${s.cluster.configured}`} label="nodes observed this round" />
			</div>
			<p class="note">
				Each row is what that node reports about itself. Commit, applied and log positions are per node: the console never
				adds them up. A node that does not answer keeps its last values, dimmed, with their age.
			</p>
			<Charts
				charts={[
					{ series: 'straum_slot', title: 'Slot', unit: 'straum_slot' },
					{ series: 'straum_leading', title: 'Reports leading', unit: 'straum_leading, 1 or 0' },
				]}
			/>
			<Filtered shown={nodes.length} total={s.nodes.length} what="nodes" />
			<table class="grid">
				<thead>
					<tr>
						<th>node</th>
						<th>address</th>
						<th>state</th>
						{columns.map(([label]) => (
							<th class="num">{label}</th>
						))}
						<th class="num">outbox</th>
						<th class="num">age</th>
					</tr>
				</thead>
				<tbody>
					{nodes.map((n) => {
						const r = reading(n);
						return (
							<tr key={n.id} class={r?.current ? '' : 'stale'} data-node={n.id}>
								<td>{n.id}</td>
								<td class="mono">{n.address}</td>
								<td>
									<StateBadge state={n.state} />
								</td>
								{columns.map(([, key]) => (
									<td class="num">{r ? r.metrics[key] : '—'}</td>
								))}
								<td class="num">{r ? `${r.metrics['straum_outbox_entries']} / ${r.metrics['straum_outbox_capacity']}` : '—'}</td>
								<td class="num">{r ? age(ageMs(r.readAt)) : '—'}</td>
							</tr>
						);
					})}
				</tbody>
			</table>
		</>
	);
}

export function Events() {
	return (
		<>
			<p class="note">
				Each event is the console's, computed from two consecutive rounds; the broker itself reports none. The newest is
				first.
			</p>
			<Filtered shown={eventCount(filterEntries(entries.value, describe))} total={eventCount(entries.value)} what="events" />
			<EventRows list={[...filterEntries(entries.value, describe)].reverse()} />
		</>
	);
}
