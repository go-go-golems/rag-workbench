import { Engine } from '../engine/engine.js';
import { env0 } from '../engine/environment.js';

// ────────────────────────────────────────────────────────────── namespace
let engine0 = null;

function defaultEngine() {
  if (!engine0) engine0 = new Engine(env0);
  return engine0;
}

function configure(env) {
  Object.assign(env0, env);
  if (engine0) engine0.env = env0;
  return env0;
}

export { configure, defaultEngine };
