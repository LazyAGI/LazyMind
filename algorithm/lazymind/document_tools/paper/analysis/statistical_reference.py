"""Data-free library examples, not an execution whitelist or a reference calculator.

Chi-square/Fisher/Mann-Whitney/ANOVA/Bootstrap examples reuse APIs exercised in
codex/f4-reliable-analysis-20261026 (4d0664af). No old job/emit protocol is imported.
"""
import inspect


EXAMPLES = (
    ('chi2_contingency', ('卡方', 'chi-square', 'chi2', 'chisquare'),
     'result = stats.chi2_contingency(observed_counts, correction=use_yates_correction)',
     'Independence from a contingency COUNT table; choose correction explicitly. '
     'Preserve dof and expected counts; percentages are not counts.'),
    ('chisquare', ('拟合优度', 'goodness-of-fit', 'chisquare'),
     'result = stats.chisquare(f_obs=observed_counts, f_exp=expected_counts)',
     'Goodness-of-fit: matching observed/expected totals and correct fitted-parameter degrees of freedom.'),
    ('fisher_exact', ('fisher', '精确检验'),
     'result = stats.fisher_exact(contingency_counts, alternative=alternative)',
     'Use the actual count table/direction. An odds ratio and a p value are different quantities.'),
    ('ttest_ind', ('welch', 't-test', 't检验', 't 检验', '独立样本'),
     'result = stats.ttest_ind(x, y, equal_var=False, alternative=alternative)',
     'Welch independent samples; set equal_var=True only for the intended equal-variance method.'),
    ('ttest_rel', ('ttest_rel', 'paired t-test', 'paired t test', '配对t', '配对 t'),
     'result = stats.ttest_rel(x_paired, y_paired, alternative=alternative)',
     'Match actual keys and paired missingness first. Row order alone does not establish pairing.'),
    ('ttest_1samp', ('单样本', 'one-sample', 't-test', 't检验', 't 检验'),
     'result = stats.ttest_1samp(x, popmean=null_value, alternative=alternative)',
     'Use the declared null value and actual independent observation unit.'),
    ('mannwhitneyu', ('mann', 'whitney', '秩和', '非参数'),
     'result = stats.mannwhitneyu(x, y, alternative=alternative, method=chosen_method)',
     'Independent groups. Choose exact/asymptotic/permutation for the actual ties and sample support.'),
    ('wilcoxon', ('wilcoxon', '符号秩'),
     'result = stats.wilcoxon(paired_differences, alternative=alternative, zero_method=zero_method)',
     'Paired signed ranks; handle zeros/ties explicitly and preserve subtraction order.'),
    ('f_oneway', ('anova', '方差分析'),
     'result = stats.f_oneway(*group_arrays)',
     'Classical independent-group one-way ANOVA is not repeated-measures or Welch ANOVA.'),
    ('kruskal', ('kruskal', '多组', '非参数'),
     'result = stats.kruskal(*group_arrays)',
     'Independent-group rank test. A rejection is not automatically a pure median shift.'),
    ('pearsonr', ('pearson', '相关', 'correlation'),
     'result = stats.pearsonr(x, y, alternative=alternative)',
     'Use matched variables and their scope/missingness. Linear association is not causation.'),
    ('spearmanr', ('spearman', '相关', 'correlation'),
     'result = stats.spearmanr(x, y, alternative=alternative)',
     'Rank association from actual variable pairs; do not invent intervals or independent repetitions.'),
    ('binomtest', ('二项', 'binomial', 'wilson', 'mcnemar', '精确检验'),
     'result = stats.binomtest(successes, n=trials, p=null_probability, alternative=alternative)\n'
     'wilson = result.proportion_ci(confidence_level=level, method="wilson")\n'
     'lower, upper = wilson.low, wilson.high',
     'Use integer successes/trials and the real denominator. Wilson is not Student-t. Exact McNemar uses '
     'discordant paired counts as binomial successes/total under p=0.5; handle zero discordant pairs explicitly.'),
    ('bootstrap', ('bootstrap', '重采样', '自助'),
     'result = stats.bootstrap((values,), statistic, confidence_level=level, n_resamples=resamples, '
     'method=interval_method, random_state=rng)',
     'Choose resampling unit and percentile/basic/BCa method. Preserve pairing; clusters/blocks/hierarchies '
     'need the corresponding resampling code instead of independent row resampling.'),
    ('linregress', ('回归', 'regression', '斜率', 'slope'),
     'result = stats.linregress(x, y, alternative=alternative)',
     'Univariate OLS with intercept and varying predictor. Multivariable OLS may use '
     'numpy.linalg.lstsq(design, response, rcond=None), with intended intercept/covariates explicitly prepared.'),
)


def reference_for(plan):
    """Use real installed signatures and only relevant examples, without a library dump."""
    import scipy
    from scipy import stats

    text = (' '.join(plan['questions']) + ' ' + plan['outline']).lower()
    catalog, selected = [], []
    for name, keywords, example, meaning in EXAMPLES:
        entry = dict(function='scipy.stats.' + name, signature=str(inspect.signature(getattr(stats, name))))
        catalog.append(entry)
        if any(keyword in text for keyword in keywords):
            selected.append(dict(**entry, example=example, meaning=meaning))
    return dict(scipy_version=scipy.__version__, catalog=catalog, examples=selected,
                usage='Import from scipy import stats. Example variables are placeholders for actual input; '
                      'adapt options to the plan/data and return plain JSON, not SciPy objects. '
                      'This is assistance, not a method whitelist; other installed/custom methods remain available.')
