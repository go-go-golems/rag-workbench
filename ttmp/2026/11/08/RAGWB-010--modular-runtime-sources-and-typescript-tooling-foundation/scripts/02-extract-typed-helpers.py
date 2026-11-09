from pathlib import Path
import re,os
root=Path('pkg/ragjs/src')
p=root/'core/util.js';s=p.read_text()
# Extract concrete, runtime-used types without changing their bodies.
a=s.index('function cyrb53(');b=s.index('\nfunction stable(',a);hashfn=s[a:b]
a2=s.index('const slugFull =');b2=s.index('\nconst tokens =',a2);ids=s[a2:b2]
a3=s.index('class RagError');b3=s.index('\nexport ',a3);err=s[a3:b3]
s=s.replace(hashfn,'').replace(ids,'').replace(err,'')
moved={'cyrb53':'core/identity.ts','slug':'core/identity.ts','docIdFor':'core/identity.ts','RagError':'core/errors.ts'}
s=s.replace('RE_WORD, RagError,','RE_WORD,').replace('clone, docIdFor,','clone,').replace('setPath, slug,','setPath,')
s="import { cyrb53 } from './identity.ts';\n"+s;p.write_text(s)
(root/'core/identity.ts').write_text(hashfn.replace('function cyrb53(str, seed = 0)', 'function cyrb53(str: string, seed = 0)')+'\n'+ids.replace('slugFull = s =>','slugFull = (s: unknown) =>').replace('slug = s =>','slug = (s: unknown) =>').replace('docIdFor = name =>','docIdFor = (name: string) =>')+'\nexport { cyrb53, slug, docIdFor };\n')
(root/'core/errors.ts').write_text(err.replace('constructor(code, message, detail)', 'declare code: string;\n  declare detail?: unknown;\n  constructor(code: string, message: string, detail?: unknown)')+'\nexport { RagError };\n')
# Rewrite consumers to import directly from the owning file, without alias exports.
for p in root.rglob('*.js'):
 s=p.read_text();new=[]
 for l in s.splitlines():
  m=re.match(r"import \{ (.*?) \} from '(.*util.js)';",l)
  if not m:new.append(l);continue
  names=m[1].split(', ');keep=[]
  groups={}
  for n in names:
   if n in moved:groups.setdefault(moved[n],[]).append(n)
   else:keep.append(n)
  if keep:new.append('import { '+', '.join(keep)+" } from '"+m[2]+"';")
  for target,ns in groups.items():
   rel=os.path.relpath(root/target,p.parent);rel=rel if rel.startswith('.') else './'+rel
   new.append('import { '+', '.join(ns)+" } from '"+rel+"';")
 p.write_text('\n'.join(new)+'\n')
p=root/'core/text.js';s=p.read_text().replace('function stem(w)', 'function stem(w: string): string').replace('function tokenize(text, opts = {})', 'function tokenize(text: unknown, opts: { stem?: boolean } = {}): string[]');p.with_suffix('.ts').write_text(s);p.unlink()
for p in root.rglob('*.js'):p.write_text(p.read_text().replace('/text.js', '/text.ts'))
# Move persistence helpers out of provider decision plumbing.
p=root/'engine/decision.js';s=p.read_text();a=s.index('const persistOf');b=s.index('\nexport ',a);helpers=s[a:b];s=s.replace(helpers,'').replace('persistOf, storeKey, ','').replace('hash, ','');s='\n'.join(l for l in s.splitlines() if 'import { JSON_PERSIST }' not in l);p.write_text(s+'\n')
p=root/'engine/records.js';s=p.read_text().replace('import { now }','import { hash, now }');s=s.replace('export { ',helpers+'\n\nexport { persistOf, storeKey, ');p.write_text(s)
p=root/'engine/engine.js';s=p.read_text().replace('persistOf, storeKey, ','').replace('import { FULL_DELTA,','import { persistOf, storeKey, FULL_DELTA,');p.write_text(s)
