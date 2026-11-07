// evals.js — golden questions as their own command: one metrics row per plan.
const { rag } = require("./lib/corpus");
const kit = require("ragwb/kit");
const plans = require("./lib/plans");

__package__({ name: "reports" });

async function goldens(names, trigger) {
  const rows = [];
  for (const name of names) {
    if (!plans[name]) throw new Error("unknown plan " + name + " (have: " + Object.keys(plans).join(", ") + ")");
    const ev = await kit.goldens().run(rag.compile(plans[name]), { engine: rag.engine(), trigger });
    rows.push({ plan: name, trigger, n: ev.summary.n, "hit@5": ev.summary["hit@5"], mrr: ev.summary.mrr, "ndcg@10": ev.summary["ndcg@10"] });
  }
  return rows;
}
__verb__("goldens", {
  short: "Score plans on the 12 golden questions",
  fields: {
    names: { type: "stringList", default: ["lexical", "fused"], help: "Plans to score (dense ones need --embed lunaroute)" },
    trigger: { type: "choice", choices: ["change", "submit"], default: "change", help: "submit includes rerank.model / LLM nodes" },
  },
});

const challengeSet = require('./lib/challenge');

// threshold is an integer percent to keep the command portable across hosts.
async function challenge(names, threshold, details) {
  if (!Number.isInteger(threshold) || threshold < 0 || threshold > 100) throw new Error('threshold must be an integer from 0 to 100');
  const rows = [];
  for (const name of names) {
    let plan = plans[name];
    if (!plan || !['lexical', 'fused', 'graded', 'filtered'].includes(name)) throw new Error('challenge supports lexical, fused, graded and filtered');
    if (name === 'filtered') {
      const filter = rag.compile(plan).nodes.find(n => n.t === 'filter.decide');
      if (!filter) throw new Error('filtered plan has no decision filter');
      plan = plan.patch([{op:'set', node:filter.id, path:'threshold', value:threshold / 100}]);
    }
    const report = await challengeSet.measure(rag, plan, {engine:rag.engine(), trigger:'submit'});
    if (details) {
      for (const c of report.answerable) rows.push({plan:name, kind:'answerable', question:c.q, rank:c.rank, relevant:c.R, hit5:c.m['hit@5'], top:c.top[0] ? c.top[0].doc + ' › ' + c.top[0].heading : ''});
      for (const c of report.unanswerable) rows.push({plan:name, kind:'unanswerable', question:c.q, rejected:c.rejected, hits:c.count, top:c.top ? c.top.doc + ' › ' + c.top.heading : ''});
    } else rows.push({plan:name, threshold:name === 'filtered' ? threshold / 100 : null, ...report.summary});
  }
  return rows;
}
__verb__('challenge', {
  short: 'Measure ranking and rejection on harder report questions',
  fields: {
    names: {type:'stringList', default:['lexical','fused','graded','filtered'], help:'Plans to compare; graded and filtered require --decide'},
    threshold: {type:'int', default:50, help:'Filtered-plan relevance threshold, as 0–100 percent'},
    details: {type:'bool', default:false, help:'Return individual answerable and unanswerable cases'},
  },
});
