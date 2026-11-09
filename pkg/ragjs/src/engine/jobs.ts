import { RagError } from '../core/errors.ts';
import { newScope, type CancellationSignal, type RuntimeEvent } from './scope.ts';

interface Waiter {
  event?: (event: RuntimeEvent) => void;
  progress?: (progress: unknown) => void;
}
interface JobOptions {
  signal?: CancellationSignal;
  onEvent?: (event: RuntimeEvent) => void;
  onProgress?: (progress: unknown) => void;
}
interface Job<T> {
  scope: ReturnType<typeof newScope>;
  waiters: Set<Waiter>;
  promise: Promise<T>;
  cancelling: boolean;
}

/** One engine-owned task per semantic key; caller cancellation only detaches. */
export class BuildJobs<T> {
  private jobs = new Map<string, Job<T>>();
  private closed = false;

  async run(
    key: string,
    options: JobOptions,
    start: (options: JobOptions) => Promise<T>,
  ): Promise<T> {
    if (this.closed) throw new RagError('shutdown', 'engine is closed');
    if (options.signal?.aborted) throw new RagError('cancelled', 'build waiter cancelled');
    let job = this.jobs.get(key);
    if (job?.cancelling) {
      // A cancelling task still owns its resources. Do not overlap its successor.
      await this.wait(job, options, false).catch((error) => {
        if (options.signal?.aborted) throw error;
      });
      return this.run(key, options, start);
    }
    if (!job) {
      const scope = newScope(() => {});
      const waiters = new Set<Waiter>();
      const created: Job<T> = {
        scope,
        waiters,
        cancelling: false,
        promise: Promise.resolve(undefined as T),
      };
      this.jobs.set(key, created);
      const notify = (kind: 'event' | 'progress', value: RuntimeEvent | unknown) => {
        for (const waiter of [...waiters]) {
          try {
            if (kind === 'event') waiter.event?.(value as RuntimeEvent);
            else waiter.progress?.(value);
          } catch {
            /* Subscriptions cannot fail another caller's build. */
          }
        }
      };
      created.promise = Promise.resolve()
        .then(() =>
          start({
            signal: scope.signal,
            onEvent: (event) => notify('event', event),
            onProgress: (progress) => notify('progress', progress),
          }),
        )
        .finally(() => {
          scope.close();
          if (this.jobs.get(key) === created) this.jobs.delete(key);
        });
      // A cancelled final waiter must not produce an unhandled task rejection.
      void created.promise.catch(() => {});
      job = created;
    }
    return this.wait(job, options, true);
  }

  private wait(job: Job<T>, options: JobOptions, interested: boolean): Promise<T> {
    return new Promise((resolve, reject) => {
      const waiter: Waiter = { event: options.onEvent, progress: options.onProgress };
      let done = false;
      const finish = (error: unknown, value?: T) => {
        if (done) return;
        done = true;
        options.signal?.removeEventListener('abort', abort);
        if (interested) job.waiters.delete(waiter);
        if (error !== null) reject(error);
        else resolve(value as T);
      };
      const abort = () => {
        finish(new RagError('cancelled', 'build waiter cancelled'));
        if (interested && job.waiters.size === 0) {
          job.cancelling = true;
          job.scope.abort('no build waiters');
        }
      };
      if (interested) job.waiters.add(waiter);
      options.signal?.addEventListener('abort', abort);
      if (options.signal?.aborted) abort();
      job.promise.then(
        (value) => finish(null, value),
        (error) => finish(error),
      );
    });
  }

  async close(): Promise<void> {
    this.closed = true;
    const active = [...this.jobs.values()];
    for (const job of active) {
      job.cancelling = true;
      job.scope.abort('engine shutdown');
    }
    await Promise.allSettled(active.map((job) => job.promise));
  }
}
