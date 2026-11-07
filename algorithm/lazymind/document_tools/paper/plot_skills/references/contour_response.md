# Response contours on an actual grid

Call `paper_extended.py::draw_contour(ax, x, y, values, levels, line_levels, title, xlabel, ylabel, palette)` with palette='viridis', then `draw_colorbar` on a separate axis.

x and y are finite, strictly increasing sampled coordinates. values is a complete finite (len(y),len(x)) grid; missing grid cells must not be filled or inferred. Contour levels enclose all observed responses. The helper shows all sampled grid nodes and interpolates contours only inside that domain, with no extrapolation. Disclose this interpolation and any actual log-coordinate transform; do not relabel arbitrary axes as parameters.

Use contouring only for a genuine response grid; ordinary paired scatter belongs in scatter/hexbin. `field_layout` gives the reviewed 182×82 mm paired layout and separate colorbar axes. A colorbar describes response units rather than method identity. All paths and the colorbar remain vector.
