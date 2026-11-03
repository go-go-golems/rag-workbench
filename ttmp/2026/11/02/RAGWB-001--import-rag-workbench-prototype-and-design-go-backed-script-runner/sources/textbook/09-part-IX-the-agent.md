# Part IX · The agent

The agent is Claude with one tool that runs JavaScript in the page, so it works through the same `rag`, `gui` and workspace objects as a programmer and is held to the same checks. This part explains that design and the controls around it.

## 28. One tool: eval

This chapter explains how the agent acts on the workbench. The agent is Claude, called through the page's sampler with a list of tools. Claude reads the prompt and replies either with text or with a request to call one of those tools; the runtime runs the tool, hands the result back to Claude, and repeats until Claude replies with text alone. The workbench gives Claude exactly one tool, `eval`, which runs a piece of JavaScript in the page. Everything the agent does, from running the golden questions to drawing a chart in the conversation, is code passed to that one tool. By the end of the chapter you should be able to read any agent step, predict what it returns, and explain why the tool's output and error messages are shaped the way they are.

A note on the evidence. Every tool result, error message and prompt excerpt below was produced by the real page in a headless browser. The code inside each step, however, was written by the author and fed to the page through a scripted stand-in for Claude, so the steps show what the workbench does with code, not what Claude would choose to write.

&#91;embedded content: one agent turn · prompt, Claude, and the per-call path of the eval tool\]

The picture shows one turn. The upper row runs once: your message is wrapped in a prompt and sent to Claude. The lower row runs every time Claude calls `eval`, and the turn ends when Claude stops calling it and answers in text. The rest of this chapter follows that path from left to right: why there is one tool (28.1), what the code can reach (28.2), what comes back (28.3 and 28.4), and what Claude was told before it wrote anything (28.5).

### 28.1 Why one eval tool rather than many narrow tools

The usual way to give a model abilities is one tool per action: `set_param`, `run_evals`, `search`, `show_table`, each with its own JSON schema. The workbench could have done that, and it pays to see why it did not.

Count what the agent can reach. The workbench API `wb` has 28 entry points, the workspace `ws` has 17, the page driver `ui` has 8, the conversation outputs `chat` have 11, and the link builders `ref` have 4. Behind them sit the whole `rag` and `gui` libraries from Parts III to VIII. One tool per action would mean some 70 schemas, each of which has to be written, described to the model and kept in step with the code, and none of which could express a loop, a comparison or a condition. A question such as "which of these five weight changes fixes the miss without losing another case?" would take five round trips per candidate through narrow tools; as code it is one step.

The `eval` tool, by contrast, is defined once and never changes. This is its definition as the page sends it to the sampler:

```json
{
  "name": "eval",
  "description": "Run JavaScript in the workbench page as the body of an async function. Scope: rag, gui, wb, ws, ui, chat, ref, log, console. Use await; end with `return <small value>`. Returns \"ok\" plus a YAML summary of the returned value (strings verbatim) and captured logs, truncated near 9,000 characters; or \"error\" with the message and line.",
  "inputSchema": { "type": "object",
    "properties": { "code": { "type": "string", "description": "JavaScript statements; use await; end with return <small value>" } },
    "required": ["code"] }
}
```

The abilities live in the API, not in the tool list, so every component, op and gui element added to the library is available to the agent the moment it exists. The same code a programmer types into the pipeline script works in an agent step, and an agent step can be pasted into the script. That is the design goal of the whole book carried one layer up: the pipeline is data that code can read and change, and the agent is one more writer of that code.

|  | One tool per action | One `eval` tool |
| --- | --- | --- |
| Tool definitions | One schema per action, \~70 here | One, fixed |
| New library feature | A new schema and description | Available at once |
| Loops, comparisons, conditions | A round trip per call | Ordinary JavaScript in one step |
| What a mistake looks like | A schema validation failure | A JavaScript or library error with a line number |
| Where limits are enforced | In the tool list | In the objects the code receives (chapter 29) |

The last row is the price. A narrow tool can only do what its schema allows; code can do anything the page can. Chapter 29 is about how the workbench limits that power, and where those limits stop.

### 28.2 The scope: `rag`, `gui`, `wb`, `ws`, `ui`, `chat`, `ref`, `log`, `console`

When Claude calls `eval`, the agent panel wraps the code in an async function and calls it with nine named arguments:

```js
const names = ['rag', 'gui', 'wb', 'ws', 'ui', 'chat', 'ref', 'log', 'console'];
const fn = new Function(...names,
  `"use strict";return (async () => {\n${code}\n})();\n//# sourceURL=agent-eval.js`);
const res = await fn(scope.rag, scope.gui, scope.wb, scope.ws, scope.ui, chat, scope.ref, log, cons);
```

Three consequences follow from these lines. The code may use `await` anywhere, because it is the body of an async function. It runs in strict mode, so assigning to an undeclared name is an error rather than a new global. And the `sourceURL` comment names the code `agent-eval.js` in stack traces, which is how errors get line numbers (28.4).

The nine names are the whole intended interface:

| Name | What it is | Typical use |
| --- | --- | --- |
| `rag` | The pipeline library of Parts III–VII | `rag.evals(…)`, `rag.explain(result, id)` |
| `gui` | The page grammar of Part VIII | `gui.app('id').use(ws).view(…)` |
| `wb` | The workbench: script, files, presets, last result, last eval | `await wb.runEvals()`, `wb.chunks(f)`, `wb.presets.load(id)` |
| `ws` | The live workspace of chapter 22 | `ws.apply(ops)`, `ws.branch(name)`, `ws.ir` |
| `ui` | Drives the visible page | `ui.query(q)`, `ui.tab('evals')`, `ui.chunk(id)` |
| `chat` | Puts outputs into the conversation | `chat.table(rows)`, `chat.ui(app)` (chapter 30) |
| `ref` | Builds `wb:` links for the answer's prose | `ref.chunk(id)` |
| `log`, `console` | Capture text into the step's logs | `log('k is', ws.ir.node('fuse').p.k)` |

The objects are the page's own, not copies. `ws` is the same workspace the DAG tab edits, so an op the agent applies appears in the History tab and re-runs the search box exactly as a person's edit would. The one exception is Read-only mode, in which `ws` and `wb` are replaced by guarded versions (29.3).

A real step that uses three of the names: it runs the golden questions through `wb`, puts the report in the conversation with `chat`, and returns the summary.

```js
const r = await wb.runEvals();
chat.evals(wb.lastEval);
return r.summary;
```

The function body runs in the page's global scope, which matters later: the nine names are what the code is given, not all it can reach.

### 28.3 Results as short YAML, truncated near 9,000 characters

Whatever the code returns is converted to text before Claude sees it. A string is passed through verbatim. Anything else goes through two functions in `agent.js`: `plain`, which turns an arbitrary JavaScript value into plain data, and `yaml`, which prints that data. The step above returned the eval summary, and this is the complete tool result Claude received:

```text
ok
n: 12
hit@5: 0.917
mrr: 0.926
ndcg@10: 0.79
p50ms: 1.3
calls: 0
```

YAML was chosen over JSON for the reader, and here there are two readers: Claude, for whom every brace and quote is a token, and the person, who sees the same text in the step's expanded view. The receipt of an applied op shows the difference in shape:

```text
ok
before: 0
rec:
  revision: 1
  live: [fuse]
  dirty: []
  changed: []
  removed: []
```

The `plain` pass is where the limits are enforced. Its rules are worth knowing, because they decide what a step should return:

| Value | Becomes |
| --- | --- |
| String, number, boolean, null | Itself |
| Object with `toJSON()` | Its `toJSON()` result, so an IR or report prints its summary form |
| Function | `ƒ name` |
| DOM node | `<tagname>` |
| Error | `{ error: "Name: message" }` |
| Map, Set | An object or list of the first 40 entries |
| Array | The first 40 items, then `… N more` |
| Object | The first 40 keys not starting with `_`, then `…: N more keys` |
| Anything nested deeper than 6 levels | `"{…}"` or `"[N items]"` |

The depth limit, from a real step that returned a deliberately deep object:

```text
ok
n: 490
deep:
  a:
    b:
      c:
        d:
          e: "{…}"
```

After printing comes a length limit. A result longer than 9,000 characters is cut there and ends with a line saying how much was dropped. A real step that returned the id and text of every chunk produced a 9,040-character result ending:

```text
      The *signature* of an order is the sorted concatenation of its present dimensions. Signatures make bask
# … truncated: 37750 more characters
```

Captured `log` and `console` output is appended under a `logs:` line, capped at 6,000 characters, and the whole tool result is capped at 28,000. These limits exist because every tool result stays in the conversation Claude reads on every later call of the turn. A step that returns the corpus wastes the budget of every step after it. The prompt says so in its first rule: "Return specific small values (a few rows, ids, numbers), never whole objects." Large evidence belongs on the screen through `chat` (chapter 30), where the person can read it and Claude only sees that it was shown.

### 28.4 Errors with line numbers, and how the agent recovers from them

A step that throws produces a tool result that starts with `error` instead of `ok`, followed by the error's class and message and, when the stack trace allows it, the line of the agent's code where it was thrown. This step reads a weight that does not exist:

```js
const ir = ws.ir;
const fuse = ir.node('fuse');
return fuse.p.weights.missing.toFixed(2);
```

```text
error
error: "TypeError: Cannot read properties of undefined (reading 'toFixed')"
line: 3
```

The line number comes from the stack trace. Because the function was created with `//# sourceURL=agent-eval.js`, the browser reports frames in it as `agent-eval.js:N`. That `N` counts lines of the generated function, which has a header the agent never wrote, so the page subtracts an offset. The offset is not hard-coded: at boot the page builds a probe function the same way, throws on a known line and measures the difference. In Chromium it is 3. The same measurement serves the script editor, which marks the failing line of a pipeline script in the gutter.

A JavaScript error tells Claude what went wrong in its own code. A library error should also tell it what to do instead, and the library's errors are written for that reader. A step that sets a param on a node that does not exist gets this:

```text
error
error: "RagOpError: op 1 (set): no node 'nope'. Nodes: files, parse, rewrite, chunks, failures, synonyms, ix-text, ix-meta, ix-failures, text, meta, failures_2, fuse, filter, rerank, neighbors, pack, answer"
line: 1
```

The message names the failing op by position and kind, states the reason, and lists the valid ids, so the next step can be written from the error alone. The line is 1 because the error was thrown inside `rag.js`, and the reported line is the agent's line that called into it. Errors met elsewhere in this book follow the same pattern:

| Error | Raised by | What the message carries |
| --- | --- | --- |
| `RagOpError` | `ws.apply`, `applyOps` | The op's index and kind, the reason, the valid choices |
| `RagCompileError` | Compiling a plan or op result | Every problem found, with node ids |
| `RagConflictError` | `ws.apply(ops, { expect })`, `ws.merge` | The current and expected revisions, and what to do |
| `RagReadonlyError` | Guarded `ws` and `wb` in Read-only mode | The blocked call and the allowed alternative |
| Any JavaScript error | The agent's own code | The message and the agent's line |

Recovery needs no special machinery. The error is an ordinary tool result, so Claude reads it and writes the next step. The person sees the failed step in the panel, marked `error` and expanded by default, with its code and the error beneath it. The step's snapshot still exists, so even a step that failed halfway through a change can be reverted (chapter 29).

### 28.5 The system prompt: an API reference plus the current state of the page

Before Claude writes any code it has read a long instruction text, built fresh for every message. Strictly, it is not a system prompt: the agent panel sends it as the first user turn of the request, followed by the recent conversation and then the new message. In the session used for this chapter it was 11,994 characters long, in four parts.

The first part is one sentence of role: "You are the agent inside a RAG workbench: a page where a person builds and tunes search over a corpus of markdown reports. You act only through the eval tool, which runs JavaScript in the page."

The second part is the API reference, a hand-written markdown text kept in `agent.js` as `DOCS`. It covers the lambda phases, the corpus and plan builders, compile, run and explain, workspace ops, evals and sweeps, `gui`, `kit`, and the agent-only objects `wb`, `ui`, `chat` and `ref`. The same text is shown to the person in the editor's Reference tab, followed by a component table generated from the registry. The agent and the person therefore read the same reference, and an error in it misleads both.

The third part is a list of working rules. Four of them, verbatim:

```text
- Prefer one eval that looks, acts, shows and returns a small answer. Return specific small values (a few rows, ids, numbers), never whole objects. Read errors: they name the right call.
- Change the running pipeline with ws.apply(ops) (it is undoable and shows in History). Edit the script (wb.script.set + build) only when the person asks for a code change.
- Try risky changes on a branch (ws.branch), measure with an eval set, show the comparison, then merge if it helps. Say what changed and the measured effect.
- Cost: nodes marked submit (rewrite.llm, rerank.llm, answer, agentic, enrich.llm, judges) spend the person's Claude usage. Run evals with trigger 'change' (lexical, free) unless asked. Ask before building enrich.llm over the whole corpus (one call per chunk).
```

The last rule states the current run mode, in words that change with the mode (29.1 to 29.3).

The fourth part, `## Current state`, is the page's state printed as YAML at the moment the message was sent. An excerpt:

```text
corpus:
  files: 11
  docs: 11
  chunks: 490
  indexes: [text, meta, failures]
plan: ask
revision: 0
nodes: files:source.files → q:signal → … → fuse:fuse.rrf → filter:filter.where → rerank:rerank.llm! → neighbors:expand.neighbors → pack:pack → answer:answer!
signals:
  q: Why did ChatGPT show no app actions after OAuth worked?
  k: 8
evals:
  id: goldens
  cases: 12
  last: not run yet
script:
  lines: 84
  built: true
  preset: reports
pendingClaudeBuild: []
claude: available
```

The `!` after a node type marks a node that calls Claude. With this section the agent's first step can act rather than look around: it already knows the plan's node ids, the revision, whether the evals have run and whether a Claude build is pending.

After the instructions come up to 16 earlier messages. Earlier agent answers are not replayed as tool calls; each is flattened into text, with every step rendered as its code (clipped to 1,500 characters), its status and its result (clipped to 900), and every output as a one-line marker. This is how the first answer of the session looked to Claude on the next message:

```text
[eval]
const before = ws.revision;
const rec = await ws.apply([{ op: 'set', node: 'fuse', path: 'k', value: 20 }]);
return { before, rec };
→ ok:
before: 0
rec:
  revision: 1
  live: [fuse]
  …
[the person reverted this step and everything after it]
[shown: hits "Results for “Why did ChatGPT show no actions after OAuth worked?”"]
```

The bracketed note on the reverted step matters: without it, Claude would believe its change was still in place. If the whole request exceeds 230,000 characters, the oldest messages are dropped until it fits, which keeps it under the sampler's 256 KiB prompt limit described in chapter 31.

One weakness of this design deserves a plain statement. `DOCS` is prose, and nothing checks it against the code. It documents `ws.apply(ops, { expect: ws.revision })` as the way to guard an edit, and as chapter 29 shows, that call can never conflict. A reference the model trusts needs the same review as the code it describes.

The key points to take from this chapter:

- The agent has one tool, `eval`, which runs JavaScript in the page; its abilities are the page's API, not a list of tool schemas.
- Agent code runs as the body of a strict async function with nine named arguments, and those objects are the page's own, so the agent's edits are indistinguishable from a person's in effect.
- Results come back as YAML, reduced by fixed rules to 40 items, 40 keys and 6 levels, and cut near 9,000 characters, because every result stays in Claude's context for the rest of the turn.
- Errors come back as ordinary tool results with the agent's line number, and the library's messages name the valid alternatives so the next step can be written from the error alone.
- Each message is preceded by a fresh prompt containing the API reference, working rules, the run mode and the current state of the page, followed by up to 16 earlier messages with their steps flattened into text.

One tool that can do anything the page can do needs controls around it. Chapter 29 describes the three run modes, the snapshots that make every step reversible, and the limits of both.

**Lab: the suggestion chips in the agent panel.** Open the agent panel on a new conversation and pick "Which golden questions does the current plan miss, and why? Show the ranks." Expand each step as it finishes and compare its code with its YAML result. Then open the editor's Reference tab and find, in the text the agent was given, the calls its steps used. Finally, ask the agent to read a node that does not exist, and read the error it gets back and the step it writes next. This step spends Claude usage.

## 29. Run modes and reversibility

Chapter 28 gave the agent one tool that can do anything the page can do. This chapter explains the controls around that tool. There are two kinds. A **run mode**, chosen by the person in the agent panel, decides what happens before and during each step: whether it runs at once, waits for approval, or runs with the mutating calls taken away. A **snapshot**, taken before each step, decides what can happen afterwards: any step, and everything after it, can be undone. By the end of the chapter you should know what each mode guarantees, what it does not, and how an agent's edit and a person's edit interact when both change the same pipeline.

As in chapter 28, the step code below was written by the author and fed through a scripted stand-in for Claude; the panel behaviour, results and errors are the real page's.

The three modes differ in four ways:

|  | Auto | Ask | Read-only |
| --- | --- | --- | --- |
| Step waits for the person | No | Yes, every step | No |
| Snapshot before the step | Yes | Yes, after approval | No |
| `ws.apply`, `wb.script.set`, … | Work | Work | Throw `RagReadonlyError` |
| Revert afterwards | Yes | Yes, for steps that ran | Nothing to revert |

The mode is also stated to Claude, in the last line of the working rules (28.5), so it can plan for it. The three lines, verbatim:

```text
- Run mode is Auto. Every step is snapshotted and can be reverted by the person.
- Run mode is Ask. Each eval waits for the person to approve it, so batch work into fewer, clearer steps.
- Run mode is Read-only. Mutating calls (ws.apply, merge, rebuild, wb.script.set, wb.addFile) throw; explain what you would change instead. Branches you create are fine to edit and measure.
```

The mode is read when an answer starts and recorded on the answer, so switching modes mid-answer affects the next message, not the steps already planned.

### 29.1 Auto: run, snapshot, report

In Auto mode each step runs as soon as Claude asks for it. The order of events inside `runEval` is the whole contract:

```text
// pseudocode of Agent.runEval(code) in Auto mode
add a step part to the answer, status 'run'; repaint
snapshot = host.snapshot()            // chapter 23.3: workspace, script, files, app, preset
scope = host.scope({ readonly: false, chat, log })
result = run code (28.2); format it (28.3, 28.4)
step.status = ok | err; step.result = result
host.afterStep()                      // re-render inspector and top bar
return result to Claude
```

The snapshot is taken before the code runs, so it records the state the step started from. The page re-renders after every step, so the DAG, the History tab and the search results show the step's effect before Claude has decided what to do next. A person watching an agent work sees each change land.

The panel shows each step as a collapsed line with its status, the first line of its code and its time. The five steps of the session used in chapter 28 appeared like this:

```text
eval  ok     const r = await wb.runEvals();                                   58 ms
eval  error  const ir = ws.ir;                                                 2 ms
eval  error  return ws.apply([{ op: 'set', node: 'nope', path: 'k', value: 1…   3 ms
eval  ok     const r = await ui.query('Why did ChatGPT show no actions after…  25 ms
eval  ok     const before = ws.revision;                                       3 ms
```

Expanding a step shows its full code, its result and its logs, and a **Revert to before this step** link. Under the whole answer, a footer reads `5 steps · 0.2 s` and offers **Undo this answer**, which reverts to before the answer's first step that still has a snapshot.

An edit made by the agent is an ordinary workspace edit. The last step applied one op and got this receipt:

```text
ok
before: 0
rec:
  revision: 1
  live: [fuse]
  dirty: []
  changed: []
  removed: []
```

The receipt is the one chapter 22 describes: revision 1, one live node changed, nothing to rebuild. There is one gap here. The workspace records an author with every history entry, taken from `apply(ops, { by })` and defaulting to `user`. The agent's `ws` is the page's workspace and the agent's code does not pass `by`, so its edits are recorded as `user`. In a session where the agent set `fuse.k` to 12 and 25 and the person set it to 40 in between, the History tab read:

```text
3  user  set fuse.k=12     ← the agent
4  user  set fuse.k=40     ← the person
5  user  set fuse.k=25     ← the agent
```

The conversation shows which steps were the agent's; the History tab cannot. Passing `{ by: 'agent' }` fixes it for one call, as the worked session in chapter 30 does, and the agent panel could do it for every call by wrapping `ws.apply`.

### 29.2 Ask: approve one step, or the rest of the turn

In Ask mode the step is shown before it runs. `runEval` sets the step's status to `ask` and waits on a promise that only the person can resolve:

```text
Run this step?                                         Ask mode
return ws.apply([{ op: 'set', node: 'fuse', path: 'k', value: 12 }]);
[Run]  [Run all in this answer]  [Decline]
```

The three buttons resolve the promise three ways. **Run** runs this step and leaves the next one to be asked about. **Run all in this answer** runs this step and marks the answer as approved, so the remaining steps of the same answer run without asking; the next message asks again. **Decline** does not run the code and does not take a snapshot; instead the tool returns this text to Claude:

```text
The person declined this step. Ask what they would prefer, or explain without running it.
```

In a real run of three scripted steps, the person pressed Run, Decline, then Run all. The three tool results were the receipt of the first edit, the declined message, and `ok 12` from the third step, which read `fuse.k` and found the first edit in place and the second never made. The step statuses were `ok`, `declined`, `ok`.

Two details make Ask mode safe to leave unattended. Pressing **Stop** while a step waits declines it, because the approval promise listens to the turn's abort signal. And the snapshot is taken after approval, so a declined step leaves nothing to revert.

Ask mode asks about every step, including steps that only read. This is why its line in the prompt tells Claude to "batch work into fewer, clearer steps": an agent that looks around in five small steps costs the person five clicks.

### 29.3 Read-only: mutating calls throw, and branches stay open for experiments

Read-only mode does not stop code from running. It changes the objects the code receives. When the mode is Read-only, the scope is built with guarded versions of `ws` and `wb`:

| Object | Blocked members |
| --- | --- |
| `ws` | `apply`, `rebuild`, `undo`, `redo`, `merge`, `reset`, `restore` |
| `wb` | `addFile`, `removeFile`, `script.set`, `script.build`, `presets.load`, `build` |

The guarded `ws` is a proxy over the real workspace: reading `ws.ir`, `ws.revision` or `ws.history` works, and calling a blocked method throws. The messages are written for the agent, and say what to do instead:

```text
error
error: "RagReadonlyError: ws.apply is blocked in Read-only mode. Describe the change instead, or measure it on a branch (ws.branch)."
line: 1
```

```text
error
error: "RagReadonlyError: wb.script.set is blocked in Read-only mode. Describe the change instead, or ask the person to switch to Auto or Ask."
line: 1
```

`ws.branch` is not blocked, and the branch it returns is an ordinary workspace (chapter 23.2), not a guarded one. An agent in Read-only mode can therefore try a change and measure it without touching the live pipeline. A real step:

```js
const b = ws.branch('try-k30');
await b.apply([{ op: 'set', node: 'fuse', path: 'k', value: 30 }]);
return { branch: b.revision, main: ws.revision, mainK: ws.ir.node('fuse').p.k };
```

```text
ok
branch: 3
main: 2
mainK: 60
```

The branch moved to revision 3; the main workspace stayed at revision 2 with `k` at 60. This is the most useful way to run an agent on a pipeline you care about: it can explore and report numbers, and the decision to merge stays with you.

Read-only mode takes no snapshots, because nothing it can change through `ws` and `wb` needs undoing. That assumption has edges, and they should be stated plainly:

- `ui` is not guarded. `ui.query` changes the search box and runs a query, `ui.filters` changes the filters, and `ui.app` replaces the App tab. These change what the person sees, not the pipeline.
- `rag` is not guarded. `rag.configure` changes library-wide settings.
- The code runs in the page's global scope. A Read-only step that returned `typeof window.__wb` got `object`: the workbench's internal state, including the unguarded workspace, is reachable.

Read-only is therefore a guard against mistakes by a cooperating agent, not a sandbox against code written to get around it. That is the right strength for this page, where the agent and the person act for the same user on the user's own data. A page that ran untrusted code would need an isolated realm, such as a worker or an iframe that receives only the guarded objects.

### 29.4 Reverting to before any step

Chapter 23.3 described what a snapshot holds: the workspace's IR, revision and history length, the script and its last built text, the uploaded and disabled files, the App tab and the loaded preset. This section is about how the agent panel uses snapshots.

Each step that ran in Auto or Ask mode keeps its snapshot, and **Revert to before this step** restores it. In the real session, the fifth step had set `fuse.k` from 60 to 20, taking the workspace from revision 0 to 1. Reverting it gave:

```text
before revert   k 20   revision 1   history 1
after revert    k 60   revision 2   history 0
```

The value returned to 60 and the history entry was removed, while the revision went up rather than back, for the reason chapter 23.1 gives: a revision number names a state that existed, and a caller holding revision 1 must not mistake the restored graph for it. The reverted step and every later step of the answer are marked as reverted, and the panel says so under each.

Claude is told. On the next message, the flattened history (28.5) ends that step with:

```text
[the person reverted this step and everything after it]
```

Without this line, the agent would describe a pipeline that no longer exists.

A snapshot restores the whole page to a point in time, so reverting has three consequences worth knowing:

- Everything after the snapshot is discarded, including edits the person made by hand after the agent's step. Revert is for undoing an agent step soon after it ran; for a single param edit, the workspace's own Undo is the finer tool.
- Snapshots live in memory, keyed by the step objects of the current page. Conversations are saved (chapter 30.4) but snapshots are not, so a conversation reopened after a reload shows its steps without revert links.
- Read-only steps and declined steps have no snapshot and no revert link.

### 29.5 Editing the live pipeline with ops versus editing the script

The agent can change the pipeline in two ways, and the prompt tells it which to prefer: "Change the running pipeline with ws.apply(ops) (it is undoable and shows in History). Edit the script (wb.script.set + build) only when the person asks for a code change."

|  | `ws.apply(ops)` | `wb.script.set(text)` + `wb.script.build()` |
| --- | --- | --- |
| Granularity | One op list | The whole script |
| Rebuild | Only changed build nodes (chapter 21.4) | Every build node whose digest changed |
| History | One entry per op list, with receipt | One `reset` entry |
| Cost known in advance | Yes, `ws.dryRun(ops)` | No |
| Survives a reload | Only if kept in the script | Yes, the script is saved |
| Can express | Params, wiring, signals, triggers, outputs, node swaps | Anything, including new components and apps |

Ops are the safer instrument for an agent: each is small, checked, priced in advance and visible in History. The script is the durable form, and **Keep in script** (chapter 23.4) carries ops into it when the person decides they are worth keeping.

When the agent and the person edit the same workspace, revisions are how an edit avoids overwriting a change it never saw. Chapter 22.2 introduced `apply(ops, { expect })`. For the agent the rule is concrete: read the revision in one step, keep the number, and pass it to the step that edits. A real run, in which the person changed `fuse.k` to 40 between the agent's two steps:

```js
// step 1 returned 3; the person then edited fuse.k in the DAG tab
return await ws.apply([{ op: 'set', node: 'fuse', path: 'k', value: 25 }], { expect: 3 });
```

```text
error
error: "RagConflictError: workspace 'main' is at revision 4; these ops were planned against 3. Re-read ws.ir and plan again."
line: 1
```

The conflict is the correct outcome: the agent planned against a graph that no longer exists. The API reference, however, shows the guard written as `{ expect: ws.revision }`, and that form can never conflict, because it reads the revision at the moment of the call. In the same run it succeeded, set `k` to 25 and silently replaced the person's 40:

```js
return await ws.apply([{ op: 'set', node: 'fuse', path: 'k', value: 25 }], { expect: ws.revision });
```

```text
ok
revision: 5
live: [fuse]
…
```

The reference should show the two-step form, and until it does, a careful agent has to know better than its documentation. A second known issue from chapter 21 also bears on agent edits: a lambda written as a JavaScript function inside an `insert` or `replace` op reaches the IR but is dropped from the recorded history, so a merge or Keep in script that replays the ops loses it. Agent code should write such lambdas as source text, `fn: '(s, ctx, hits) => …'`.

The key points to take from this chapter:

- Auto runs each step at once after taking a snapshot of the whole page; Ask shows each step and waits for Run, Run all or Decline; Read-only runs steps with the mutating calls of `ws` and `wb` taken away.
- The run mode is stated to Claude in the prompt, so the agent can plan for it: fewer steps in Ask, branches in Read-only.
- Read-only is a guard for a cooperating agent, not a sandbox: `ui`, `rag` and the page's globals are still reachable.
- Reverting a step restores the snapshot taken before it, discards everything after it including the person's later edits, increments the revision, and tells Claude on the next message.
- Agent edits are recorded in History as `user` unless the code passes `{ by }`, so the History tab cannot tell them apart from the person's.
- A revision guard only protects an edit if the revision was read in an earlier step; `{ expect: ws.revision }` never conflicts.

The controls in this chapter decide what the agent may change. Chapter 30 turns to what it shows: the tables, charts, apps and links it puts into the conversation.

**Lab: the mode switch in the agent panel.** Load Reports · hybrid + rerank + answer and set the mode to Ask. Ask the agent to raise the heading weight in `ix-text` and run the goldens; approve the first step, decline the second, and read what the agent writes next. Switch to Read-only and ask for the same change: watch it create a branch, apply the op there, and report a comparison while the History tab stays unchanged. Finally, in Auto mode, ask for any edit, then use **Revert to before this step** and check the revision number in the top bar: it goes up, not back. These steps spend Claude usage.

## 30. Answers the agent can draw

An agent that can only reply in prose has to describe evidence: "the expected section ranked ninth". An agent that can draw puts the ranking on screen and lets the person read it. This chapter explains how the agent's code places tables, charts, hit lists, eval reports, DAGs and complete gui apps into the conversation, how its prose links into the workbench, how it drives the page directly, and how a conversation is stored. It ends with a complete worked session. By the end you should be able to predict what the person sees, and what Claude sees, for any output an agent step produces.

The step code is again the author's, run through a scripted stand-in for Claude; the outputs, rendering, storage and numbers are the real page's.

### 30.1 `chat` outputs: md, table, chart, hits, evals, dag, ui, download

An agent answer is not a single string. It is a record with the streamed text and a list of **parts**, and each part is either a tool step (chapter 28) or an output. Every part stores `at`, the length of the answer's text at the moment it was created, and the panel renders the answer by interleaving text and parts in that order. An output appears where Claude was in its prose when the step that made it ran.

The `chat` object creates output parts. Each method adds a part and returns a short string to the agent's code:

```js
// pseudocode of makeChat(msg)
push(kind, data) = msg.parts.push({ type: 'out', kind, data, at: msg.text.length }); repaint soon
                   return 'shown: ' + kind
```

The eleven kinds, and what each stores:

| Method | Stores | Rendered as |
| --- | --- | --- |
| `chat.md(text)` | The text | Markdown |
| `chat.note(text, tone)` | Text and tone (`info`, `ok`, `warn`, `bad`) | A coloured note |
| `chat.code(text, lang)` | The text | A code block |
| `chat.json(value, { title })` | The value through `plain` | Pretty-printed JSON (not in the API reference) |
| `chat.table(rows, { title, columns })` | A compiled gui app with one `table` | A live gui runtime |
| `chat.chart(kind, data, opts)` | A compiled gui app with one `chart` | A live gui runtime |
| `chat.hits(resultOrHits, { title, k })` | A gui `hits` list of the first `k` (default 8) | A live gui runtime |
| `chat.evals(report)` | A gui app with summary stats and the case table | A live gui runtime |
| `chat.ui(app)` | The compiled app | A live gui runtime |
| `chat.dag({ highlight })` | A copy of the current IR and the ids to highlight | The DAG view, compact |
| `chat.download(name, data)` | The data as a string | A file line with a Save button |

The central design choice is visible in the middle rows: tables, charts, hit lists and eval reports are not special widgets of the agent panel. They are gui apps, compiled with the grammar of chapter 24 and mounted with the same runtime as the search page. `chat.table(rows)` is shorthand for `gui.app('table').view(v => v.table(rows)).compile()`. The panel needs one renderer for all of them, and anything the agent can show in the conversation it could also mount in the App tab.

A real step that uses most of the kinds:

```js
chat.table(wb.lastEval.table().slice(0, 4), { title: 'First four goldens' });
chat.chart('bar', wb.lastEval.cases.slice(0, 6).map(c => [String(c.i), c.rank || 0]), { title: 'Rank per case' });
chat.dag({ highlight: ['fuse'] });
const app = gui.app('k').title('k slider').use(ws).state({ k: 8 })
  .view(v => v.slider('k', { min: 1, max: 20, step: 1, label: 'k' }).results());
chat.ui(app);
chat.note('Ranks above 5 count as misses for hit@5.', 'warn');
chat.download('summary.json', wb.lastEval.summary);
```

The answer's parts after this step, as the panel recorded them, and the elements it rendered:

```text
tool
table · First four goldens      → out out-table
chart · Rank per case           → out out-chart
dag                             → out out-dag
ui · k slider                   → out out-ui
note                            → out out-note g-note warn
download                        → out out-download
```

The app in the fourth output is live, not a picture. It declared `.use(ws)`, which binds it to the workspace's plan (chapter 25.3), so moving its slider sets the signal `k`, the session re-runs the nodes that read `k`, and its `results` list redraws. The person can tune the pipeline from inside the conversation. Because the panel repaints the whole answer as text streams in, each mounted runtime is cached under a key made of the message, the part and the app's digest; a repaint reattaches the same runtime, so a slider keeps its position while Claude keeps writing.

What Claude sees of all this is deliberately little. The code receives `shown: table`, and on the next message the flattened history (28.5) carries one line per output:

```text
[shown: hits "Results for “Why did ChatGPT show no actions after OAuth worked?”"]
```

The rows, the chart's values and the app's state are on the person's screen, not in Claude's context. This is the division of labour chapter 28.3 argued for: a step returns a few numbers to Claude and shows the full evidence to the person. If Claude needs a value to reason about, the step must also `return` it.

### 30.2 `ref` links: pointing at a query, a chunk, a node or a tab

Prose can point at things too. The `ref` object builds links in a `wb:` scheme, which the panel's markdown renderer turns into in-page actions instead of navigation. A real step returned four of them:

```text
ok
- wb:chunk?id=project-report-coinvault-mcp-from-local-conformance-to-chatgpt-2%2311-why-chatgpt-still-showed-no-actions
- wb:node?id=fuse
- wb:query?q=rebalance%20law
- wb:tab?name=evals
```

Claude writes them into its text as ordinary markdown links, as the prompt asks: "In your text, link what you talk about with ref: the section, node, query." The markdown renderer recognises the scheme and emits `<a href="#" class="ref" data-ref="wb:…">` instead of a real link; the panel catches clicks on `a.ref` and hands the address to one handler:

| Link | Action on click |
| --- | --- |
| `wb:query?q=…` | Puts the query in the search box and runs it on `change`; with `&submit=1`, runs it on `submit`, which spends Claude usage |
| `wb:chunk?id=…` | Opens the section in the search page's section viewer |
| `wb:node?id=…` | Selects the node in the DAG tab |
| `wb:tab?name=…` | Opens an inspector tab: dag, trace, evals, history, corpus or ir |

A link whose target no longer exists, such as a chunk removed by rechunking, shows the handler's error as a toast rather than failing silently. Links make an answer checkable in one click: "the expected section ranked ninth" becomes a link that opens that section.

### 30.3 `ui`: running a query, opening a tab or a section for the person

`ref` gives the person something to click. `ui` does the click for them, during the step:

| Call | Effect | Returns |
| --- | --- | --- |
| `await ui.query(q, { submit, filters })` | Sets the search box and runs it | `{ q, trigger, ms, top, answer, errors }` |
| `ui.filters(obj)` | Sets the filter signal | `'filters set'` |
| `ui.signals(obj)` | Sets any signals | `'signals set'` |
| `ui.tab(name)` | Opens an inspector tab | `'showing ' + name` |
| `ui.select(id)` | Selects a node in the DAG | `'selected ' + id` |
| `ui.chunk(id)` | Opens a section | `'opened ' + id` |
| `await ui.app(app)` | Mounts an app in the App tab | `mounted '…' in the App tab` |
| `ui.toast(msg)` | Shows a toast | `'ok'` |

`ui.query` is both an action and a measurement. It changes what the person sees and returns a summary Claude can reason about. A real step ran the running question and returned its top five, with each item's `id`, `score`, `doc` and `heading` reduced to a line:

```text
ok
- "1. 11. Why ChatGPT still showed no actions · 0.02108"
- "2. 15. Failure chronology and diagnostic lesson · 0.02035"
- "3. 1. The acceptance problem · 0.01999"
- "4. 6. OAuth discovery and registration incompatibilities › 6.1 RFC 9728 path-specific metadata · 0.01988"
- "5. 1. The acceptance problem › 1.1 Exact deployed identity · 0.01862"
```

The scores are reciprocal-rank-fusion scores (chapter 14), which is why they are small and close together. With `{ submit: true }` the query also runs the Claude nodes, the reranker and the answer, and the returned `answer` is clipped to 800 characters; the prompt's cost rule tells the agent not to do this unless asked.

On a narrow screen the page shows one panel at a time, and `ui` calls switch to the panel they act on. On a wide screen they leave the layout alone, so the agent cannot move the person's view away from the conversation they are reading.

The `ui` calls validate their arguments and fail with errors that list the valid choices, in the style of chapter 28.4: `ui.tab('graph')` throws `tabs: dag trace evals history corpus ir`, and `ui.select('nope')` throws `no node 'nope'`.

### 30.4 Conversations saved per user, and what is clipped when they are stored

When an answer finishes, the panel saves the whole conversation to the per-user database described in chapter 31. Each conversation is one document, and an index of conversations lives in the workbench's root document:

```text
data/users/<uid>/ragwb                         root: { convIndex: [{ id, title, updated, n }], agent: { tier, mode }, preset, script, … }
data/users/<uid>/ragwb/transcripts/<id>        { id, title, created, updated, messages: […] }
```

The stored shapes, from the real session:

```text
convIndex[0]   { id: 'cmvblfn10nf4b', title: 'Run the goldens, then show me the top results for the OAuth…', n: 16, updated: … }
user message   { role, text, at }
agent message  { role, text, parts, pending, at, tier, mode, usage }
tool part      { type: 'tool', code, status, at, t0, ms, result, logs, open }
output part    { type: 'out', kind: 'table', data: { ir: { kind: 'gui', …, root: { … rows … }, digest }, title, n }, at }
```

The title is the first message, clipped to 60 characters. The index keeps the 60 most recent conversations, and the panel's **History** button lists them by date with their message counts. The agent's model tier and run mode are saved in the root document when the person changes them, so a returning person gets the same mode.

Two kinds of clipping keep the stored record small. At save time every step's `result` is clipped to 2,500 characters and its `logs` to 1,000; the step in 28.3 that returned 9,040 characters was stored with 2,500. Then, if the conversation as JSON still exceeds 230,000 characters, any output whose compiled app exceeds 20,000 characters is replaced by a one-line note, `(too large to keep)`, and if that is not enough the oldest message pairs are dropped. Output parts store compiled gui apps, not screenshots, so a reopened conversation re-mounts its tables, charts and apps as working gui runtimes.

What a reopened conversation lacks is its snapshots. They live in memory with the step objects (29.4), so a conversation loaded from History shows its steps without revert links.

### 30.5 A worked session: diagnose a miss, branch, fix, score, merge

The pieces of Parts VI, VII and IX meet in one task, the second suggestion chip in the agent panel: "Fix the eval-tool miss on a branch, compare it with main, and keep it if it helps." What follows is that task as five steps on the Reports preset, written by the author and run through the scripted stand-in. Every result is real.

**Step 1: find the miss.** The golden questions run on `change`, which is lexical and free.

```js
const r = await wb.runEvals({ trigger: 'change' });
return { summary: r.summary, misses: r.misses };
```

```text
ok
summary:
  n: 12
  hit@5: 0.917
  mrr: 0.926
  ndcg@10: 0.79
  p50ms: 1.3
  calls: 0
misses:
  - q: How does the eval tool report line numbers in errors?
    rank: 9
    got: "Agent runtime spec — a portable chat agent with a single eval() tool › 21. Generative UI: gui › 21.6 Canvas"
    want:
      doc: Agent runtime spec
      section: /eval tool|execution/i
```

Eleven of twelve questions find a relevant section in the top five. The miss found the right document but the wrong section: the top hit is the spec's section on canvas drawing.

**Step 2: see where the expected sections rank.** The step runs the missed question with a deep `k` and finds every section the case would accept.

```js
const c = wb.lastEval.cases.find(c => !c.m['hit@5']);
const res = await ws.run({ q: c.q, k: 40 }, { trigger: 'change' });
const want = wb.chunks(x => /Agent runtime spec/i.test(x.title) && /eval tool|execution/i.test(x.headingPath.join(' '))).map(x => x.id);
return { q: c.q, top3: res.top(3).map(t => t.doc.slice(0, 40) + ' › ' + t.heading),
         expected: want.map(id => ({ id: id.split('#')[1], rank: res.hits.findIndex(h => h.id === id) + 1 || null })) };
```

```text
expected:
  - id: "5-1-definition"                       rank: 16
  - id: "5-2-execution"                        rank: 11
  - id: "5-2-execution-2"                      rank: 9
  - id: "5-3-what-good-eval-code-looks-like"   rank: 22
```

All four accepted sections sit under the heading "5. The eval tool", and the best of them ranks ninth. The spec's title, "Agent runtime spec: a portable chat agent with a single `eval()` tool", contains both query words, so every section of the spec matches them through its title, the canvas section included. In the BM25F of chapter 5.5, field weights scale term frequencies before saturation; with the title weighted 2, the title's matches account for much of each section's score for "eval" and "tool", and a section's own heading adds comparatively little. That suggests a fix: weight the title less.

**Step 3: try candidates on branches.** One step tries five changes, each on its own branch, and scores each with the goldens. Branches share the engine's build memo (chapter 11), so each rebuilds only `ix-text` when its op touches the index.

```text
candidate     hit@5   mrr     ndcg@10  rank of the missed case
heading 5     0.917   0.929   0.797    7
heading 8     0.917   0.929   0.797    7
title 1       1       0.933   0.803    5
meta 0        0.917   0.926   0.805    9
text k 60     0.917   0.926   0.79     9
```

Raising the heading weight helps a little; halving the title weight from 2 to 1 moves the missed case to rank 5 and loses no other case.

**Step 4: compare the chosen branch with main and show it.**

```js
const b = ws.branch('title-1');
await b.apply([{ op: 'set', node: 'ix-text', path: 'fields.title', value: 1 }], { by: 'agent' });
const built = await b.rebuild();
const d = await wb.evals.compare([ws, b], { trigger: 'change' });
chat.table(d.rows, { title: 'main vs title-1' });
return { rebuilt: built.computed, delta: d.delta, lineNumbersRank: d.cases.find(c => /line numbers/.test(c.q)) };
```

```text
ok
rebuilt: [ix-text]
delta:
  hit@5: 0.083
  mrr: 0.007
  ndcg@10: 0.013
  p50ms: -0.1
  calls: 0
lineNumbersRank:
  q: How does the eval tool report line numbers in errors?
  main: 9
  title-1: 5
```

The person sees the full comparison table in the conversation; Claude sees the deltas and the one case that moved. Note `{ by: 'agent' }` on the branch's op, which makes the History entry honest (29.1).

**Step 5: merge and confirm.**

```js
const rec = await ws.merge('title-1');
const built = await ws.rebuild();
const r = await wb.runEvals({ trigger: 'change' });
return { rec, rebuilt: built.computed, summary: r.summary, history: ws.history.map(h => `${h.revision} ${h.by} ${h.note || ''}`) };
```

```text
ok
rec:
  revision: 1
  changed: [ix-text]
  merged: title-1
  ops: 1
  …
rebuilt: []
summary:
  n: 12
  hit@5: 1
  mrr: 0.933
  ndcg@10: 0.803
  calls: 0
history: ["1 merge merge title-1"]
```

The merge replayed the branch's one op on main (chapter 23.2), and the rebuild computed nothing: the branch had already built `ix-text` with the same digest, and main found it in the shared memo. The History tab shows one `merge` entry, and the step's snapshot means the person can revert the whole merge in one click.

An honest reading of the result belongs in the agent's answer, and in this chapter. The fix moved one case from rank 9 to rank 5, exactly the edge of hit@5, and the gains in MRR and nDCG are small. Chapter 19.6 showed how far a single case can move these numbers on twelve questions. The change is defensible, because it has a mechanism and costs no other case, but it is not proof that title weight 1 is better for this corpus in general. An agent that reports "hit@5 is now 1.0" without that sentence has reported the number and not the evidence.

The key points to take from this chapter:

- An agent answer is text plus parts, and each output part is placed at the point in the text where the step that made it ran.
- Tables, charts, hit lists, eval reports and apps in the conversation are compiled gui apps on the same runtime as the search page, so an app bound to the workspace is live inside the conversation.
- Claude sees only that an output was shown, never its contents; values Claude must reason about have to be returned as well as shown.
- `ref` builds `wb:` links that run a query, open a section, select a node or open a tab when clicked, and `ui` performs the same actions during a step.
- Conversations are saved per user with step results clipped to 2,500 characters and outputs stored as compiled apps; snapshots are not saved.
- A complete improvement cycle is five steps: find the miss, locate the expected sections, try candidates on branches, compare and show, merge and confirm.

The agent, like every other part of the workbench, works inside limits it did not choose: one sampler, a small database, a prompt cap of 256 KiB, and a page with no server of its own. Part X describes that platform and how each of its limits shaped the design.

**Lab: ask the agent for a search page.** Load Reports · hybrid + rerank + answer and ask the agent: "Build me a small app with sliders for the fuse weights that reruns the goldens live." When the app appears in the conversation, move a slider and press its button, and check that the History tab gains an entry. Then ask the agent to mount the same app in the App tab with `ui.app`. Finally, ask it to explain the top result for your current query with links, and click each `wb:` link in its answer to see where it leads. These steps spend Claude usage.
