import { RagError } from '../core/errors.ts';

export interface RecordRef {
  readonly dataset: string;
  readonly id: string;
}
export interface RecordValue {
  readonly id: string;
  readonly [field: string]: unknown;
}
export type DatasetKind = 'documents' | 'chunks';
export interface Dataset<T extends RecordValue = RecordValue> {
  readonly id: string;
  readonly kind: DatasetKind;
  readonly size: number;
  get(id: string): T | null;
  values(): Iterable<T>;
  materialize(): readonly T[];
}
interface RecordReader<T> {
  readonly size: number;
  get(id: string): T | null | undefined;
  values(): Iterable<T>;
}

// Copy once at the dataset boundary. A provider cannot mutate authoritative
// in-memory records through nested metadata, and disk reads have the same rule.
export function immutable<T>(value: T): T {
  if (value === null || typeof value !== 'object') return value;
  if (Array.isArray(value)) return Object.freeze(value.map(immutable)) as T;
  const out: Record<string, unknown> = {};
  for (const [key, item] of Object.entries(value)) out[key] = immutable(item);
  return Object.freeze(out) as T;
}

export function dataset<T extends RecordValue>(
  id: string,
  kind: DatasetKind,
  reader: RecordReader<T>,
): Dataset<T> {
  const result: Dataset<T> = {
    id,
    kind,
    get size() {
      return reader.size;
    },
    get(key) {
      const value = reader.get(key);
      return value == null ? null : immutable(value);
    },
    *values() {
      for (const value of reader.values()) yield immutable(value);
    },
    materialize() {
      return Object.freeze([...result.values()]);
    },
  };
  return Object.freeze(result);
}
export function memoryDataset<T extends RecordValue>(
  id: string,
  kind: DatasetKind,
  records: Iterable<T>,
): Dataset<T> {
  const entries = new Map<string, T>();
  for (const record of records) {
    if (entries.has(record.id))
      throw new RagError('dataset', `duplicate record '${record.id}' in ${id}`);
    entries.set(record.id, immutable(record));
  }
  return dataset(id, kind, entries);
}
export function referenceKey(ref: RecordRef): string {
  if (!ref || typeof ref.dataset !== 'string' || typeof ref.id !== 'string')
    throw new RagError('reference', 'expected {dataset, id}');
  return JSON.stringify([ref.dataset, ref.id]);
}
export function resolveRecord(
  datasets: ReadonlyMap<string, Dataset>,
  ref: RecordRef,
): RecordValue | null {
  referenceKey(ref);
  const source = datasets.get(ref.dataset);
  if (!source) throw new RagError('reference', `unknown dataset '${ref.dataset}'`);
  return source.get(ref.id);
}
