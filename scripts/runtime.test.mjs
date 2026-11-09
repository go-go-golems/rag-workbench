import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import test from 'node:test';
import { setImmediate } from 'node:timers/promises';
import vm from 'node:vm';

const source = await readFile(new URL('../pkg/ragjs/assets/rag.js', import.meta.url), 'utf8');
function runtime(extra = {}) {
  const context = vm.createContext(extra);
  vm.runInContext(source, context, { filename: 'rag.js' });
  return context;
}
const fixture = String.raw`
rag.configure({ files: () => [{ name: 'report.md', text: '# Engine\n\nSnapshot ownership keeps queries consistent during refresh.\n' }] });
const corpus = rag.corpus('reports').from(rag.files()).parse(rag.markdown()).chunk(rag.sections()).index('text', rag.bm25());
const plan = rag.plan('search').over(corpus).signals({ q: 'snapshot' }).retrieve('text', { k: 5 });
`;

test('bundle publishes only rag in a browser and uses CommonJS when supplied', () => {
  const browser = runtime();
  assert.deepEqual(Object.keys(browser), ['rag']);
  const common = runtime({ module: { exports: {} } });
  assert.equal(common.rag, undefined);
  assert.deepEqual(Object.keys(common.module.exports).sort(), Object.keys(browser.rag).sort());
  assert.equal(browser.rag.components().length, 31);
});

test('separate evaluations isolate registration, default engine and request scopes', () => {
  const a = runtime();
  const b = runtime();
  vm.runInContext("rag.define('custom', { phase: 'live', run: (s, ctx, hits) => hits });", a);
  assert.equal(typeof a.rag.plan('a').custom, 'function');
  assert.equal(b.rag.plan('b').custom, undefined);
  assert.notEqual(a.rag.engine(), b.rag.engine());
  const scope = a.rag.newScope(() => {});
  a.rag.withScope(scope, () => {
    assert.equal(a.rag.scope(), scope);
    assert.equal(b.rag.scope(), null);
  });
  assert.equal(a.rag.scope(), null);
});

test('closed stored lambdas and dynamically registered components execute after bundling', async () => {
  const context = runtime();
  const result = await vm.runInContext(
    `(async () => {
    ${fixture}
    rag.define('custom', { phase: 'live', run: (s, ctx, hits) => hits.slice(0, 1) });
    const filtered = plan.custom().map((s, ctx, hits) => hits.map(h => ({ ...h, score: 7 })));
    const result = await rag.run(filtered, { q: 'snapshot' });
    return JSON.stringify({ hits: result.hits, text: result.chunk(result.hits[0].id).text });
  })()`,
    context,
  );
  const parsed = JSON.parse(result);
  assert.equal(parsed.hits.length, 1);
  assert.equal(parsed.hits[0].score, 7);
  assert.match(parsed.text, /Snapshot ownership/);
});

test('scope capture restores nesting, keeps independent listeners, and aborts once', async () => {
  const { rag } = runtime();
  const events = [];
  const outer = rag.newScope((event) => events.push(event));
  const inner = rag.newScope(() => {});
  let aborted = 0;
  outer.signal.addEventListener('abort', () => {
    throw new Error('listener');
  });
  outer.signal.addEventListener('abort', () => {
    aborted++;
  });
  assert.throws(() =>
    rag.withScope(outer, () => {
      assert.throws(() =>
        rag.withScope(inner, () => {
          throw new Error('nested');
        }),
      );
      assert.equal(rag.scope(), outer);
      throw new Error('outer');
    }),
  );
  assert.equal(rag.scope(), null);
  const pending = rag.withScope(outer, async () => {
    assert.equal(rag.scope(), outer);
    await Promise.resolve();
    assert.equal(rag.scope(), null);
  });
  await pending;
  outer.emit({ type: 'start' });
  outer.close();
  outer.emit({ type: 'end' });
  outer.abort('cancel');
  outer.abort('again');
  assert.equal(aborted, 1);
  assert.equal(outer.signal.reason, 'cancel');
  assert.equal(events.length, 1);
});

test('standalone export contains an executable bundle and searches with its embedded corpus', async () => {
  const context = runtime();
  context.library = source;
  const html = await vm.runInContext(
    `(async () => {
    ${fixture}
    rag.configure({ librarySource: () => library });
    return await rag.export(plan);
  })()`,
    context,
  );
  const elements = { q: { value: '', addEventListener() {} }, out: { innerHTML: '' } };
  const browser = vm.createContext({ document: { getElementById: (id) => elements[id] } });
  const scripts = [...html.matchAll(/<script>([\s\S]*?)<\/script>/g)].map((match) => match[1]);
  assert.equal(scripts.length, 2);
  for (const script of scripts) vm.runInContext(script, browser);
  for (let i = 0; i < 20 && !elements.out.innerHTML; i++) await setImmediate();
  assert.match(elements.out.innerHTML, /ownership/);
  assert.match(elements.out.innerHTML, /<mark>Snapshot<\/mark>/);
});

test('runtime source modules have explicit local imports and no dependency cycles', async () => {
  const { readdir } = await import('node:fs/promises');
  const path = await import('node:path');
  const { fileURLToPath } = await import('node:url');
  const ts = await import('typescript');
  const root = fileURLToPath(new URL('../pkg/ragjs/src/', import.meta.url));
  const graph = new Map();
  async function visitDirectory(directory) {
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      const file = path.join(directory, entry.name);
      if (entry.isDirectory()) {
        await visitDirectory(file);
        continue;
      }
      if (!/\.(js|ts)$/.test(file)) continue;
      const sourceFile = ts.createSourceFile(
        file,
        await readFile(file, 'utf8'),
        ts.ScriptTarget.Latest,
        true,
      );
      const deps = [];
      for (const statement of sourceFile.statements) {
        if (!ts.isImportDeclaration(statement) && !ts.isExportDeclaration(statement)) continue;
        const specifier = statement.moduleSpecifier;
        if (!specifier || !ts.isStringLiteral(specifier)) continue;
        assert.ok(specifier.text.startsWith('.'), `runtime cannot import host packages: ${file}`);
        deps.push(path.resolve(path.dirname(file), specifier.text));
      }
      graph.set(file, deps);
    }
  }
  await visitDirectory(root);
  const visited = new Set();
  const stack = [];
  function visit(file) {
    assert.ok(
      !stack.includes(file),
      `dependency cycle: ${[...stack, file].map((f) => path.relative(root, f)).join(' -> ')}`,
    );
    if (visited.has(file)) return;
    assert.ok(graph.has(file), `missing source dependency: ${file}`);
    stack.push(file);
    for (const dependency of graph.get(file)) visit(dependency);
    stack.pop();
    visited.add(file);
  }
  for (const file of graph.keys()) visit(file);
});
