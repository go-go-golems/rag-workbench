import { RagError } from '../core/errors.ts';

/** Copy only the result's promised JSON data; reject hidden resource instances. */
export function detach<T>(value: T, maxBytes = 16 * 1024 * 1024): T {
  const ancestors = new Set<object>();
  let bytes = 0;
  const copy = (item: unknown, depth: number): unknown => {
    bytes += 8;
    if (bytes > maxBytes) throw new RagError('result_limit', 'result exceeds byte budget');
    if (depth > 100) throw new RagError('result_limit', 'result nesting exceeds 100 levels');
    if (item === null || item === undefined || typeof item === 'boolean') return item;
    if (typeof item === 'string') {
      bytes += item.length * 2;
      if (bytes > maxBytes) throw new RagError('result_limit', 'result exceeds byte budget');
      return item;
    }
    if (typeof item === 'number') {
      if (!Number.isFinite(item))
        throw new RagError('result', 'result contains a non-finite number');
      bytes += 8;
      return item;
    }
    if (typeof item !== 'object')
      throw new RagError('result', 'result must contain only plain data');
    if (ancestors.has(item)) throw new RagError('result', 'result contains a cycle');
    if (
      !Array.isArray(item) &&
      Object.getPrototypeOf(item) !== Object.prototype &&
      Object.getPrototypeOf(item) !== null
    )
      throw new RagError('result', 'result contains an instance or resource');
    ancestors.add(item);
    let out: unknown;
    if (Array.isArray(item)) out = item.map((value) => copy(value, depth + 1));
    else {
      const result: Record<string, unknown> = {};
      for (const [key, child] of Object.entries(item)) {
        copy(key, depth + 1);
        result[key] = copy(child, depth + 1);
      }
      out = result;
    }
    ancestors.delete(item);
    return out;
  };
  return copy(value, 0) as T;
}
