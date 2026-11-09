// Structural dependencies are visited in one place by compilation and edits.
// Component-specific parameter references supply a setter; opaque dependency
// declarations can be validated, but cannot be silently rewritten.
export type ReferenceKind = 'data' | 'artifact' | 'decision' | 'signal' | 'output';
export interface Reference {
  kind: ReferenceKind;
  target: string;
  set?: (target: string) => void;
}
interface Node {
  id: string;
  in: string[];
  p: Record<string, unknown>;
  when?: { decision?: string; $fn?: string; bindings?: Record<string, string> };
}
interface Definition {
  references?: (params: Record<string, unknown>) => Reference[];
  uses?: (params: Record<string, unknown>) => string[];
  reads?: (params: Record<string, unknown>) => string[];
}
export function references(node: Node, def: Definition = {}): Reference[] {
  const refs: Reference[] = node.in.map((target, i) => ({
    kind: 'data',
    target,
    set: (value) => {
      node.in[i] = value;
    },
  }));
  refs.push(...(def.references?.(node.p) || []));
  for (const target of def.uses?.(node.p) || []) {
    if (!refs.some((r) => r.kind === 'artifact' && r.target === target))
      refs.push({ kind: 'artifact', target });
  }
  for (const target of def.reads?.(node.p) || []) refs.push({ kind: 'signal', target });
  const visit = (value: unknown) => {
    if (!value || typeof value !== 'object') return;
    const object = value as Record<string, unknown>;
    if (typeof object.$bake === 'string') {
      const bindings = object.datasets as Record<string, string> | undefined;
      for (const [name, target] of Object.entries(bindings || {}))
        refs.push({
          kind: 'artifact',
          target,
          set: (value) => {
            if (bindings) bindings[name] = value;
          },
        });
    } else for (const child of Object.values(object)) visit(child);
  };
  visit(node.p);
  const decisions = node.p.decisions as Record<string, string> | undefined;
  for (const [name, target] of Object.entries(decisions || {}))
    refs.push({
      kind: 'decision',
      target,
      set(value) {
        if (decisions) decisions[name] = value;
      },
    });
  const when = node.when;
  if (when?.decision) {
    const split = when.decision.indexOf('.');
    const output = when.decision.slice(split);
    refs.push({
      kind: 'decision',
      target: when.decision.slice(0, split),
      set: (target) => {
        when.decision = target + output;
      },
    });
  }
  for (const [name, target] of Object.entries(when?.bindings || {})) {
    refs.push({
      kind: 'decision',
      target,
      set: (value) => {
        if (when?.bindings) when.bindings[name] = value;
      },
    });
  }
  return refs;
}
export function rewriteReferences(refs: Reference[], from: string, to: string): void {
  const affected = refs.filter((r) => r.target === from);
  if (affected.some((r) => !r.set))
    throw new Error(`reference to '${from}' has no declared rewrite binding`);
  for (const ref of affected) ref.set?.(to);
}
export function outputReferences(outputs: Record<string, string>): Reference[] {
  return Object.entries(outputs).map(([name, target]) => ({
    kind: 'output',
    target,
    set: (value) => {
      outputs[name] = value;
    },
  }));
}
export function indexReferences(params: Record<string, unknown>): Reference[] {
  if (typeof params.index === 'string')
    return [
      {
        kind: 'artifact',
        target: 'ix-' + params.index,
        set: (target) => {
          if (!target.startsWith('ix-')) throw new Error('index node IDs must start with ix-');
          params.index = target.slice(3);
        },
      },
    ];
  if (Array.isArray(params.indexes))
    return params.indexes.map((name, i) => ({
      kind: 'artifact',
      target: 'ix-' + String(name),
      set: (target) => {
        if (!target.startsWith('ix-')) throw new Error('index node IDs must start with ix-');
        (params.indexes as string[])[i] = target.slice(3);
      },
    }));
  return [];
}
