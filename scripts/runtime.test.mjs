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
  assert.equal(browser.rag.components().length, 32);
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

test('graph rename rewrites declared decision and index references atomically', () => {
  const context = runtime();
  const result = vm.runInContext(
    `(() => {
    ${fixture}
    const routed = plan.classify({id:'route',questions:{needs:{type:'noul',question:'Search?'}}})
      .retrieve('text',{id:'gated',when:{decision:'route.needs',min:0.5}});
    const original=rag.compile(routed);
    const renamed=rag.applyOps(original,[{op:'rename',node:'route',to:'routing'},{op:'rename',node:'ix-text',to:'ix-lexical'}]);
    let collision='';try {rag.applyOps(original,[{op:'rename',node:'route',to:'gated'}]);}catch(e){collision=e.message;}
    return JSON.stringify({condition:renamed.node('gated').when.decision,index:renamed.node('gated').p.index,original:original.node('gated').when.decision,collision});
  })()`,
    context,
  );
  const got = JSON.parse(result);
  assert.equal(got.condition, 'routing.needs');
  assert.equal(got.index, 'lexical');
  assert.equal(got.original, 'route.needs');
  assert.match(got.collision, /already exists/);
});

test('lexical export removes model gates in one transaction and retains local submit work', async () => {
  const context = runtime({ library: source });
  const html = await vm.runInContext(
    `(async()=>{
    ${fixture}
    rag.configure({librarySource:()=>library});
    const adaptive=rag.plan('adaptive').over(corpus).signals({q:'snapshot'})
      .classify({id:'route',questions:{needs:{type:'noul',question:'Search?'}}})
      .retrieve('text',{when:{decision:'route.needs',min:0.5}})
      .limit({k:3,trigger:'submit'});
    return rag.export(adaptive);
  })()`,
    context,
  );
  const elements = { q: { value: '', addEventListener() {} }, out: { innerHTML: '' } };
  const browser = vm.createContext({ document: { getElementById: (id) => elements[id] } });
  for (const [, script] of html.matchAll(/<script>([\s\S]*?)<\/script>/g))
    vm.runInContext(script, browser);
  for (let i = 0; i < 20 && !elements.out.innerHTML; i++) await setImmediate();
  assert.match(elements.out.innerHTML, /ownership/);
  assert.equal(vm.runInContext("ir.nodes.some(n=>n.t==='classify')", browser), false);
  assert.equal(vm.runInContext("ir.nodes.some(n=>n.t==='limit')", browser), true);
});

test('dataset identities preserve duplicate local records through fusion and result detachment', async () => {
  const context = runtime();
  const output = await vm.runInContext(
    `(async()=>{
    ${fixture}
    const enriched=corpus.enrich('summary',c=>'enriched '+c.text)
      .index('enriched',rag.bm25());
    const fused=rag.plan('both').over(enriched).signals({q:'snapshot'})
      .retrieve('text').retrieve('enriched').fuse.rrf();
    const result=await rag.run(fused);
    const records=result.hits.map(h=>result.chunk(h.id));
    let bareError='';try{rag.engine().artifacts(rag.compile(fused)).chunks.get(JSON.parse(result.hits[0].id)[1]);}catch(e){bareError=e.code;}
    return JSON.stringify({ids:result.hits.map(h=>JSON.parse(h.id)),records,bareError});
  })()`,
    context,
  );
  const got = JSON.parse(output);
  assert.equal(got.ids.length, 2);
  assert.notEqual(got.ids[0][0], got.ids[1][0]);
  assert.equal(got.ids[0][1], got.ids[1][1]);
  assert.equal(got.records.filter((r) => r.summary).length, 1);
  assert.equal(got.bareError, 'reference');
});

test('named bake datasets are immutable and changed equal-sized content invalidates the value', async () => {
  const context = runtime();
  const result = await vm.runInContext(
    `(async()=>{
    let text='# Note\\n\\nAAAA';
    rag.configure({files:()=>[{name:'note.md',text}]});
    const corpus=rag.corpus('bake').from(rag.files()).parse().chunk(rag.sections())
      .synonyms(rag.bake({datasets:{source:'chunks'}},({datasets})=>({value:datasets.source.materialize()[0].text})))
      .index('text',rag.bm25());
    const ir=rag.compile(corpus),engine=rag.engine();
    await engine.build(ir);const first=engine.artifacts(ir).synonyms.value;
    text='# Note\\n\\nBBBB';await engine.build(ir);const second=engine.artifacts(ir).synonyms.value;
    return JSON.stringify({first,second});
  })()`,
    context,
  );
  const got = JSON.parse(result);
  assert.notEqual(got.first, got.second);
  assert.match(got.second, /BBBB/);
});
