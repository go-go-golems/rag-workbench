// One-time mechanical extraction from the documented baseline. Not a build step.
import ts from 'typescript';
import { mkdirSync, writeFileSync, readFileSync } from 'node:fs';
import path from 'node:path';
const original = readFileSync(process.argv[2], 'utf8');
const lines = original.split('\n');
const source = lines.map((line, i) => i >= 16 && i < 2696 ? line.replace(/^  /, '') : '').join('\n');
const filename = path.resolve('extraction-input.js');
const host = ts.createCompilerHost({ allowJs: true });
const oldGet = host.getSourceFile.bind(host);
host.getSourceFile = (name, ...args) => name === filename ? ts.createSourceFile(name, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.JS) : oldGet(name, ...args);
const program = ts.createProgram([filename], { allowJs: true, noLib: true }, host);
const sf = program.getSourceFile(filename);
const checker = program.getTypeChecker();
function group(line) {
 if(line < 101) return 'core/util';
 if(line < 165) return 'core/lambda';
 if(line < 201) return 'core/text';
 if(line < 315) return 'components/markdown';
 if(line < 384) return 'components/chunking';
 if(line < 498) return 'components/indexes';
 if(line < 508 || (line >= 926 && line < 934)) return 'components/registry';
 if(line < 924) return 'components/builtins';
 if(line < 1431) return 'graph/graph';
 if(line >= 1452 && line <= 1454) return 'engine/environment';
 if(line < 1478) return 'engine/records';
 if(line < 1523) return 'engine/scope';
 if(line < 1539) return 'engine/events';
 if(line < 1637) return 'engine/decision';
 if(line < 2069) return 'engine/engine';
 if(line < 2278) return 'engine/session';
 if(line >= 2295 && line < 2301) return 'api/target';
 if(line < 2439) return 'evaluation/evals';
 if(line < 2573) return 'api/workspace';
 if(line < 2602) return 'api/define';
 if(line < 2632) return 'export/html';
 if(line === 2634) return 'engine/environment';
 if(line < 2637) return 'api/default-engine';
 return 'api/index';
}
const modules = new Map(), ownership = new Map();
for(const st of sf.statements) {
 const line = sf.getLineAndCharacterOfPosition(st.getStart(sf)).line + 1;
 const key = group(line);
 if(!modules.has(key)) modules.set(key,{statements:[], imports:new Map(), exports:new Set()});
 modules.get(key).statements.push(st);
 const names = ts.isVariableStatement(st) ? st.declarationList.declarations.map(d=>d.name) : st.name ? [st.name] : [];
 for(const name of names) if(ts.isIdentifier(name)) ownership.set(checker.getSymbolAtLocation(name), {key,name:name.text});
}
for(const [key,m] of modules) {
 const walk = n => {
  if(ts.isIdentifier(n)) {
   const owner = ownership.get(ts.isShorthandPropertyAssignment(n.parent) ? checker.getShorthandAssignmentValueSymbol(n.parent) : checker.getSymbolAtLocation(n));
   if(owner && owner.key !== key) {
    if(!m.imports.has(owner.key)) m.imports.set(owner.key,new Set());
    m.imports.get(owner.key).add(owner.name);
    modules.get(owner.key).exports.add(owner.name);
   }
  }
  ts.forEachChild(n,walk);
 };
 m.statements.forEach(walk);
}
for(const [key,m] of modules) {
 const file = path.resolve('pkg/ragjs/src',key+'.js');
 mkdirSync(path.dirname(file),{recursive:true});
 const imports = [...m.imports].map(([dep,names])=> {
  let rel = path.relative(path.dirname(key),dep+'.js'); if(!rel.startsWith('.'))rel='./'+rel;
  return `import { ${[...names].sort().join(', ')} } from '${rel}';`;
 }).join('\n');
 const body = m.statements.map(st=>source.slice(st.getFullStart(),st.end).trimStart()).join('\n\n');
 writeFileSync(file, `${imports}\n\n${body}\n\n${m.exports.size ? 'export { '+[...m.exports].sort().join(', ')+' };' : ''}\n`);
 console.log(key, '->', [...m.imports.keys()].join(', '));
}
