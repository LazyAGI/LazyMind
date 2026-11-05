# Additive stacked components and proportions

Call `paper_extended.py::draw_stacked(ax, values, categories, components, colors, normalized, title, ylabel)`.

`values` is category × component, complete and nonnegative, with a positive total in every category. Do not turn unknown components into zero; do not stack quantities that cannot be added in the same unit. For a normalized panel the helper divides each row by its own total once, and returns the displayed values. Never treat normalized percentages as raw measurements for another normalization.

Color identifies **component**, not the category/method on the x axis. Bind full input component order to `style_contract.component_colors` once, before panel filtering/sorting, then look up colors by component name in every panel. Do not zip a longer data list with a shorter palette. The approved first three are #0072B2 (blue), #E69F00 (orange), #009E73 (green); additional approved colors are in the contract. Each BarContainer must be labeled with its real component.

The renderer preserves this native component palette and rejects off-palette or inconsistent component colors. Ordinary method bars and custom bars retain the method identity palette. Use no focal-method hatches on additive components. The 182×78 mm paired absolute/proportion layout reserves top=0.78 and one shared component legend. Caption the denominator and rounding, and distinguish absolute totals from proportions.
