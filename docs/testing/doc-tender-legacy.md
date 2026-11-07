# Doc Tender legacy failures

PRD section 11 says the Doc Tender corpus is generated, with no client data, and includes a stand-in for each way the old doc-tender failed: an old `.doc` file, a 120-page scanned PDF that fails partway, a page set that was merged and then split, and a password-protected PDF.

`tests/harness/corpus.py` builds those four. `python3 -c "from tests.harness.corpus import write_corpus; from pathlib import Path; write_corpus(Path('tmp/corpus'))"` writes them under `tmp/corpus`. That directory is generated. It is not committed.

| Legacy id | Corpus path | Acceptance case | What the file is |
| --- | --- | --- | --- |
| `old_doc` | `legacy/old.doc` | AC-043-P3, AC-043-F4 | OLE magic, a v3 header, and a `WordDocument` stream name. Not a document wordcraft-cli can open. The evidence gate refuses this hash. |
| `scanned_pdf_page_49` | `legacy/scanned-120.pdf` | AC-044-P1, AC-044-F1, AC-044-F2 | 120 image-only pages, no text layer. Manifest sets the injected failure and the resume point at page 49. Pages 1 through 48 must not be redone. |
| `merged_then_split` | `legacy/merged-then-split/page-01.png` through `page-08.png` | AC-045-P1, AC-045-F1, AC-045-F2, AC-045-F3 | Eight photos. Pages 1-3 paint LETTER and PAGE n OF 3. Pages 4-8 paint LEASE and PAGE n OF 5. Expected groups are those two documents, in that order. `bad-merge.pdf` is one 8-page file. `bad-split-a.pdf` and `bad-split-b.pdf` are 4 and 4. Those wrong outputs are not a pass. |
| `password_pdf` | `legacy/password.pdf` | AC-048-P1, AC-048-F3 | Trailer points at a Standard encrypt dictionary. Streams are still plaintext. The evidence gate refuses this hash. AC-048-P1 needs a PDF the product library reports as encrypted. |

The same builder also writes the damaged PDF, the low-confidence score fixture (890 thousandths, threshold 900), a 1-second 440 Hz WAV with no speech, a 3-frame uncompressed AVI (red, green, blue), and the four arrival files for AC-043: staff upload, client upload, drop folder, and sample module.

None of these files accept AC-043 through AC-052. Those criteria stay pending until Doc Tender, files, and the editor run against the corpus and record command evidence.
