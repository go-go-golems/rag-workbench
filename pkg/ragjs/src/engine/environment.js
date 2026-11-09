// Timing hook (env.timing(kind, name, ms, detail)): ragwb --timings and the serve API
// report compile, build nodes, artifact loads, artifacts() and query nodes.
const timed = (kind, name, ms, detail) => {
  try {
    if (env0.timing) env0.timing(kind, String(name), +(+ms).toFixed(2), detail || '');
  } catch (_) {
    /* timing (or env0 before init) never breaks a run */
  }
};

let env0 = {};

export { env0, timed };
