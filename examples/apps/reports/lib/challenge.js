// A harder, hand-labelled evaluation over the bundled reports. Similar project
// titles and shared terminology provide distractors; unanswerable cases ask for
// plausible project details that are not documented in this corpus. Labels are
// corpus-relative, not claims that the questions have no answer anywhere.
const answerable = [
  { id: 'identity-rebase', q: 'When a sync conflict is rebased, why does the next concrete send need another operation ID?', doc: 'Workbench Identity', section: /conflict rebasing/i },
  { id: 'identity-framing', q: 'How are multibyte strings separated when hashing operation identity?', doc: 'Workbench Identity', section: /byte-length framing/i },
  { id: 'identity-local', q: 'When does an installed local revision advance?', doc: 'Workbench Identity', section: /installed-state generation/i },
  { id: 'core-sync', q: 'How does the headless workbench keep a transaction batch intact during synchronization?', doc: 'Workbench Core', section: /batch-preserving sync/i },
  { id: 'core-document', q: 'Which boundary allows a product to provide a document ID that the workbench does not own?', doc: 'Workbench Core', section: /connectDocumentSource/i },
  { id: 'stabilization-reentry', q: 'What happens when an observer tries to execute another workbench transaction during publication?', doc: 'Workbench Stabilization', section: /reentrancy rule/i },
  { id: 'stabilization-errors', q: 'How can a workbench commit succeed if a subscriber throws during notification?', doc: 'Workbench Stabilization', section: /observer errors/i },
  { id: 'forum-atomic', q: 'What database boundary keeps a forum thread and its corresponding event consistent?', doc: 'SQLite-Backed Forum', section: /transaction boundary/i },
  { id: 'forum-metadata', q: 'Which forum metadata representation remains authoritative alongside its flattened searchable terms?', doc: 'SQLite-Backed Forum', section: /metadata/i },
  { id: 'web-protobuf', q: 'Which protobuf JSON encoding traps did the Agentforum web interface encounter?', doc: 'Agentforum Web', section: /type traps/i },
  { id: 'coinvault-scope', q: 'Where did an authorized tool scope get lost between consent and the MCP tool handler?', doc: 'CoinVault MCP', section: /tool-scope propagation/i },
  { id: 'agent-undo', q: 'Which execution modes give the portable eval agent an undo boundary?', doc: 'Agent runtime spec', section: /safety|run modes/i },
];

const unanswerable = [
  { id: 'identity-checksum', q: 'What SHA-512 digest identifies the PBUI operation ID production signing key?' },
  { id: 'core-redis', q: 'Which Redis cluster hostname stores the headless workbench command log?' },
  { id: 'forum-graphql', q: 'What is the GraphQL mutation name for creating an Agentforum thread?' },
  { id: 'forum-kafka', q: 'What Kafka topic contains Agentforum inbox events?' },
  { id: 'web-grpc-port', q: 'Which TCP port does the Agentforum web browser use for a gRPC streaming endpoint?' },
  { id: 'coinvault-bcrypt', q: 'What bcrypt cost factor hashes CoinVault employee passwords?' },
  { id: 'clustering-snowflake', q: 'What Snowflake warehouse name hosts the TTC customer segments?' },
  { id: 'workbench-kubernetes', q: 'What Kubernetes namespace runs the PBUI workbench production service?' },
];

function asEvals(rag) {
  let ev = rag.evals('reports-challenge');
  for (const c of answerable) ev = ev.case(c.q, {doc:c.doc, section:c.section}, {id:c.id});
  return ev.metrics(['hit@5', 'mrr', 'ndcg@10']);
}

// Evaluate answerable ranking separately from unanswerable rejection. Any run
// error aborts the measurement: an unavailable model must not look like a
// successful rejection. Evaluation runs use submit so decision nodes execute.
async function measure(rag, plan, opts = {}) {
  const engine = opts.engine || rag.engine();
  const trigger = opts.trigger || 'submit';
  if (trigger !== 'submit') throw new Error('challenge requires submit: decision nodes must run');
  const ir = rag.compile(plan);
  const positive = await asEvals(rag).run(ir, {engine, trigger});
  const invalid = positive.cases.filter(c => c.errors && c.errors.length);
  if (invalid.length) throw new Error('answerable evaluation failed: ' + invalid[0].errors.join('; '));
  const unmatched = positive.cases.filter(c => c.R === 0);
  if (unmatched.length) throw new Error('challenge label matches no corpus section: ' + unmatched.map(c => c.q).join('; '));
  const negative = [];
  for (const c of unanswerable) {
    const result = await engine.run(ir, {q:c.q, k:10}, {trigger});
    if (result.errors.length) throw new Error('unanswerable evaluation failed for ' + c.id + ': ' + result.errors.join('; '));
    const hits = result.top(10);
    negative.push({id:c.id, q:c.q, rejected:hits.length === 0, count:hits.length, top:hits[0] || null, calls:result.calls, ms:result.ms});
  }
  const rejected = negative.filter(c => c.rejected).length;
  const falseRejects = positive.cases.filter(c => c.top.length === 0).length;
  return {
    summary: {
      answerable: positive.summary.n, unanswerable: negative.length,
      hit5: positive.summary['hit@5'], mrr: positive.summary.mrr, ndcg10: positive.summary['ndcg@10'],
      false_rejects: falseRejects, false_rejection_rate: +(falseRejects / positive.cases.length).toFixed(3),
      rejected, rejection_rate: +(rejected / negative.length).toFixed(3),
      false_accepts: negative.length - rejected,
      calls: positive.summary.calls + negative.reduce((sum, c) => sum + c.calls, 0),
    },
    answerable: positive.cases, unanswerable: negative,
  };
}

module.exports = { answerable, unanswerable, asEvals, measure };
