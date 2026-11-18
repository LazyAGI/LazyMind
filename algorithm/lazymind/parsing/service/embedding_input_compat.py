"""Keep empty text segments out of provider requests without discarding stored text."""
from functools import wraps


def install():
    from lazyllm.tools.rag.store import document_store
    from lazyllm.tools.rag.doc_node import ImageDocNode, MetadataMode

    original = document_store.parallel_do_embedding
    if getattr(original, '_lazymind_nonempty_text', False):
        return

    @wraps(original)
    def embed_nonempty(embed, embed_keys, nodes, group_embed_keys=None):
        usable = [node for node in nodes if isinstance(node, ImageDocNode)
                  or node.get_text(MetadataMode.EMBED).strip()]
        return original(embed, embed_keys, usable, group_embed_keys)

    embed_nonempty._lazymind_nonempty_text = True
    document_store.parallel_do_embedding = embed_nonempty
