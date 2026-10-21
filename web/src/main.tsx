import { render } from 'preact';
import { connect } from './api';
import { App } from './app';
import './console.css';

connect();
render(<App />, document.getElementById('app')!);
