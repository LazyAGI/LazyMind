# Synthetic paid-channel monthly report

Use the Performance Report skill to produce a concise monthly paid-channel report for a marketing lead. This is invented test data in USD, not actual business performance. Reporting period: October 2026; comparison: September 2026. No connectors are connected. All data is provided below, so do not request uploads or call external services. Do not send messages, change campaigns, or make purchases. Deliver a readable Markdown report Artifact and intermediate calculation evidence.

period,channel,spend_usd,impressions,clicks,conversions,revenue_usd
2026-09,Search,1000,50000,2000,100,5000
2026-09,Social,1000,100000,1000,20,1000
2026-10,Search,1200,60000,2400,120,6000
2026-10,Social,800,80000,800,16,800

Calculate channel and combined CTR, CPC, CPM, conversion rate (conversions/clicks), CPA, ROAS and month-over-month changes using totals, not averages of channel rates. Targets: combined CPA <= 20 USD; combined ROAS >= 3.0. Use these explicit targets only for status; no supplied targets for other metrics. Include executive summary, metrics dashboard, limited period comparison, what worked, what needs improvement, insights, prioritized impact/effort recommendations and next-period focus. Do not invent 3-5 distinct winners or losers from only two channels. Mark causal explanations as hypotheses. Two periods cannot establish seasonality, long-term trends, statistical significance or a reliable forecast. State that attribution method, unique reach and customer quality are unknown. Stop after the report; no email, slide or ongoing monitoring follow-up is requested.


Calculation contract for this rerun: compute every rate, difference and percentage change from the original unrounded totals, then round each displayed field independently to two decimal places using decimal ROUND_HALF_UP. Never subtract rounded displayed rates. Emit 34 unique metric rows: the 11 base/rate metrics for Search, Social and Combined, plus search_share_of_combined_spend_pct; each row must contain this_period, prior_period, change and change_pct (136 values total). These are calculation constraints, not additional observations.
