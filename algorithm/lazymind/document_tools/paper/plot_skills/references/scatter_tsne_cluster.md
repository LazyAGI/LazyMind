# Display of supplied embedding coordinates

This style renders an existing two-dimensional embedding. It does not estimate an
embedding, cluster observations or generate coordinates. Select it only when the
input supplies the coordinates and their meaning. A measured latency/accuracy pair
belongs in ordinary scatter; the presence of two numeric columns does not make a
plot t-SNE.

## Required call

`scripts/scatter_tsne.py` provides:

```python
draw_embedding(ax, series, labels, colors, edges, markers, label_positions,
               title, xlabel, ylabel, xlim, ylim)
```

`series[label]` is an array of `(x, y)` rows for the named input group. Preserve
each coordinate pair. Rows missing either coordinate are omitted together;
infinite coordinates are invalid. Bind all appearance arrays by the same global
group identity used elsewhere in the study, even when the panel order changes.

Choose `xlim` and `ylim` with strict padding around every complete observation;
points exactly on a limit are rejected along with points outside it. The helper
uses equal axis scales to preserve the supplied coordinate geometry. Its returned
counts report valid plotted pairs per label, not inferred cluster sizes.

`label_positions` maps group names to display coordinates. An empty dictionary uses
each nonempty group's coordinate centroid. Place supplied label boxes beside a
group when its centroid would obscure important observations; moving a label must
not move the underlying points.

## Presentation and claims

Use the profile's 89 × 82 mm single-panel canvas and margins. The helper draws small
translucent points, outlined markers and direct label boxes on axes without a grid.
Those boxes usually make a repeated group legend unnecessary. Additional groups
retain their assigned blue shades and marker shapes instead of reusing the first
few colors by position.

Pass an empty `title` or a panel identifier. Return the figure title, coordinate
meaning and group definitions in metadata. Name the embedding algorithm only when
known; report source features, preprocessing and available hyperparameters or seed.
State the number of incomplete pairs and identify synthetic fixtures explicitly.
Do not infer original-space distances, group separation or model quality solely
from the appearance of a two-dimensional projection.
