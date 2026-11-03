---
title: TTC Customer Clustering — Tier B Customer Segmentation
aliases:
  - TTC Tier B
  - TTC customer-grain segmentation
tags:
  - project
  - r
  - shiny
  - clustering
  - marketing
  - latent-class-analysis
status: active
type: project
created: 2026-09-03
repo: /home/manuel/code/gec/2026-09-03--marketing-analysis
publish: false
---

# TTC Customer Segmentation — Tier B

This report documents Tier B of the TTC customer-clustering project: the move from order-grain analysis (documented in the companion Tier A report) to customer-grain segmentation, which was the actual deliverable the project was asked for. It covers the feature table that condenses every customer's order history into one row, the three segmentation families built on top of it — rule-based RFM, k-means, and model-based mixtures — the statistical problem that emerged when information criteria were asked to count the clusters, and the extension of the Shiny explorer with a customer page. The report is written so that a reader who has not followed the project can reconstruct the reasoning, including the parts that failed and why.

> [!summary]
> - Tier B aggregates 447k completed orders into 282,180 customer rows with 38 features: recency/frequency/monetary, behavioral rates, product-mix shares, cohort fields, and Thrive membership status.
> - 72.6% of customers have exactly one order, so behavioral clustering runs on the 77,187 repeat buyers; RFM covers everyone.
> - The k = 7 k-means solution is only moderately stable (bootstrap ARI ≈ 0.62–0.76); the Gaussian mixture's BIC and the latent-class BIC both keep buying components at this sample size, so component counts are fixed at interpretable values with the full criteria curves saved as diagnostics.
> - Latent class analysis on twelve binary indicators produced the most marketing-legible segments: spring evergreen repeaters, bulk buyers, Thrive members, and fall discount users, with conditional probabilities as crisp as 1.00.
> - A marketing-ready export (email + segment + core stats) exists in `derived/`, and the Shiny explorer gained a Customers page whose KPIs were verified in a browser against the model output.

## From orders to customers

Tier A answered the question "what do orders look like?" A customer who placed three orders appeared in three rows there, each carrying its own tier and basket signature. Marketing, however, acts on customers, not orders: the win-back email goes to a person, the loyalty program enrolls a person. Tier B therefore aggregates each customer's full order history into a single row and clusters those rows.

Tier A is not discarded in this step. It becomes the raw material: the order fact table built by the Tier A prep script is the input to the Tier B feature builder, and the Tier A vocabulary (addons, Thrive, plant categories, seasons) is what the Tier B segment descriptions are written in.

### The feature table

The feature builder (`scripts/08-tier-b-prep.R`) reads the completed-order fact table and produces one row per billing email. The warehouse keys customers by billing email — guests included — which was validated during the export design in Tier A. The feature groups are:

- **RFM core**: number of orders, lifetime revenue, average order value, and recency measured as days from the data horizon to the customer's last order. Monetary and count features are also carried in log form, because revenue distributions in retail data span several orders of magnitude and clustering on raw dollars lets a handful of whale customers dominate every centroid.
- **Behavioral rates**: the share of the customer's orders containing care addons, warranties, or discounts; the share falling in the spring and fall planting windows; average quantity per order.
- **Product mix**: for the twelve most frequent dominant plant categories (maple, arborvitae, hydrangea, and so on), the share of the customer's orders whose dominant category is that genus. This is the "Japanese maple person versus privacy hedge buyer" signal, computed over a whole customer history rather than per order.
- **Cohort**: first-order year, number of distinct active years, tenure, and — for repeat buyers only — the median gap in days between consecutive orders. Gap features are structurally undefined for single-order customers and are left missing rather than filled with a sentinel zero that would distort distances.

Two construction details turned out to matter. First, the discount rate was initially computed from the line-level discount column of the warehouse export; that column is never populated (every value is zero), which silently produced a zero-variance feature and crashed the clustering with a confusing numeric error inside `kmeans`. The order-level discount column is the live one. Second, Thrive membership status could not be joined directly: the subscriptions export is keyed by customer id and order id, not by email. The mapping resolves each customer id to the email with the most orders, and a residual class of customers whose orders flagged a membership but who have no subscription row — a few hundred — is kept as a distinct `order_only` status rather than being forced into "never."

The resulting table has 282,180 rows and 38 columns. Of those customers, 204,993 — 72.6% — have exactly one order. That single number shaped every modeling decision that followed.

## Choosing the population before choosing the model

A segmentation of all 282k customers by behavioral features would be dominated by the single-order mass: their addon rates and category shares are determined by one basket, and their gap and frequency features are undefined. Every centroid would be pulled toward the one-and-done profile, and the structure that exists — the difference between a landscaper who orders thirty trees every season and a collector who orders one rare maple every other year — would be flattened.

The project therefore split the population by purpose:

- **Everyone** gets the RFM segmentation, because recency and monetary are meaningful for a single-order customer and the win-back question ("who used to buy and stopped") applies to them most of all.
- **Repeat buyers only** (77,187 customers, 27.4%) get the behavioral clusterings, because that is where the features have variance and the segments have meaning.

This split is stated explicitly in the explorer's customer page, so nobody reads a repeat-buyer cluster profile as a statement about the whole base.

## RFM: the baseline that does not need a model

The RFM segmentation assigns each customer a recency quintile (inverted, so five means most recent), a frequency score, and a monetary quintile, then names segments by rules over the scores. One deviation from the textbook version was forced by the data: frequency quintiles are meaningless when the median is one order, so frequency uses fixed cutpoints (one order, two, three-to-five, six-plus).

The resulting segment table is well-behaved and immediately actionable:

| Segment | Share | Median orders | Median revenue | Median recency |
|---|---|---|---|---|
| new_or_one_time | 33.3% | 1 | $161 | 729 days |
| hibernating | 27.0% | 1 | $108 | 2,327 days |
| mid | 17.4% | 1 | $158 | 1,722 days |
| big_spenders_lapsed | 10.7% | 1 | $348 | 2,308 days |
| champions | 6.6% | 4 | $829 | 560 days |
| loyal | 2.8% | 3 | $632 | 1,627 days |
| at_risk_loyal | 2.3% | 3 | \$583 | 2,261 days |

Read as a marketing brief: a third of the base is recent first-time buyers who need a second-order nudge; a quarter is dormant with low historical value; the champions — under seven percent of customers — order around four times with a median lifetime revenue five times the one-time median, and they were last seen within the last two years. The `at_risk_loyal` and `big_spenders_lapsed` rows are the win-back lists.

RFM is not clever, and that is the point: every more elaborate model has to be compared against it, and for the audiences defined above it is not obvious that anything more elaborate does better.

## Behavioral clusters: k-means and what stability means

The k-means clustering runs on thirteen standardized features over the repeat buyers: log order count, log average order value, recency, addon/warranty/discount rates, spring and fall shares, average quantity, plant-order share, and the three largest category shares. The cluster count is chosen by mean silhouette computed on a random sample — the same sampled-silhouette pattern as Tier A, for the same reason: a full distance matrix at this size is infeasible rather than merely slow. The silhouette peaks at k = 7, at 0.136.

A silhouette of 0.136 is low, and it is honest to say so: thirteen-dimensional behavioral data does not separate into crisp islands, and any clustering of it is a compression, not a discovery of natural kinds. The more informative quality measure is stability: refit the clustering on bootstrap resamples and measure label agreement with the reference solution using the adjusted Rand index, which is invariant to label switching. The measured mean ARI across twenty resamples was 0.616 with a standard deviation of 0.16 — and, notably, this number moved between otherwise-identical configurations (0.757, 0.685, 0.616 across runs with different random states), which is itself evidence about the solution: it is moderately stable, and its exact shape depends on which local optimum the algorithm lands in. The final script pins seeds per stochastic block so the pipeline is reproducible, and reports the stability honestly in the explorer rather than presenting one favorable draw.

The seven clusters, named from their profiles, are: care-focused repeaters (addon attach 0.72); off-season discount buyers (spring share 0.15); landscapers (eleven plants per order, AOV around \$505); small-cart warranty buyers; a rare addon-heavy class; lapsed spring buyers (the largest bucket, recency around five years); and an engaged core — nearly five orders, recent, warranty attach 0.42, and the strongest Thrive-membership overlap at 12.6%.

## The mixture models, and a lesson about BIC

The Gaussian mixture model (mclust, diagonal covariance families) was included for a specific reason: soft membership. In a mixture, every customer has a probability of belonging to every component, and for a marketing team deciding how aggressively to treat a borderline case, that probability is more useful than a hard centroid assignment.

The first fit searched component counts from one to nine and selected nine — the top of the searched range. A criterion that selects the edge of its search space is telling you it has not found its optimum, so the search was extended to fifteen. The result is the most statistically interesting finding of Tier B:

- Under the equal-shape covariance families, BIC keeps improving with every additional component, all the way to fifteen, with no plateau. The last few components each still buy over a thousand BIC units.
- Under the variable-shape families, BIC prefers a single component.

In other words, the criterion does not identify a component count. Equal-shape components keep splitting the continuous clouds of the feature space — each split reduces the within-component misfit enough to pay for the extra parameters — while flexible components absorb the shape of the data without needing splits. The mixture is a reasonable soft-clustering view, but "how many Gaussian components are in this data" is not a question this data can answer, and pretending otherwise would ship a number that is an artifact of the search range.

The deliverable therefore pins the GMM at the interpretable nine-component equal-shape solution, saves the full BIC curve as a diagnostic, and states in the explorer's mathematics panel that the component count is fixed for interpretability rather than selected by BIC. The same statement, in the same words, belongs in any handoff document.

### Latent class analysis: the interpretable alternative

The last model family addresses the objection that all of the above segments describe positions in a continuous feature space, while marketing teams think in terms of behaviors that a customer either exhibits or does not. Latent class analysis fits exactly that representation: the input is a set of binary indicators, and the output is the probability of each indicator conditional on class membership plus the class sizes. The classes read like survey segments.

The twelve indicators (spring buyer, fall buyer, addon buyer, warranty buyer, discount user, frequent, big spender, maple buyer, evergreen buyer, Thrive member, multi-year active, bulk buyer) collapse the 77k repeat buyers into fewer than 4,096 distinct patterns, which matters for implementation: BayesLCA's expectation-maximization is linear in the number of rows per iteration in pure R, and running it on the raw rows was slow enough to be aborted. Collapsing to the pattern table with per-pattern counts — a sufficient statistic the package explicitly supports — reduced the fit from hours to under two minutes.

The BIC behavior repeated: with 77k observations, the criterion keeps buying classes (twelve and still rising), because each additional class costs only around a hundred and fifty BIC units while genuine local dependence keeps paying more than that. The deliverable selects the knee — the first count whose marginal gain falls below ten percent of the first step's gain — which lands at eight classes, and saves the curve.

The classes themselves are the most legible output of the project:

| Class | Share | Defining conditional probabilities |
|---|---|---|
| Spring evergreen repeaters | 28.6% | spring 0.81, evergreen 0.46, multi-year 0.42 |
| Engaged big spenders | 18.2% | multi-year 0.90, big spender 0.86, frequent 0.82 |
| Spring warranty buyers | 12.1% | spring 0.88, warranty 0.63, addon 0.52 |
| Fall discount buyers | 11.7% | fall 0.69, discount 0.57 |
| Bulk buyers | 11.2% | bulk 1.00, big spender 0.79 |
| Thrive members | 7.1% | member 1.00, discount 1.00, warranty 0.81 |
| Big-spender bulk | 6.7% | big spender 1.00, bulk 0.94 |
| Fall big spenders | 4.4% | fall 1.00, big spender 0.82 |

A conditional probability of 1.00 for bulk buying inside the bulk class is not a modeling triviality — it says the classes are close to deterministic partition rules on the indicators, which is exactly what makes them safe to name in a marketing brief.

## The explorer's customer page

The Shiny application gained a Customers page built on the same pattern as the Tier A tabs: reactive data flow from the global time filter, a mathematics panel explaining every statistic on the page, a collapsible glossary of segment labels, and links to local copies of the reference material.

The one design decision that required care is the semantics of the global time filter at customer grain. Customer features are all-time aggregates by construction; re-computing them per window would mean re-reading the order stream on every filter change. The page instead filters the *population*: a customer is in scope if their first-to-last-order interval overlaps the selected window, and their features remain lifetime aggregates. The intro text states this in plain words, because the alternative reading — "these are the customer's 2024 features" — would be wrong.

Verification was done in a real browser with a scripted client: the KPI values on the page (282,180 customers; 77,187 repeat buyers; 7,850 active Thrive members; 18,536 champions) match the model outputs exactly, selecting calendar year 2024 narrows the population to 38,195 customers with 20,055 repeat buyers, and the page produces zero client errors.

## The marketing export

The segment assignments exist as a joinable file: one row per customer email with RFM segment, both cluster assignments, Thrive status, and the core statistics, sorted by lifetime revenue (`derived/marketing-segments.csv`, produced by `scripts/11-marketing-export.R`). It contains personal data and lives in the gitignored derived directory; the aggregate profiles that describe the segments without identifying customers are the committed deliverables.

## What was learned

Three lessons generalize beyond this project:

1. **Choose the population before the model.** The single-order majority is not noise to be absorbed by the algorithm; it is a different population for a different question. RFM for everyone, behavioral clustering for repeaters.
2. **An information criterion that selects the edge of its search range is a finding, not a result.** Both the Gaussian mixture and the latent class model exhibited non-identifiable component counts at this sample size. The defensible response is to report the full criterion curve, fix the count by an explicit and reproducible rule, and label the choice as a resolution decision.
3. **Sufficient statistics are an implementation strategy, not just a theory topic.** The LCA went from hours to minutes not by a faster algorithm but by noticing that twelve binary indicators have at most 4,096 patterns.

## Open questions

- The LCA classes are not yet wired into the explorer; they exist as profile tables in the output directory. If marketing adopts them as the working vocabulary, the page should present them alongside the k-means clusters.
- Stability of the k-means solution is moderate (ARI ≈ 0.6). If downstream decisions are sensitive to boundary customers, consensus clustering or a k-medoids fit with a distance designed for mixed feature types would be the next comparison.
- The `order_only` Thrive status (381 customers) should be confirmed as legacy manual memberships rather than data error before the membership features are trusted in an audit.
- Guest emails are treated as distinct customers; household and shared-address deduplication would refine the frequency features.

## Key points

- `scripts/08-tier-b-prep.R` builds the customer feature table; `09` fits RFM, k-means, and the Gaussian mixture with stability; `10` fits the latent class model; `11` writes the marketing export.
- `output/b1..b5-*.csv` are the committed, row-level-free deliverables: RFM profiles, cluster profiles, BIC curves, stability, and LCA class tables.
- The companion Tier A report documents the order-grain foundation and the explorer architecture; the intern guide in the ticket documents the full system.
