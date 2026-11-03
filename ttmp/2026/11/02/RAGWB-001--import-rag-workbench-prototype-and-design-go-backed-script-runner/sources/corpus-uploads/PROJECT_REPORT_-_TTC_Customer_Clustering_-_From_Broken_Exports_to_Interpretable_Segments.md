---
title: TTC Customer Clustering — From Broken Exports to Interpretable Segments
aliases:
  - TTC clustering complete report
  - thetreecenter.com segmentation project report
tags:
  - project
  - r
  - shiny
  - clustering
  - marketing
  - data-engineering
status: complete
type: project
created: 2026-09-03
repo: /home/manuel/code/gec/2026-09-03--marketing-analysis
publish: false
---

# TTC Customer Clustering — Complete Project Report

This report documents the complete customer-segmentation project for thetreecenter.com (TTC), from the first inspection of a set of CSV attachments to a running exploratory application with customer-grain segments and a marketing-ready export. Two companion notes cover the individual phases in depth — the order-grain foundation ("PROJ - TTC Customer Clustering - Order-Grain Segmentation and the Shiny Explorer") and the customer-grain phase ("PROJ - TTC Customer Clustering - Tier B Customer Segmentation"). This note covers the whole arc: how the problem was reframed twice by what the data turned out to support, how the segmentation vocabulary was built before the segments were, what the model-selection process actually established about the data, and what the engineering workflow looked like. The writing assumes no prior familiarity with the repository.

> [!summary]
> - The supplied CSV exports had no customer key and covered a filtered catalog slice; the project discovered the store's dbt warehouse and designed four customer-keyed exports (277k customers, 474k orders, 978k line items, 24k subscriptions).
> - Tier A built the order-grain vocabulary — product classification cascade, basket signatures, attach rates, lift, k-means order tiers, seasonality distributions — and shipped it as a Shiny explorer with per-tab mathematics panels and offline reference copies.
> - Tier B aggregated orders into 282,180 customer rows, ran RFM on everyone and behavioral clustering on the 77,187 repeat buyers, and produced a marketing export.
> - The central statistical finding: at this sample size, information criteria do not identify component counts for either the Gaussian mixture or the latent class model; the project's response was to publish the full criterion curves, fix counts by explicit reproducible rules, and label the choice as a resolution decision rather than a discovery.
> - The project ran on a documented process: a docmgr ticket with a fifteen-step investigation diary, phase-gated thermal work slips, and commits at phase boundaries.

## What was asked, and what the data said

The request was to cluster TTC's customers for marketing. The starting material was a `data/` directory of CSV exports. Inspection established two facts that reframed the work before any modeling happened.

First, the market-basket export contained no customer identifier at all — order numbers and products, nothing else. Clustering customers requires a customer key; there was none.

Second, the export covered roughly a dozen products: care addons, the Thrive membership, and a handful of trees. Line items for most of the catalog were absent. A segmentation built on this slice would describe a filtered view of the business rather than the business.

The response was not to work around the exports but to go around them. The store runs a dbt warehouse over its WooCommerce database, with materialized models for orders, line items, customers, subscriptions, and products. The project read the warehouse, designed four export queries against its models, and validated the results on landing: essentially complete key coverage between line items and parent orders, `billing_email` as the guest-inclusive customer key the warehouse itself uses, and split-shipment children correctly resolved to their parent orders through a source-order identifier.

One warehouse property became a design constraint for everything downstream: the product flags on line items are produced by joining to live product posts, so when a product has been deleted from the store, every flag on its historical line items comes back empty. A substantial share of historical sales is affected. An analysis that trusted the flags would silently treat years of real sales as unclassifiable.

## Tier A: building the vocabulary before the segments

With customer-keyed data available but no customer-grain modeling yet, the project built an order-grain baseline first. The reasoning was that order-grain work is immediately possible, that its outputs become the features and the naming vocabulary for the customer-grain phase, and that an order-grain explorer delivers value even if the later phases stall.

The foundation is a product classification cascade ordered by signal reliability: giftcards by SKU pattern (a live giftcard post carries the generic product flag and would otherwise count as a plant), membership by its flag, care addons by their flag, live products by their flags, and everything else — the deleted products with empty flags — as a legacy class classified by genus keyword against the product names, which survive deletion. The cascade encodes a principle worth stating explicitly: classify by the failure mode you can detect. An empty flag vector means the join failed; treating it as "none of the above" is how deleted products disappear from an analysis.

On top of the cascade sit the order-grain statistics: basket signatures (the sorted set of dimensions present in an order, which turns half a million binary vectors into a countable set of combinations), conditional attach rates by dominant plant category, pairwise lift computed as a single matrix cross-product over the binary basket matrix, k-means order tiers with cluster count chosen by silhouette computed on a sample, and seasonality as row-normalized monthly distributions — conditioning on the row so that the answer to "when do maple buyers buy?" is not swamped by "maple buyers are numerous."

The statistical texture of the business that emerged: three quarters of orders contain plants and nothing else; care products attach to under a fifth of plant orders, with the attach rate varying meaningfully by genus; the two care addons co-occur at several times the independent rate; membership purchases cluster with warranty purchases; and a refund-adjusted zero-or-negative-total tier exists as a data-quality bucket rather than a behavioral one.

## Tier B: one row per customer

Tier B condensed each customer's order history into a single row. The feature table carries 38 columns in four groups: RFM quantities in raw and log form; behavioral rates (addon, warranty, discount, spring and fall shares); product-mix shares over the twelve most frequent dominant plant categories; and cohort fields including the median inter-order gap, which is defined only for repeat buyers and is left missing for the rest.

Two population decisions preceded any model. Because 72.6% of the 282,180 customers have exactly one order, behavioral clustering runs on the 77,187 repeat buyers — the single-order mass would otherwise flatten every centroid — while the RFM segmentation covers everyone, since recency and monetary are meaningful for a one-time buyer and the win-back question applies to them most. And frequency scoring uses fixed cutpoints rather than quintiles, because quintiles of a variable whose median is one are arithmetic without meaning.

The RFM table reads as a marketing brief on its own: a third of the base is recent first-time buyers, a quarter is dormant with low historical value, and the champions — 6.6% of customers with a median of four orders and around five times the one-time median lifetime revenue — were last seen within the last two years. The k-means solution over thirteen standardized behavioral features produced seven named clusters (care-focused repeaters, landscapers, lapsed spring buyers, an engaged core with the strongest membership overlap, and others), with silhouette 0.136 — honestly low, as thirteen-dimensional behavioral data does not separate into islands — and bootstrap stability of adjusted Rand index around 0.62 with meaningful variance across runs.

## What model selection actually established

The most transferable findings of the project came from asking the mixture models how many components the data contains.

The Gaussian mixture fit over the repeat-buyer features, searched over component counts from one to fifteen and four diagonal covariance families, returned a split verdict: the equal-shape families' BIC improves monotonically with every added component all the way to the top of the range, while the variable-shape families prefer a single component. The criterion does not identify a component count. Equal-shape components keep paying for themselves by splitting continuous clouds; flexible components absorb the data's shape without splitting.

The latent class model over twelve binary indicators repeated the pattern with a different mechanism: with 77,000 observations, an additional class costs only on the order of a hundred and fifty BIC units while genuine local dependence keeps paying more, so the BIC rises through twelve classes and beyond.

A criterion that selects the edge of its search range is a finding, not a result. The project's response, applied identically to both models, was to publish the full criterion curves as diagnostics, fix the deliverable's component count by an explicit and reproducible rule — an interpretable pinned value for the mixture, a knee rule (the first count whose marginal BIC gain falls below ten percent of the first step's gain) for the latent classes, landing at eight — and to state in the application's mathematics panel that the count is fixed for interpretability rather than selected by the criterion. This is the difference between reporting a number and reporting what the number means.

The latent class output earned its place as the most legible segmentation. Its classes come with conditional indicator probabilities as crisp as 1.00 — the bulk-buyer indicator is deterministic inside the bulk class, the membership indicator deterministic inside the members class — which means the classes are close to partition rules and safe to name in a marketing brief. The largest classes are the spring-evergreen multi-year repeaters (28.6%), the engaged big spenders (18.2%), and the spring warranty buyers (12.1%).

## The application, and its failure catalog

The Shiny explorer is the project's user-facing artifact: seven tabs spanning order-grain views (overview, orders browser, archetypes, attach and lift, tiers, seasonality) and the customer page, each with a mathematics panel rendering the formulas behind its statistics, a collapsible glossary of every label the page uses, and links to local markdown copies of the reference material so the documentation survives offline.

Two architectural decisions carry the application. Every tab derives from a single filtered order population wired to one global time filter — a reactive with two branches (all-time, or a date window), consumed by every downstream view — so that no statistic on any page can silently disagree with another. And the orders browser renders a capped sample through server-side DataTables while its summary line is computed on the full filtered set, with personal-data columns used for joins and counts but never displayed.

The application is also the project's most concentrated catalog of failure modes, each documented with mechanism and fix: an event-reactive that left a tab empty until its first click because event reactives evaluate nothing before their event fires; a character column in the basket matrix that crashed the lift computation because the matrix product requires a numeric matrix; a date-range input whose unset end date arrives as a missing value and silently filters out everything; browser session tokens that die whenever the application process restarts, leaving tables stuck on a processing spinner until reload; and, on this machine, an application server that must be launched detached with `setsid` to survive the shell session that started it.

## Process: how the work was tracked

The project ran on an explicit process. A docmgr ticket holds the analysis document, an intern-guide design document covering the full system, and a fifteen-step investigation diary that records every command, every failure with its verbatim error, and the reasoning at each step — the diary's standing value is that most surprises in this codebase were surprises once. Code changes were committed at phase boundaries with the diary and ticket bookkeeping updated in the same commit. Phase-gated work slips were printed on a thermal printer at each phase start and completion, giving the work a physical checkpoint trail. Reference material consulted during implementation — package documentation and papers — was fetched and stored as markdown or PDF inside the ticket's sources directory, so the provenance of every methodological decision is inspectable offline.

## The deliverable inventory

What exists at the end of the project:

- Four export queries against the dbt warehouse (`scripts/02–05`), documented and validated.
- A feature and analysis pipeline: order-grain prep and analysis (`06`, `07`), customer feature table (`08`), segmentation models with stability (`09`), latent class analysis (`10`), and the marketing export writer (`11`).
- Committed aggregate outputs under `output/` — RFM and cluster profiles, BIC curves, stability, seasonality — containing no row-level data.
- The explorer, seven tabs, running locally with all mathematics panels and glossaries.
- A marketing-ready file joining each customer email to its segment assignments and core statistics, held in the gitignored derived directory because it contains personal data.
- The documentation set: this note, the two phase reports, the intern guide, and the diary.

## Open questions

The population split is a modeling commitment that could be revisited: a growth-mixture or sequence model over the single-order majority might separate "will reorder" from "never will" earlier than RFM recency does. Guest emails are treated as distinct customers; household deduplication would refine frequency features. The `order_only` membership status — a few hundred customers whose orders flag a membership that has no subscription row — should be confirmed as legacy manual memberships rather than data error before an audit relies on it. The latent class output exists as profile tables but is not yet wired into the explorer, and its classes have no production names. And the k-means solution's moderate stability means boundary customers are assignment-noisy; if downstream decisions are sensitive to them, a consensus or medoids-based comparison is the next step.

## Key points

- The project's shape was set by two data discoveries: no customer key in the supplied exports, and dead flags on deleted products in the warehouse. Both were met by design decisions (warehouse exports; a classification cascade keyed to detectable failure modes) rather than by assumptions.
- Segmentation value came from ordering the work: vocabulary before segments, baselines before models, everyone-questions (RFM) separated from repeater-questions (behavioral clustering).
- The mixture and latent-class component counts are not identified by information criteria at this sample size; the full curves are published and the deliverable counts are fixed by explicit rules.
- Every statistic in the explorer is recomputable within a selectable time window, and every label on every page has a glossary entry and a mathematics panel behind it.
