// Line charts of /api/v1/history (docs/state-json.md, History): one line
// per node, drawn as SVG by hand to stay inside the 60 KB budget.
import { signal } from '@preact/signals';
import { useEffect, useRef, useState } from 'preact/hooks';
import { state } from './api';

export interface History {
	schema_version: 1;
	epoch: string;
	series: string;
	derived: boolean;
	interval_ms: number;
	range_ms: number;
	from: string | null;
	to: string | null;
	t: number[];
	nodes: { id: number; address: string; observed_at: (number | null)[]; v: (number | null)[] }[];
}

/** The range every chart shows; one choice for the whole page. */
export const range = signal<'15m' | '1h'>('15m');

// Categorical slots in configuration order; a node past the eighth is drawn
// in muted ink and told apart in the legend and the readout.
const slots = 8;
const color = (i: number) => (i < slots ? `var(--series-${i + 1})` : 'var(--st-text-2)');

const W = 640;
const H = 180;
const pad = { l: 56, r: 12, t: 10, b: 22 };

function fmt(v: number): string {
	if (Math.abs(v) >= 1e6) return `${(v / 1e6).toFixed(1)}M`;
	if (Math.abs(v) >= 1e4) return `${(v / 1e3).toFixed(0)}k`;
	return Number.isInteger(v) ? String(v) : v.toFixed(1);
}

/**
 * Adds the rounds of `d` newer than the last one held to `old`, and drops
 * those past the range. A new epoch is a restarted console: its rounds all
 * postdate `since`, so the answer is its whole history. Another range is a
 * new start.
 */
function merge(old: History | null, d: History): History {
	if (!old || old.epoch !== d.epoch || old.range_ms !== d.range_ms) return d;
	const last = old.t[old.t.length - 1] ?? -Infinity;
	let from = d.t.findIndex((x) => x > last);
	if (from < 0) from = d.t.length;
	const t = old.t.concat(d.t.slice(from));
	const cut = t.findIndex((x) => x >= t[t.length - 1]! - d.range_ms);
	return {
		...d,
		t: t.slice(cut),
		nodes: old.nodes.map((n, i) => ({
			...n,
			observed_at: n.observed_at.concat(d.nodes[i]!.observed_at.slice(from)).slice(cut),
			v: n.v.concat(d.nodes[i]!.v.slice(from)).slice(cut),
		})),
	};
}

/**
 * Fetches the range of every series once, then after a round at most every
 * 15 s only the rounds since the last one held, in one request for all of
 * them: a GET to the console, never a scrape. At 5 s, 64 pages took the
 * soak past its 1 % CPU budget (docs/performance.md); 15 s is about 10 px
 * of a 15 min chart. A new range fetches in full at once, and an answer for
 * a range no longer shown is dropped.
 */
function useHistories(series: string[]): (History | null)[] {
	const [held, setHeld] = useState<{ r: string; docs: History[] } | null>(null);
	const [fetched, setFetched] = useState({ r: '', at: 0 });
	const shown = useRef('');
	const round = state.value?.round;
	const r = range.value;
	shown.current = r;
	const docs = held?.r === r ? held.docs : null;
	useEffect(() => {
		if (fetched.r === r && Date.now() - fetched.at < 15000) return;
		setFetched({ r, at: Date.now() });
		const t = docs?.[0]?.t;
		const since = t?.length ? `&since=${t[t.length - 1]}` : '';
		fetch(`/api/v1/history?series=${series.join(',')}&range=${r}${since}`)
			.then((res) => (res.ok ? (res.json() as Promise<{ histories: History[] }>) : null))
			.then((d) => {
				if (shown.current !== r) return;
				setHeld((old) => (d ? { r, docs: d.histories.map((h, i) => merge(old?.r === r ? old.docs[i]! : null, h)) } : null));
			})
			.catch(() => setHeld(null));
	}, [round, r, fetched]);
	return series.map((_, i) => docs?.[i] ?? null);
}

/** A page's charts, under one range picker, sharing one request. */
export function Charts({ charts }: { charts: { series: string; title: string; unit: string }[] }) {
	const docs = useHistories(charts.map((c) => c.series));
	return (
		<>
			<RangePicker />
			<div class="charts">
				{charts.map((c, i) => (
					<Chart {...c} doc={docs[i]!} />
				))}
			</div>
		</>
	);
}

function RangePicker() {
	return (
		<div class="range" role="group" aria-label="history range">
			{(['15m', '1h'] as const).map((r) => (
				<button type="button" class={range.value === r ? 'on' : ''} aria-pressed={range.value === r} onClick={() => (range.value = r)}>
					{r === '15m' ? '15 min' : '1 h'}
				</button>
			))}
		</div>
	);
}

function Chart({ series, title, unit, doc }: { series: string; title: string; unit: string; doc: History | null }) {
	const [hover, setHover] = useState<number | null>(null);
	const ids = state.value?.nodes.map((n) => n.id) ?? [];
	const head = (
		<h3>
			{title}
			{doc?.derived && <span class="tag">derived</span>}
		</h3>
	);
	if (!doc || doc.t.length < 2)
		return (
			<figure class="chart" data-series={series}>
				{head}
				<p class="none">No history yet: it builds up from the console's start, one point per round.</p>
			</figure>
		);
	const t0 = doc.t[0]!;
	const t1 = doc.t[doc.t.length - 1]!;
	let max = 0;
	let min = 0;
	for (const n of doc.nodes) for (const v of n.v) if (v !== null) (max = Math.max(max, v)), (min = Math.min(min, v));
	if (max === min) max = min + 1;
	const x = (t: number) => pad.l + ((t - t0) / (t1 - t0 || 1)) * (W - pad.l - pad.r);
	const y = (v: number) => pad.t + (1 - (v - min) / (max - min)) * (H - pad.t - pad.b);
	const ticks = [min, (min + max) / 2, max];
	const at = hover ?? doc.t.length - 1;
	const onMove = (e: PointerEvent) => {
		const svg = e.currentTarget as SVGSVGElement;
		const px = ((e.clientX - svg.getBoundingClientRect().left) / svg.clientWidth) * W;
		let best = 0;
		for (let i = 1; i < doc.t.length; i++) if (Math.abs(x(doc.t[i]!) - px) < Math.abs(x(doc.t[best]!) - px)) best = i;
		setHover(best);
	};
	return (
		<figure class="chart" data-series={series}>
			{head}
			<svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={`${title}, ${unit}, per node`} onPointerMove={onMove} onPointerLeave={() => setHover(null)}>
				{ticks.map((v) => (
					<g class="grid-line">
						<line x1={pad.l} x2={W - pad.r} y1={y(v)} y2={y(v)} />
						<text x={pad.l - 6} y={y(v) + 4} text-anchor="end">
							{fmt(v)}
						</text>
					</g>
				))}
				<text class="axis" x={pad.l} y={H - 4}>
					{new Date(t0).toLocaleTimeString()}
				</text>
				<text class="axis" x={W - pad.r} y={H - 4} text-anchor="end">
					{new Date(t1).toLocaleTimeString()}
				</text>
				{doc.nodes.map((n) => {
					// A null breaks the line: no value is drawn where none was read.
					let d = '';
					let pen = false;
					n.v.forEach((v, i) => {
						if (v === null) return void (pen = false);
						d += `${pen ? 'L' : 'M'}${x(doc.t[i]!).toFixed(1)} ${y(v).toFixed(1)}`;
						pen = true;
					});
					return <path d={d} stroke={color(ids.indexOf(n.id))} data-node={n.id} />;
				})}
				{hover !== null && <line class="cross" x1={x(doc.t[at]!)} x2={x(doc.t[at]!)} y1={pad.t} y2={H - pad.b} />}
			</svg>
			<figcaption>
				<span class="when">
					{hover === null ? 'latest, ' : ''}
					{new Date(doc.t[at]!).toLocaleTimeString()}
				</span>
				{doc.nodes.map((n) => (
					<span class="key" data-node={n.id}>
						<i style={{ background: color(ids.indexOf(n.id)) }} />
						node {n.id} <b>{n.v[at] === null || n.v[at] === undefined ? '—' : fmt(n.v[at]!)}</b>
					</span>
				))}
				<span class="unit">{unit}</span>
			</figcaption>
		</figure>
	);
}
