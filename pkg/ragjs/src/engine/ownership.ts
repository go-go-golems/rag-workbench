/** A resource closes only after its last owning scope releases it. */
export class ResourceRegistry<T extends object> {
  closed = 0;
  private entries = new Map<T, { owners: number; close: () => void }>();
  retain(resource: T, close: () => void): () => void {
    let entry = this.entries.get(resource);
    if (!entry) {
      entry = { owners: 0, close };
      this.entries.set(resource, entry);
    }
    entry.owners++;
    let released = false;
    return () => {
      if (released) return;
      released = true;
      if (--entry.owners === 0) {
        this.entries.delete(resource);
        entry.close();
        this.closed++;
      }
    };
  }
  get size(): number {
    return this.entries.size;
  }
}

export class OwnershipScope {
  private releases: (() => void)[] = [];
  private closed = false;
  own(release: () => void): void {
    if (this.closed) {
      release();
      throw new Error('ownership scope is closed');
    }
    this.releases.push(release);
  }
  close(): void {
    if (this.closed) return;
    this.closed = true;
    let failure: unknown;
    for (const release of this.releases.reverse()) {
      try {
        release();
      } catch (error) {
        failure ??= error;
      }
    }
    this.releases = [];
    if (failure) throw failure;
  }
}
