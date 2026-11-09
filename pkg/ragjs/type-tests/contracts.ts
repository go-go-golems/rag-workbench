import { RagError } from '../src/core/errors.ts';
import { docIdFor } from '../src/core/identity.ts';
import { tokenize } from '../src/core/text.ts';
import { newScope, scoped, withScope } from '../src/engine/scope.ts';

const scope = newScope(
  (event) => {
    String(event.type);
  },
  { top: 5, chunks: true },
);
const result: number = withScope(scope, () => 42);
const options = scoped({ scope, custom: 'preserved' });
const preserved: string = options.custom;
void [result, preserved, new RagError('compile', 'invalid graph'), docIdFor('report.md')];
tokenize('engineering reports', { stem: false });
scope.signal.addEventListener('abort', () => {});
// @ts-expect-error A cancellation signal is read-only to its consumers.
scope.signal.aborted = true;
// @ts-expect-error Scope payloads must include an event discriminator.
scope.emit({ node: 'retrieve' });
// @ts-expect-error The event detail limit must be numeric.
newScope(() => {}, { top: 'five' });
// @ts-expect-error withScope preserves the callback's return type.
const wrong: string = withScope(scope, () => 42);
void wrong;
// @ts-expect-error Document identities take source path strings.
docIdFor(42);
// @ts-expect-error Tokenizer options use a boolean stem flag.
tokenize('text', { stem: 'yes' });
