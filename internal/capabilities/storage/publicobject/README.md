# Storage public-object reader

This provider-owned seam opens one confirmed local object after a product has
made its own request-time authorization decision. It exposes a seekable stream,
canonical MIME, detected MIME, and size; it never exposes the stored object key
or filesystem path.

The PostgreSQL lookup rechecks confirmation and retention state. The local
reader rejects path traversal, symlink escape, non-regular objects, metadata
size drift, HTML, and SVG. The package has no dependency on Storage HTTP
handlers, central middleware, eventbus, cache, or Redis.
