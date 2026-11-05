"""API grounding captured from the pinned Matplotlib 3.10.8 plotting image."""

SIGNATURES = {
    'bar': "(self, x, height, width=0.8, bottom=None, *, align='center', data=None, **kwargs)",
    'barh': "(self, y, width, height=0.8, left=None, *, align='center', data=None, **kwargs)",
    'boxplot': "(self, x, *, notch=None, sym=None, vert=None, orientation='vertical', whis=None, positions=None, "
               'widths=None, patch_artist=None, bootstrap=None, usermedians=None, conf_intervals=None, meanline=None, '
               'showmeans=None, showcaps=None, showbox=None, showfliers=None, boxprops=None, tick_labels=None, '
               'flierprops=None, medianprops=None, meanprops=None, capprops=None, whiskerprops=None, manage_ticks=True, '
               'autorange=False, zorder=None, capwidths=None, label=None, data=None)',
    'violinplot': "(self, dataset, positions=None, *, vert=None, orientation='vertical', widths=0.5, showmeans=False, "
                  "showextrema=True, showmedians=False, quantiles=None, points=100, bw_method=None, side='both', "
                  'data=None)',
    'scatter': '(self, x, y, s=None, c=None, *, marker=None, cmap=None, norm=None, vmin=None, vmax=None, alpha=None, '
               'linewidths=None, edgecolors=None, colorizer=None, plotnonfinite=False, data=None, **kwargs)',
    'plot': '(self, *args, scalex=True, scaley=True, data=None, **kwargs)',
    'errorbar': "(self, x, y, yerr=None, xerr=None, fmt='', *, ecolor=None, elinewidth=None, capsize=None, "
                'barsabove=False, lolims=False, uplims=False, xlolims=False, xuplims=False, errorevery=1, '
                'capthick=None, data=None, **kwargs)',
    'fill_between': '(self, x, y1, y2=0, where=None, interpolate=False, step=None, *, data=None, **kwargs)',
    'pcolormesh': '(self, *args, alpha=None, norm=None, cmap=None, vmin=None, vmax=None, colorizer=None, shading=None, '
                  'antialiased=False, data=None, **kwargs)',
    'hist': '(self, x, bins=None, *, range=None, density=False, weights=None, cumulative=False, bottom=None, '
            "histtype='bar', align='mid', orientation='vertical', rwidth=None, log=False, color=None, label=None, "
            'stacked=False, data=None, **kwargs)',
}


def reference(spec: dict) -> dict:
    kind = (spec['kind'] + ' ' + spec['style']).lower()
    selected = {'plot', 'scatter', 'errorbar'}
    families = {'bar': {'bar', 'barh'}, 'box': {'boxplot'}, 'violin': {'violinplot'},
                'line': {'fill_between'}, 'heat': {'pcolormesh'}, 'hist': {'hist'}}
    for token, methods in families.items():
        if token in kind:
            selected.update(methods)
    if not any(token in kind for token in (*families, 'scatter', 'radar')):
        selected.update(SIGNATURES)
    result = {'matplotlib_version': '3.10.8',
              'Axes_signatures': {name: SIGNATURES[name] for name in SIGNATURES if name in selected},
              'usage': 'Call methods on ax; self is implicit. Use only parameters supported by that exact method. '
                       'Do not transfer options between chart types. Artist styling goes through the artist setters.'}
    result['recipes'] = {name: source for name, source in RECIPES.items() if name in selected}
    result['native_contract_paths'] = {
        'layout': "spec['style_contract']['layout']",
        'legend_anchor': "spec['style_contract']['layout']['legend_anchor']",
        'template_name': "spec['style_contract']['native_template']",
    }
    result['text_api'] = {
        'position': 'Matplotlib Text uses get_position() -> (x,y), set_position((x,y)), set_x(x), set_y(y). '
                    'Text has NO get_x() or get_y(); those getters belong to Rectangle patches. '
                    'Read text coordinates with text.get_position()[0/1].',
    }
    result['legend_api'] = {
        'bar_handles': 'ax.bar returns BarContainer, not a Patch. Use container.get_label() and container.patches. '
                       'Never call container.set_linewidth().',
        'shared_legend': 'handles, labels = ax.get_legend_handles_labels(); legend = fig.legend(handles, labels)',
        'bar_key_border': 'for patch in legend.get_patches(): patch.set_linewidth(1.0)',
        'version_note': 'Matplotlib 3.10.8 uses legend.legend_handles, not legend.legendHandles. '
                        'For bar key borders use legend.get_patches(), not the original container handles.',
    }
    if 'heat' in kind:
        result['native_heatmap_api'] = {
            'palette_type': 'str; style_contract.colormaps[mode] is a list of allowed names, not one palette',
            'palette_example': "palette = spec['style_contract']['colormaps'][mode][0]",
            'signed_modes': ['difference', 'correlation', 'correlation_pairs'],
        }
    return result


# Small, data-free calls exercised in the pinned Docker image by integration tests.
# These are API examples, not complete figures or a fixed rendering fallback.
RECIPES = {
    'boxplot': 'boxes = ax.boxplot(arrays, positions=positions, widths=0.5, patch_artist=True, '
               "showfliers=False, medianprops={'color': 'black', 'linewidth': 1.0})",
    'bar': "bars = ax.bar(positions, means, width=0.55, color=colors, edgecolor='white', linewidth=0.8)",
    'barh': "bars = ax.barh(positions, means, height=0.55, color=colors, edgecolor='white', linewidth=0.8)",
    'scatter': "points = ax.scatter(x, y, s=16, color='black', zorder=3)",
    'errorbar': "errors = ax.errorbar(positions, means, yerr=errors_from_mean, fmt='none', "
                "ecolor='black', capsize=3, elinewidth=0.9)",
    'plot': "lines = ax.plot(x, y, linewidth=1.5, marker='o', markersize=4)",
}
