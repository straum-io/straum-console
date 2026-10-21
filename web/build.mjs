// Builds web/dist, which the Go binary embeds: the app bundle, its CSS, and
// the static files of public/ as they are. `--watch` rebuilds on change.
import * as esbuild from 'esbuild';
import { cpSync, readFileSync, rmSync, writeFileSync } from 'node:fs';

const watch = process.argv.includes('--watch');

rmSync('dist', { recursive: true, force: true });
cpSync('public', 'dist', { recursive: true });

const options = {
	entryPoints: { app: 'src/main.tsx' },
	outdir: 'dist',
	bundle: true,
	minify: !watch,
	format: 'esm',
	target: 'es2022',
	jsx: 'automatic',
	jsxImportSource: 'preact',
	// Fonts stay files under dist/fonts, referenced by their own URL.
	external: ['/fonts/*'],
	legalComments: 'none',
	logLevel: 'info',
};

// The bundle carries code of these packages; their licences ship with it.
const bundled = ['preact', '@preact/signals', '@preact/signals-core'];
writeFileSync(
	'dist/licenses.txt',
	bundled
		.map((p) => {
			const { version, license } = JSON.parse(readFileSync(`node_modules/${p}/package.json`, 'utf8'));
			return `${p} ${version} (${license})\n\n${readFileSync(`node_modules/${p}/LICENSE`, 'utf8').trim()}\n`;
		})
		.join('\n----\n\n'),
);

if (watch) {
	await (await esbuild.context(options)).watch();
} else {
	await esbuild.build(options);
}
