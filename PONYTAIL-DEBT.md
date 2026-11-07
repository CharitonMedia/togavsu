# Simplification debt

The application has not been built. Product acceptance is pending.
These entries are harness stand-ins. They are not product-ready behavior.
Each entry names the file, the ceiling, the upgrade path, and a concrete trigger.

## tests/harness/corpus.py legacy/old.doc

ponytail: ceiling: OLE magic, a v3 header, and the WordDocument stream name. Word and wordcraft-cli are not expected to open it. Upgrade path: replace these bytes with a document wordcraft-cli opens at the pinned LawCraft commit, then set product_ready true. trigger: The doc-tender or editor package can shell out to wordcraft-cli and this file is rejected as an unreadable .doc rather than converted and checked.

Not product-ready. `product_ready` stays false and the file hash is refused as acceptance evidence.

## tests/harness/corpus.py legacy/password.pdf

ponytail: ceiling: The trailer points at a Standard encrypt dictionary. The streams are still plaintext, so a reader that ignores /Encrypt will open the page. Upgrade path: write a real Standard-security encrypted PDF once the product PDF library reports encryption, then set product_ready true. trigger: The doc-tender package opens this file as an ordinary one-page PDF instead of an encrypted file.

Not product-ready. `product_ready` stays false and the file hash is refused as acceptance evidence.
