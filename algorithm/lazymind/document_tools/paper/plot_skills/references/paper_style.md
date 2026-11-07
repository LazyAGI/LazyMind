# Shared paper appearance

Use the supplied paper_profile and relevant helper source for fonts, palette and
starting dimensions. Single-column figures start near 89 mm wide; double-column
figures near 182 mm. Reflow panels or choose a suitable height for the actual labels.
Do not hide scientific distinctions to fit a fixed template.

All text inside figures is English. External captions and analysis follow the paper
language. Generate labels from scientific meaning; share terminology, series order
and color identity across figures. Raw result IDs and filter expressions are provenance.
Use short names with necessary units and explain abbreviations in the caption.
Ticks identify categories briefly. Give shared factor definitions and uniform sample
support once in headings, a legend or the caption, rather than repeating full descriptions
and the same n on every row. Multifactor conditions can use short single-line level
combinations, group headings, aligned factor columns or facets; retain distinct conditions.
Show per-group sample sizes when they differ or matter. Wrapping is a finishing adjustment,
not a substitute for this hierarchy; do not truncate words or remove comparison direction.
Use brief axis titles. Lengthy factor-order keys, metric definitions and derived-metric
relationships belong in a horizontal key or caption, not a vertical title or category tick.
Omit redundant dimensionless 0-1 suffixes without rescaling the underlying values.

The application applies a Unicode-capable STIX font chain at export, preserving size,
style and weight. Leave font families and rcParams to the host; emphasize with fontweight.
Choose spacing and legend placement in the drawing program. Export can wrap long x ticks
and fit genuine subplots around external notes/legends without changing words or data.
The application does not construct scientific labels, add observations or overwrite data artists.
Keep overall titles and long captions outside the canvas. Subplot identifiers, axis
names, units and scientific annotations remain inside.

Use subtle grids, open top/right spines where appropriate, and thin visible outlines.
Color and marker shape identify the same group consistently, never significance.
Quantitative matrices need their actual numeric scale and a colorbar; signed effects
use a zero-centered diverging palette when appropriate. Distinguish absent cells from zero.
Nonnegative bars start at zero. Difference figures show a zero reference and full
subtraction direction. Use supplied endpoints exactly; absent intervals stay absent.

Preserve original observations, pairing, scales and adverse outcomes. Do not manufacture
uncertainty, extrapolate an unobserved trajectory or imply relationships from unmatched scopes.
Source intervals do not establish error bars unless actually drawn.

Native functions are reusable helpers, not mandatory geometry. Call their actual signatures
when applicable, or use Matplotlib for a required layout with the same shared appearance.
Give the figure enough room for readable labels and legends. Identify every series with a
legend or direct label; keep legend handles and labels paired. Return an accurate external
caption describing how to read the drawing, sample/interval meaning and simulation disclosure.
In dense plots, avoid redundant numeric labels on individual points. Keep necessary summary
annotations clear of data artists, or leave exact values to the accompanying analysis.
Numeric contrasts and substantive conclusions belong in the single separate figure analysis.
The renderer saves vector SVG/PDF and 300-DPI PNG; successful export does not certify journal acceptance.
