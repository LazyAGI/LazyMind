# Raw distribution with common-bin histograms

Call `paper_extended.py::draw_histogram(ax, series, methods, colors, edges, bins, title, xlabel, ylabel)`.

`series` holds raw observations by method. Each method may have a different valid count. None/NaN is omitted and disclosed; infinity is rejected. Bin edges are finite, strictly increasing and shared across methods, covering every finite observation. The helper returns raw bin counts for verification and draws density-normalized step/fill shapes, without smoothing. State reciprocal units on the y axis, e.g. probability density (1/ms).

Read colors/edges by global method identity before sorting. Keep pale fills and darker thin boundaries. A complementary ECDF panel may use the same observations, but is optional. In the reviewed 182×78 mm pairing, both use the same full x range and compact in-axes legends. An empty bin is a measured zero count; an entirely missing group is not a zero-valued observation.
