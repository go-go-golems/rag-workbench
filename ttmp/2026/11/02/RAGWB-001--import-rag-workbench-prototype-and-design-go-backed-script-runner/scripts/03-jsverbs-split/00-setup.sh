#!/usr/bin/env bash
# 03-jsverbs-split setup: copy the prototype engine (with the goja \p{L} compat rewrite) and corpus into app/lib.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
proto="$here/../../sources/prototype"
sed 's/\\p{L}\\p{N}/A-Za-z0-9\\u00C0-\\u024F\\u0370-\\u03FF\\u0400-\\u04FF\\u3040-\\u30FF\\u4E00-\\u9FFF/g' "$proto/rag.js" > "$here/app/lib/rag.js"
{ printf "module.exports = "; cat "$proto/corpus.json"; printf ";\n"; } > "$here/app/lib/corpus-data.js"  # jsverbs loader resolves scanned .js only, not .json
