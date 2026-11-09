// Run from repository root. Local synthetic data only; no provider/network calls.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import vm from 'node:vm';
const source = readFileSync('pkg/ragjs/assets/rag.js', 'utf8');
const context = vm.createContext({});
vm.runInContext(source, context);
const { rag } = context;
const records = readFileSync('pkg/ragjs/src/engine/records.js', 'utf8');
const unionChunks = vm.runInNewContext(records.slice(records.indexOf('function unionChunks'), records.indexOf('class Outputs')) + '\nunionChunks');
const old = { id: 'same', text: 'old' }, newer = { id: 'same', text: 'new' };
const cold = new Map([[old.id, old], [newer.id, newer]]).get('same');
const warm = unionChunks(new Map(), [new Map([[old.id, old]]), new Map([[newer.id, newer]])]).get('same');
assert.equal(cold.text, 'new'); assert.equal(warm.text, 'old');
console.log(JSON.stringify({probe:'duplicate precedence',cold:cold.text,warm:warm.text}));
vm.runInContext(`
rag.configure({files:()=>[{name:'report.md',text:'# Engine\\n\\nSnapshot ownership keeps queries consistent.'}]});
const corpus = rag.corpus('reports').from(rag.files()).parse(rag.markdown()).chunk(rag.sections({maxTokens:450})).index('text',rag.bm25());
const plan = rag.plan('search').over(corpus).signals({q:'',k:10}).retrieve('text',{k:30}).filter.where().limit();
`, context);
const result = await vm.runInContext("rag.run(plan,{q:'snapshot'})",context);
assert.ok(result.hits.length); console.log(JSON.stringify({probe:'guide fluent example with memory source',hits:result.hits.length,errors:result.errors}));
const ir = vm.runInContext('rag.compile(plan)',context);
const engine = rag.engine();
const optsA = {signal:{aborted:false}}, optsB = {signal:{aborted:false}};
let captured, finish;
const oldBuild=engine.build;
engine.build=(_ir,opts)=>{captured=opts; return new Promise(resolve=>{finish=resolve;});};
const pA=engine.buildShared(ir,optsA), pB=engine.buildShared(ir,optsB);
assert.equal(pA,pB); assert.equal(captured.signal,optsA.signal);
finish({ok:true}); await pA; engine.build=oldBuild;
console.log(JSON.stringify({probe:'shared build',samePromise:pA===pB,usesFirstSignal:captured.signal===optsA.signal}));
// Synthetic handle counts exercise the actual GC method, not native filesystem readers.
let closed=0;
const resource={__ix:{close(){closed++;}}};
const savedMemo=engine.memo, savedDigests=engine.digests;
engine.memo=new Map([['obsolete',{value:resource}]]); engine.retired=[]; engine.digests=()=>new Map();
engine.gc(ir); const afterFirst=closed; engine.gc(ir);
assert.equal(afterFirst,0); assert.equal(closed,1);
console.log(JSON.stringify({probe:'GC cycles with external resource reference',afterFirst,afterSecond:closed}));
engine.memo=savedMemo; engine.digests=savedDigests;
const routed=vm.runInContext("rag.compile(rag.plan('routed').over(corpus).signals({q:''}).classify({id:'route',questions:{needs:{type:'noul',instructions:'Is search needed?'}}}).retrieve('text',{when:{decision:'route.needs',min:0.5}}))",context);
let renameError=''; try {rag.applyOps(routed,[{op:'rename',node:'route',to:'classify'}]);}catch(e){renameError=e.message;}
assert.match(renameError,/route/); console.log(JSON.stringify({probe:'decision rename',error:renameError}));
let exportError='';try {await rag.export(routed);}catch(e){exportError=e.message;}
assert.match(exportError,/route/);console.log(JSON.stringify({probe:'classified lexical export',error:exportError}));
