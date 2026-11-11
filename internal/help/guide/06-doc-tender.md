# Doc Tender

## Doc Tender {#doc-tender}
<!-- template: doctender/review.html, doctender/guide.html, doctender/admin.html | path: /doc-tender/review, /doc-tender/guide, /admin/doc-tender | where: Sidebar > Documents > Doc Tender (admins and attorneys) | keywords: review queue, file processing, naming, categories, markdown copy, exceptions, ocr, transcript -->
Doc Tender processes every file that lands in a matter as it arrives: staff and client uploads, the editor, filed mail, recordings, importers, the drop folder and module files. Nothing waits for a nightly batch. A file on a closed matter is listed as Not processed (matter closed) and costs nothing until the matter reopens. Until the AI tab has a provider key, files are stored and listed and their jobs wait.

### What it does with each file type
- Text, Markdown and HTML are read as they are.
- Word and other documents (DOCX, DOC, ODT, RTF) are converted and checked against the document's own word and page counts.
- Spreadsheets (XLSX and CSV) become one table per sheet, checked by row and column counts.
- PDFs are read page by page. A text layer is checked against an OCR read of the same page; a scanned page is checked against a second read by Tesseract. A failure resumes at the page that failed, never at page 1.
- Photos are read and described. Photos of one multi-page document are grouped once the matter has had no new photos for 10 minutes, into one PDF with the photos as its pages; the photos stay linked as its sources.
- Audio gets a transcript with speakers and timestamps, checked for gaps. Video adds what is on screen, one frame a second, on the same timeline.
- Anything else is flagged as unsupported and stays available.

Every processed file gets a Markdown copy, kept for good next to the original with the same name and the .md extension. Search and modules read it. Then Doc Tender labels the file (what it is, its date, a summary and the key names, dates, amounts and case numbers, each cited to its page or timestamp), picks one category and files it into that category's folder. A category never sets a deadline or sends anything.

### How names are made
Each file gets one name from the naming pattern, by default client last name, matter number, what it is and the document's own date, lowercase with hyphens: smith-2026-0042-motion-to-withdraw-2026-10-15.docx. The date is left off when the document has none. A clash in the same matter adds -2, -3. Status never goes in a name. Doc Tender names a file once; a name, category, matter or grouping a person sets is locked and Doc Tender never changes it.

### The review queue and how to clear it
Only exceptions reach the queue: low confidence (below 0.90 by default) on a name part, category or matter; check models that disagree; a file it can't match to a matter, or that looks like it belongs to another; an extraction that failed after its retries; a password-protected, damaged or unsupported file; or a provider down for an hour. The sidebar's Doc Tender entry shows the live count of open items you can see. Every file in the queue stays listed, viewable and downloadable with its flag.

Open an item with **Review**. It shows the file, what's wrong and Doc Tender's best answer. Clear it with **Accept** (take the best answer), **Correct** (give the right name, category or grouping), **Assign matter**, **Retry** (run the failed step again) or **Keep as is** (the file stays as it is with its flag). Doc Tender never moves a file between matters on its own.

### Where its settings live
The Doc Tender tab ([Administrative > Doc Tender](/admin/doc-tender), admins only) holds the naming pattern, the categories with their folders and tiers, the review confidence, the quiet time before photos group, the retry schedule, who gets review emails, the daily summary and spend by day, matter and file type. Its models are on the AI tab. The Files tab on a matter shows each file's Doc Tender status.

## Doc Tender review item {#doc-tender-item}
<!-- template: doctender/item.html | path: /doc-tender/review/{exception_id} | where: Sidebar > Documents > Doc Tender > Review | keywords: accept, correct, assign matter, retry, keep as is, best answer, prompt injection -->
One review queue item: the file, what's wrong, any other flags on it, and Doc Tender's best answer. Choose Accept, Correct, Assign matter, Retry or Keep as is and save; the file stays downloadable the whole time. Text that tried to instruct an AI model is quoted here only and never followed. See [Doc Tender](/help/topics/doc-tender) for what each exception means.
