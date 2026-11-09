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

/** The public ID is an opaque, canonical encoding of dataset and local ID.
 * One identifier crosses JSON, maps, browser caches and model tool arguments;
 * there is no bare-ID lookup and no implicit choice between output versions. */
export type QualifiedRecordID = string & { readonly __qualifiedRecordID: unique symbol };
export function qualify(datasetId: string, localId: string): QualifiedRecordID {
  return referenceKey({ dataset: datasetId, id: localId }) as QualifiedRecordID;
}
export function unqualify(id: string): RecordRef {
  let pair: unknown;
  try {
    pair = JSON.parse(id);
  } catch {
    throw new RagError('reference', 'record ID must include its dataset identity');
  }
  if (
    !Array.isArray(pair) ||
    pair.length !== 2 ||
    pair.some((v) => typeof v !== 'string') ||
    JSON.stringify(pair) !== id
  )
    throw new RagError('reference', 'invalid qualified record ID');
  return { dataset: pair[0], id: pair[1] };
}

export interface ChunkRecord extends RecordValue {
  readonly docId: string;
}
/** Snapshot-local resolver; dataset selection comes from the ID, never precedence. */
export class RecordCatalog {
  private sources = new Map<string, { data: Dataset; documents?: string }>();
  add(data: Dataset, documents?: string): void {
    if (this.sources.has(data.id)) return;
    this.sources.set(data.id, { data, documents });
  }
  get size(): number {
    let size = 0;
    for (const { data } of this.sources.values()) size += data.size;
    return size;
  }
  get(id: string): RecordValue | null {
    const ref = unqualify(id);
    const source = this.sources.get(ref.dataset);
    if (!source)
      throw new RagError('reference', `dataset '${ref.dataset}' is not in this snapshot`);
    const record = source.data.get(ref.id);
    return record ? this.project(record, source.data.id, source.documents) : null;
  }
  has(id: string): boolean {
    return this.get(id) !== null;
  }
  private project(record: RecordValue, datasetId: string, documents?: string): RecordValue {
    return immutable({
      ...record,
      id: qualify(datasetId, record.id),
      ...(typeof record.docId === 'string' && documents
        ? { docId: qualify(documents, record.docId) }
        : {}),
    });
  }
  *values(): IterableIterator<RecordValue> {
    for (const { data, documents } of this.sources.values())
      for (const record of data.values()) yield this.project(record, data.id, documents);
  }
  *keys(): IterableIterator<string> {
    for (const record of this.values()) yield record.id;
  }
  *entries(): IterableIterator<[string, RecordValue]> {
    for (const record of this.values()) yield [record.id, record];
  }
  [Symbol.iterator](): IterableIterator<[string, RecordValue]> {
    return this.entries();
  }
  forEach(callback: (record: RecordValue, id: string) => void): void {
    for (const record of this.values()) callback(record, record.id);
  }
}
