import { compile } from '../graph/graph.js';
import { defaultEngine } from './default-engine.js';
import { Workspace } from './workspace.js';

function resolveTarget(target, engine) {
  if (target instanceof Workspace)
    return { ir: target.ir, engine: target.engine, name: target.name };
  if (target && target.ir && target.engine)
    return { ir: compile(target.ir), engine: target.engine, name: target.name || target.ir.id };
  const ir = compile(target);
  return { ir, engine: engine || defaultEngine(), name: ir.id };
}

export { resolveTarget };
