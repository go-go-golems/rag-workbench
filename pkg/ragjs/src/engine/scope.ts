/** The host supplies this subset; a browser AbortSignal is not required. */
export interface CancellationSignal {
  readonly aborted: boolean;
  readonly reason: unknown;
  addEventListener(type: string, listener: () => void): void;
  removeEventListener(type: string, listener: () => void): void;
}

/** Events remain extensible for custom components; 009 can narrow payloads. */
export interface RuntimeEvent {
  type: string;
  [key: string]: unknown;
}
export interface EventDetail {
  top?: number;
  chunks?: boolean;
}
export type EventSink = (event: RuntimeEvent) => void;
export interface RequestScope {
  readonly signal: CancellationSignal;
  readonly detail: EventDetail;
  emit: EventSink;
  nextRun(): number;
  runs(): number;
  close(): void;
  abort(reason?: unknown): void;
}
export interface ScopeOptions {
  scope?: RequestScope | null;
  signal?: CancellationSignal;
  detail?: EventDetail;
  onEvent?: EventSink;
}

// Only the synchronous start of a call inherits the current scope. Each async
// operation captures its options before yielding; this is not async-local state.
export let currentScope: RequestScope | null = null;

export function newScope(emit: EventSink, detail: EventDetail = {}): RequestScope {
  let runs = 0;
  let open = true;
  const listeners: (() => void)[] = [];
  const signal = {
    aborted: false,
    reason: undefined as unknown,
    addEventListener: (type: string, listener: () => void) => {
      if (type === 'abort') listeners.push(listener);
    },
    removeEventListener: (_type: string, listener: () => void) => {
      const i = listeners.indexOf(listener);
      if (i >= 0) listeners.splice(i, 1);
    },
  };
  return {
    signal,
    detail: { top: 20, chunks: false, ...detail },
    emit: (event) => {
      if (open && event && typeof event === 'object') {
        try {
          emit(event);
        } catch {
          /* A sink error must not fail the query. */
        }
      }
    },
    nextRun: () => ++runs,
    runs: () => runs,
    close: () => {
      open = false;
    },
    abort: (reason) => {
      if (signal.aborted) return;
      signal.aborted = true;
      signal.reason = reason;
      for (const listener of listeners.slice()) {
        try {
          listener();
        } catch {
          /* Keep aborting other listeners. */
        }
      }
    },
  };
}

export function withScope<T>(scope: RequestScope | null | undefined, fn: () => T): T {
  const previous = currentScope;
  currentScope = scope || null;
  try {
    return fn();
  } finally {
    currentScope = previous;
  }
}

// Preserve caller-specific options while adding captured scope options.
export function scoped<T extends ScopeOptions>(opts: T): Omit<T, keyof ScopeOptions> & ScopeOptions;
export function scoped(): ScopeOptions;
export function scoped(opts: ScopeOptions = {}): ScopeOptions {
  const scope = opts.scope !== undefined ? opts.scope : currentScope;
  if (!scope) return opts;
  const run = scope.nextRun ? scope.nextRun() : 0;
  const previous = opts.onEvent;
  return {
    ...opts,
    scope: null,
    signal: opts.signal || scope.signal,
    detail: opts.detail || scope.detail,
    onEvent: (event) => {
      if (previous) {
        try {
          previous(event);
        } catch {
          /* Keep scope event delivery independent. */
        }
      }
      scope.emit(run ? { ...event, run } : event);
    },
  };
}

export function safeEmit(opts?: ScopeOptions): EventSink | null {
  return opts?.onEvent
    ? (event) => {
        try {
          opts.onEvent?.(event);
        } catch {
          /* Listener errors stay local. */
        }
      }
    : null;
}
