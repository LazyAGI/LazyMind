# Count-valued hexagonal density bins

Call `paper_extended.py::draw_hexbin(ax, x, y, title, xlabel, ylabel, xlim, ylim, gridsize, palette)` with palette='viridis', then `draw_colorbar` on a separate explicit axis.

Coordinates are raw paired observations. Omit only incomplete pairs and disclose their count; reject infinity and any view that crops a finite pair. Each occupied hexagon contains a count, empty bins stay blank. The helper verifies total bin counts equal the finite pair count. Do not label this as a KDE or manufacture a surface between observations. Choose grid resolution for density at the final size, not to suggest unsupported clusters.

`field_layout` supplies the reviewed 182×82 mm data/colorbar positions when density and a separate grid-response question form complementary panels. For a single density plot allocate the same readable axis/colorbar separation. State coordinate units and label the colorbar samples/bin. All meshes/colorbars must remain vector.
