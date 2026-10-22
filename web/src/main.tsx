import { render } from 'preact';
import { start } from './api';
import { App } from './app';
import './console.css';

void start();
render(<App />, document.getElementById('app')!);
