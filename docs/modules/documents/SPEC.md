# Documents module specification

Module id `documents`. PRD 9.8 item 1: documents and templates, document assembly and generation, bulk download and extra versioning tools. This spec is complete: every decision is made here, and a build lane runs it start to finish.

**Where this module builds (Nov 14, 2026).** This module builds in its own repository, CharitonMedia/togavsu-documents (modules PRD section 2), not in the core repository. Read each path below under `modules/documents/` as the same path at that repository's root, `cmd/documents/` as its `cmd/`, and `internal/modules/module_documents_pg_test.go` as that repository's Postgres acceptance suite. The suite runs on `pkg/modulekit/coretest` from the public module kit (modules PRD section 5), whose counterparts of the harness names used below (`newEnv`, `e.rt.handlers`, `e.rt.last`, `approveAll`, `contextFor`, `moduleDB`) are listed in docs/modules/MODULE-KIT.md. Packaging and signing go through `pkg/modulekit`, never `internal/marketplace`. "Lane" below means this module's build in its repository. The repository's go.mod requires the core module for the kit and only dependencies the core's go.mod pins. The spec itself stays here, at docs/modules/documents/SPEC.md.

## 1. Purpose and scope

The module turns the firm's own templates into finished drafts on a matter, generates whole document packages in one step, downloads a matter's files as one zip, and adds version notes, compare and copy tools on top of the core's file versions.

It builds on the core and owns nothing the core already owns (PRD 9.8 item 1, 9.9, 8.19 to 8.21):

- **Files.** Every template body, component body and generated output is a core file record with core versions. The module keeps no file bytes in its schema or on its volume; it holds working copies in memory or a temp folder for one request and drops them when the request ends.
- **Editor.** Template bodies and generated drafts open in LawCraft through the core editor (PRD 8.20). The module ships no editor. Its token fill is a mechanical text replacement in a DOCX's XML, not an editing surface.
- **Doc Tender.** Generated drafts go through Doc Tender like any other new file: it names, categorizes and files them (PRD 8.21). The module never renames a file after publishing it and never fights Doc Tender's folder choice. Template library files are kept out of Doc Tender by the core change CC-1.
- **Access.** Every core call carries the viewer's context token, so walls, roles and attorney-only rules come from the core. Module rows are shown only for core records the API returns to that viewer.

## 2. What Unified does today and how it carries over

Unified's working approach is kept and written clean on Togavsu's records. Built fresh means writing it clean; it doesn't mean throwing out a working idea.

| # | Unified behavior | Unified source | Carries over as |
| --- | --- | --- | --- |
| U1 | Template library: name, subject area, document class, topic tags, description, active or archived; search by name, area and topic | `src/lib/documents/templates-store.ts` (`listTemplates`, `listSubjectAreas`), `src/app/documents/templates/page.tsx`, `src/app/api/documents/templates/route.ts` | Same fields in `mod_documents.templates`. Subject area becomes links to core practice areas. Same search. |
| U2 | Reusable components (caption, signature block, certificate of service, letterhead) stitched into a template by an ordered `structure` of slots | `templates-store.ts` (`listComponents`, `assembleTemplatePreview`, `assembleTemplateHtml`), `src/app/documents/templates/components/*`, `src/app/api/documents/components/*` | Components are DOCX library files. A slot is a paragraph holding only `{{slot:name}}` in the template body; `template_slots` maps the name to a component. |
| U3 | Body edits cut an immutable version; metadata edits don't; read-only history and version view | `PRD-template-edit.md`, `build-log-template-edit.md`, `updateTemplate`, `listTemplateVersions`, `src/app/documents/templates/[id]/versions/[v]/page.tsx` | Each LawCraft save of the body adds a core file version. History comes from the core's version list. Metadata edits touch only module rows. Restore is the core's. |
| U4 | Field vocabulary: a token is a field name, qualified by source (matter, client, opposing party, served party, firm, attorney, document and date) | `src/lib/documents/field-vocabulary.ts` | Same token names and groups, mapped to Togavsu records (section 7.3). `matter_internal_number` becomes `matter_number`. |
| U5 | Token resolution: client from the client party, address of record from the current address history row, attorney from the lead attorney, long dates | `src/lib/documents/resolve-tokens.ts` (`resolveTokensForMatter`, `applyTokens`, `remainingTokens`) | Same rules through the core API: primary client party, current primary address, responsible attorney, firm time zone. |
| U6 | Assemble preview, edit the body, file it on the matter as a PDF with letter, pleading or estate styling and the firm footer | `src/app/api/matters/[id]/documents/assemble/route.ts`, `.../from-template/route.ts`, `src/app/matters/[id]/documents/new-from-template/page.tsx`, `standalone-doc.ts`, `render-pdf.ts`, `doc-scope.ts`, `footer-template.ts` | Assemble resolves tokens and asks for the rest in a form. Output is a DOCX draft on the matter that opens in LawCraft for any further edit. Styling and the firm footer live in the template DOCX itself (styles, header, footer with firm tokens). PDF comes from the core conversion. |
| U7 | DocForge package generation: whole package or nothing, a strict undefined check so no placeholder ships, a named error report, new files every run, drafts only, unsupported family refused | `PRD-generate-documents.md`, `build-log-generate-documents.md`, `qc-stage-4-generate-documents.md` | Packages: an ordered set of templates generated in one run. All of it publishes in one core transaction (`bulkFile`) or none of it does. A run never ships an unresolved token. Every run makes new files with status draft. |
| U8 | Finalize and revert to draft for generated files | `src/app/api/documents/[id]/finalize/route.ts`, `.../revert-draft/route.ts` | Mark final and Mark draft buttons on the Drafts tab call the core status write. The status is on the record, never in the name. |
| U9 | Closing letter writes a timeline event | `from-template/route.ts` (`closing_letter_generated`) | Every successful run writes one timeline event on the matter. |
| U10 | Generated file naming with `-2`, `-3` on a clash | `src/lib/documents/file-generated-document.ts` | The module proposes `<template-slug>-<YYYY-MM-DD>.docx` as the arrival name; the core adds the clash suffix and Doc Tender sets the final name. |
| U11 | Bulk download: selection, folder, no cap, streamed, entry names deduped, zip named by matter and date, nothing-to-download message, frozen manifest per bulk, one bulk line on the timeline | `PRD-matter-docs-bulk-download.md`, `src/lib/documents/bulk-download.ts`, `src/app/api/matters/[id]/documents/download-zip/route.ts`, `build-log-bulk-download.md`, `recon-report-bulk-download.md` | Whole matter, folder or selection, filtered by status or category, current or all versions, optional Markdown copies, folder paths in the zip. The core streams the bytes (CC-5); the module keeps the manifest and writes one summary line. |
| U12 | Per-document access history | `src/app/documents/[id]/view/AccessHistoryPanel.tsx`, `src/app/api/documents/[id]/access-history/route.ts` | Already core: every download is in the file's history and audit log (PRD 8.19). The module adds nothing here. |
| U13 | Editor version list and restore | `src/lib/documents/editor-versions.ts`, `src/app/api/documents/[id]/restore-version/route.ts` | Core. The module adds notes and labels, compare, and copy as new file (section 7.8). |

Not carried here: page split and merge (`src/lib/page-assembly.ts`) belongs to Doc Tender's review work; Quick Parts (`quick-part-html.ts`) belong to the editor; questionnaire serializers and the DocForge engine call (`PLAN-family-serializers.md`, `Plan-gun-trust-serializer.md`) belong to the intake module and DocForge; the client's "Download all" belongs to the client portal module. Section 10 lists each.

## 3. Manifest

`modules/documents/package/togavsu-module.yaml`:

```yaml
id: documents
name: Documents and templates
version: "1.0.0"
author: Togavsu
homepage: https://github.com/CharitonMedia/togavsu/tree/main/modules/documents
license: Same as Togavsu
price: Included
container:
  image: ghcr.io/charitonmedia/togavsu-documents
  digest: sha256:<digest of the built image, set before signing>
  port: 8080
  events: /events
permissions:
  - scope: matters:read
    reason: Read the matter, its parties and its custom fields to fill template tokens.
  - scope: contacts:read
    reason: Read party names and current addresses to fill template tokens.
  - scope: files:read
    reason: Read template bodies, list a matter's files and versions, download versions and Markdown copies for zips and compare.
  - scope: files:write
    reason: Store templates as library files, publish generated drafts, mark draft or final, add version copies.
  - scope: settings:read
    reason: Read county names, practice areas, party roles and custom field definitions for tokens and filters.
  - scope: firm:read
    reason: Fill firm name, address, phone, email and website tokens, and dates in the firm's time zone.
  - scope: users:directory
    reason: Fill the responsible attorney's name and list attorneys for attorney profiles.
  - scope: events:read
    reason: Hear when a generated or template file changes status or goes to the trash.
  - scope: events:write
    reason: Write one timeline line per generation run and per bulk download.
  - scope: editor:run
    required: false
    reason: Make a PDF beside a generated draft. Without it, output is DOCX only.
  - scope: doc_tender:read
    required: false
    reason: Offer a category hint per template and show Doc Tender status on generated drafts.
widgets:
  - id: drafts
    title: Generated drafts
    path: /widget/drafts
    size:
      w: 4
      h: 3
    sizes:
      - w: 4
        h: 3
      - w: 6
        h: 4
    roles: [admin, attorney, staff]
    target:
      tab: drafts
      params: [matter_id]
tabs:
  - id: templates
    title: Templates
    placement: home
    path: /templates
  - id: drafts
    title: Generated drafts
    placement: home
    path: /drafts
  - id: matter
    title: Templates and downloads
    placement: matter
    path: /matter
matter_items:
  - id: new-from-template
    kind: action
    title: New from template
    path: /matter/new
  - id: download-all
    kind: action
    title: Download files as zip
    path: /matter/download
  - id: version-tools
    kind: action
    title: Version tools
    path: /matter/versions
  - id: recent-generated
    kind: panel
    title: Recently generated
    path: /matter/recent
sidebar:
  - id: templates
    title: Templates
    path: /templates
    section: documents
    icon: file
    order: 200
quick_create:
  - id: from-template
    title: Document from template
    path: /quick/new
search_scopes:
  - id: templates
    title: Templates
    path: /search
help:
  heading: Documents and templates
  pages:
    - id: overview
      title: Documents and templates
      keywords: [template, assemble, generate]
      covers: [/modules/documents/tabs/templates]
      body: |
        Keep the firm's templates and components in one library and generate filled drafts on any matter.
    - id: tokens
      title: Template tokens and slots
      keywords: [token, merge field, slot, component]
      covers: [/modules/documents/tabs/templates]
      body: |
        Tokens such as {{client_full_name}} fill from the matter, its parties and the firm; slots pull in a shared component.
    - id: generate
      title: Generate a document or package
      keywords: [generate, package, draft]
      covers: [/modules/documents/tabs/matter, documents.drafts, /modules/documents/tabs/drafts]
      body: |
        Pick one template or a package on the matter, review the filled values, and generate drafts into the matter's files.
    - id: zip
      title: Download files as a zip
      keywords: [zip, bulk, download]
      covers: [/modules/documents/tabs/matter]
      body: |
        Download a matter's files, or a folder of them, as one zip, in folders or flat.
    - id: versions
      title: Version notes, compare and copy
      keywords: [version, compare, diff, note]
      covers: [/modules/documents/tabs/matter]
      body: |
        Add a note to a version, compare two versions side by side, or copy an older version back as the newest.
    - id: attorneys
      title: Attorney profiles
      keywords: [bar number, signature]
      covers: [/modules/documents/tabs/templates]
      body: |
        Admins keep each attorney's bar number, bar state, courtesy title and signature line for signature blocks.
events: [file.updated, file.trashed]
data:
  extends:
    - name: generated
      record: file
      table: generated_files
      key: file_id
      columns:
        - name: template_id
        - name: run_id
        - name: template_version_number
    - name: attorney
      record: user
      table: attorney_profiles
      key: user_id
      columns:
        - name: bar_number
        - name: bar_state
        - name: courtesy_title
        - name: signature_line
  views:
    - name: generated_documents
      record: file
      key: file_id
      columns:
        - name: template_id
        - name: template_name
        - name: run_id
        - name: template_version_number
settings:
  - key: default_output
    label: Output for new templates
    type: enum
    options: [docx, docx_and_pdf]
    default: docx
  - key: max_package_templates
    label: Most templates in one package
    type: int
    default: "25"
  - key: zip_layout
    label: Zip layout
    type: enum
    options: [folders, flat]
    default: folders
  - key: drafts_window_days
    label: Days of generated drafts the widget shows
    type: int
    default: "30"
```

Every widget, tab and matter item is served by the module under `/m/documents/` in the core's sandboxed frame (PRD 9.4). The widget's click-through opens the `drafts` tab filtered to the clicked row's matter. Roles: every page answers 404 to accountant and client viewers. Attorney profiles and Release (section 7.10) are admin only.

## 4. Data

Schema `mod_documents`, created by the core at install. Ids are UUIDv7 text, as core ids are. Every foreign key to a core record is `REFERENCES core.<table> (id) ON DELETE RESTRICT`. No table copies a core record: template bodies and outputs are core files, and parties, users and matters are links. Timestamps are `BIGINT` unix seconds like matter-clock. No column holds a token value typed by a person.

**0001_library.sql**

- `templates`: `id` PK; `slug` unique; `name`; `doc_class` (`pleading`, `letter`, `estate`, `other`); `topics TEXT[]`; `description`; `body_file_id` NOT NULL unique, FK core.files; `output` (`docx`, `docx_and_pdf`); `attorney_only BOOLEAN` (outputs inherit it); `category_id TEXT NULL` (a Doc Tender category id, a hint only; categories aren't a linkable core record); `status` (`active`, `archived`); `created_by` FK core.users; `created_at`; `updated_at`.
- `template_practice_areas`: `template_id` FK templates, `practice_area_id` FK core.practice_areas; PK both. No rows means every practice area.
- `components`: `id` PK; `slug` unique; `name`; `category` (`caption`, `signature`, `certificate`, `letterhead`, `other`); `description`; `body_file_id` NOT NULL unique, FK core.files; `status`; `created_by` FK core.users; `created_at`; `updated_at`.
- `template_slots`: `template_id` FK templates, `slot` (lower-case name), `component_id` FK components; PK (`template_id`, `slot`).
- `packages`: `id` PK; `name`; `description`; `status`; `created_by` FK core.users; `created_at`; `updated_at`.
- `package_practice_areas`: as `template_practice_areas`, for packages.
- `package_templates`: `package_id` FK packages, `position INTEGER`, `template_id` FK templates; PK (`package_id`, `position`); unique (`package_id`, `template_id`).

**0002_generation.sql**

- `generation_runs`: `id` PK; `matter_id` FK core.matters; `package_id` NULL FK packages; `template_id` NULL FK templates; CHECK exactly one of the two; `actor_user_id` FK core.users; `principal_contact_id` NULL FK core.contacts; `outcome` (`succeeded`, `failed`, `pdf_failed`); `error_report JSONB` (template ids, token names, part names and reasons only); `manual_tokens TEXT[]` (names of tokens a person filled, never the values); `file_count`; `started_at`; `finished_at`.
- `generated_files` (extension of file): `file_id` PK FK core.files; `run_id` FK generation_runs; `template_id` FK templates; `template_version_number INTEGER`; `component_versions JSONB` (slot to component version number); `pdf_file_id TEXT NULL` FK core.files; `status_cache` (last status the core reported); `trashed BOOLEAN`; `updated_at`.

**0003_downloads.sql**

- `bulk_downloads`: `id` PK; `matter_id` FK core.matters; `actor_user_id` FK core.users; `scope` (`matter`, `folder`, `selection`); `folder_label TEXT` (the folder's name at the time, for the record); `status_filter`, `category_filter` (text, empty for any); `versions` (`current`, `all`); `with_markdown BOOLEAN`; `layout`; `zip_name`; `entry_count`; `byte_count BIGINT`; `status` (`started`, `complete`, `interrupted`); `started_at`; `finished_at`.
- `bulk_download_items`: `bulk_id` FK bulk_downloads; `position`; `file_id TEXT NULL` FK core.files (set to NULL only by Release); `version_number`; `kind` (`version`, `markdown`); `entry_path`; `sha256`; `size_bytes`; PK (`bulk_id`, `position`). This is the frozen manifest.

**0004_versions.sql**

- `version_notes`: `file_id` FK core.files; `version_number`; `label` (`sent`, `signed`, `filed`, `reviewed`, `other`, or empty); `note TEXT`; `updated_by` FK core.users; `updated_at`; PK (`file_id`, `version_number`).
- `version_copies`: `file_id` PK FK core.files (the new file); `source_file_id` FK core.files; `source_version_number`; `created_by` FK core.users; `created_at`.

**0005_profiles_events.sql**

- `attorney_profiles` (extension of user): `user_id` PK FK core.users; `bar_number`; `bar_state` (two letters); `courtesy_title` (typed by the admin, never inferred); `signature_line`; `updated_at`.
- `received_events`: `event_id` PK; `action`; `record_id`; `received_at`. Each delivered event is handled once.

**0006_views.sql**

- View `generated_documents`: `generated_files` joined to `templates`, columns `file_id`, `template_id`, `template_name`, `run_id`, `template_version_number`. Published for other modules (the signing module reads it to find generated fee agreements) under `views:documents.generated_documents:read`.

Extension tables `generated` (file) and `attorney` (user) are read through `include=`, for example `GET /api/v1/files?matter_id={id}&include=documents.generated`, so the core applies walls and roles to module rows. The module declares no custom fields.

## 5. Core API calls

Every call sends the module token and, for anything a viewer triggers, `X-Togavsu-Context-Token`. Names are the OpenAPI operation ids; entries marked CC need section 6.

| Method and path | Operation | Scope | Used for |
| --- | --- | --- | --- |
| GET /api/v1/matters | listMatter | matters:read | Quick-create matter picker |
| GET /api/v1/matters/{matter_id} | getMatter | matters:read | Matter tokens, custom fields, wall check before any module page |
| GET /api/v1/matter-parties | listMatterParty | matters:read | Client, opposing and served parties |
| GET /api/v1/contacts/{contact_id} | getContact | contacts:read | Party names, also known as, contact custom fields |
| GET /api/v1/contact-addresses | listContactAddress | contacts:read | Current primary address |
| GET /api/v1/contact-addresses/{address_id} | getContactAddress | contacts:read | Served party's service address |
| GET /api/v1/counties/{county_id} | getCounty | settings:read | `matter_county` (CC-4) |
| GET /api/v1/practice-areas | listPracticeArea | settings:read | Template and package filters (CC-4) |
| GET /api/v1/party-roles | listPartyRole | settings:read | `opposing_party_role` label (CC-4) |
| GET /api/v1/custom-field-definitions | listCustomFieldDefinition | settings:read | Custom field tokens and their types (CC-4) |
| GET /api/v1/firm-profile | getFirmProfile | firm:read | Firm tokens, firm time zone (CC-2) |
| GET /api/v1/user-directory | listUserDirectory | users:directory | Attorney name, attorney profile list (CC-3) |
| POST /api/v1/file-uploads | createFileUpload | files:write | Stage a template or generated output (`library` per CC-1) |
| PUT /api/v1/file-uploads/{upload_id} | putFileChunk | files:write | 8 MB chunks |
| POST /api/v1/file-uploads/{upload_id}/complete | completeFileUpload | files:write | Stage bytes; no file and no event yet |
| POST /api/v1/files | publishFile | files:write | Publish one template or component body, one version copy |
| POST /api/v1/files:bulk | bulkFile | files:write | Publish every output of a run in one transaction |
| GET /api/v1/files | listFile | files:read | Matter files for zips; library list (CC-1 filter); `include=documents.generated` |
| GET /api/v1/files/{file_id} | getFile | files:read | Visibility and status of tracked files |
| PATCH /api/v1/files/{file_id} | updateFileMetadata | files:write | Category hint on a new output |
| POST /api/v1/files/{file_id}/status | markFileStatus | files:write | Draft on publish; Mark final; Mark draft |
| GET /api/v1/files/{file_id}/versions | listFileVersions | files:read | Template history, zip versions, version tools |
| GET /api/v1/files/{file_id}/versions/{version_id}/download | downloadFileVersion | files:read | Read a template, component or version into memory |
| GET /api/v1/files/{file_id}/renditions | listFileRenditions | files:read | Markdown copy per version for compare and zips |
| GET /api/v1/files/{file_id}/renditions/{rendition_id} | downloadFileRendition | files:read | Markdown text for compare |
| GET /api/v1/folders | listFolder | files:read | Folder names for zip paths and the folder picker |
| POST /api/v1/file-archives | createFileArchive | files:read | Stream the zip (`entries` per CC-5) |
| POST /api/v1/files/{file_id}/conversions | convertFile | editor:run | PDF beside a generated DOCX |
| GET /api/v1/doc-tender/categories | listDocCategory | doc_tender:read | Category hint picker |
| GET /api/v1/files/{file_id}/doc-tender-status | getDocTenderStatus | doc_tender:read | Status line on the Drafts tab and compare |
| POST /api/v1/events | createEvent | events:write | One timeline line per run and per zip |
| Webhook to /events | moduleEvent | events:read, files:read | `file.updated`, `file.trashed` |

## 6. Core changes

The module-core lane builds these from docs/modules/CORE-CHANGES.md. The core lacks each of them today; each carries its own core tests and OpenAPI and MCP parity. Each item names its merged definition there, which is the one built; where the merged definition serves more callers than this module needs, this module relies only on what is written here.

- **CC-1 Library files (CORE-17).** Add `library BOOLEAN NOT NULL DEFAULT false` to `core.files` (core migration). `createFileUpload`, `publishFile` and `FileBulkItem` accept `library`; `true` requires `matter_id` and `contact_id` null, else `validation_failed`. `File` returns `library`. `listFile` takes a `library` boolean filter. `updateFileMetadata` refuses setting a matter or contact on a library file with `validation_failed`. Access is the existing firm-only rule (admin, attorney, staff; accountant and client get `not_found`). Doc Tender queues no job for a library file's versions, reports `getDocTenderStatus` as `not_processed_library`, opens no exception, spends no tokens, and its nightly sweep skips library files. `file.version_created` still fires.
- **CC-2 Firm profile read (CORE-18).** New `GET /api/v1/firm-profile`, operation `getFirmProfile`, new scope `firm:read`, every role (the client role is for the client portal), `module_delegation: delegated_viewer`. Returns `name`, `short_name`, `address`, `phone`, `email`, `website`, `logo_file_id` and `time_zone` from the `firm.*` settings. Read only; writes stay on the admin settings.
- **CC-3 User directory (CORE-19).** New `GET /api/v1/user-directory`, operation `listUserDirectory`, new scope `users:directory`, roles admin, attorney and staff (the merged definition also serves client callers a reduced row, and adds `ids`, `role` and `active` filters; this module filters with `role=attorney`), `module_delegation: delegated_viewer`, cursor and limit. Each row: `id`, `name`, `roles`, `active`. No email, auth or rate fields.
- **CC-4 Delegation on settings reads (CORE-20).** Set `module_delegation: delegated_viewer` on `listCounty`, `getCounty`, `listPracticeArea`, `getPracticeArea`, `listPartyRole`, `getPartyRole`, `listCustomFieldDefinition` and `getCustomFieldDefinition`. Scope (`settings:read`) and roles stay as they are.
- **CC-5 Archive entries (CORE-21).** `createFileArchive` accepts a third selector, `entries`: an array of `{file_id, version_id, path}` or `{file_id, rendition_id, path}`, exclusive with `file_ids` and `folder_id`. `path` is relative, `/`-separated, at most 255 bytes, with no leading `/`, no backslash, no `.` or `..` segment and no control character, and unique without regard to case; otherwise `validation_failed` and no bytes. Each entry is checked as the viewer before the first byte (a hidden one is `not_found` for the whole archive), the quarantine rule is unchanged, each entry writes one `file.downloaded`, and the zip is streamed. Credentials, the context token included, are checked once when the request starts, and that one response streams to completion after the token's expiry.
- **CC-6 Module frame bridge and sandbox (CORE-09).** The core page that frames a module listens to `postMessage` from its own child frame (the source window must be that frame's window; anything else is ignored) and handles `{type: "togavsu:editor.open", file_id}` (staff frames only; PRD 8.20 Open: checks write access as the viewer, then opens `/files/{file_id}/edit`), `{type: "togavsu:navigate", page, params}` (the same allowlist and typed filters as widget targets, checked as the viewer) and `{type: "togavsu:context.refresh", request_id}` (answered with a fresh context token). The staff frame sandbox in `writeFrame` (`internal/modules/http.go`) and in the matter-screen frame (`web/templates/practice/matter.html`) becomes `allow-scripts allow-forms allow-downloads allow-popups allow-popups-to-escape-sandbox`, so zips and files download from module pages; it never gets `allow-same-origin`.
- **CC-7 Sidebar placement (CORE-08).** Manifest `sidebar` entries gain `section`, `icon` and `order`. This module's Templates entry sits in section `documents` with icon `file` (an icon the core adds) at order 200, in place of the fixed Activity section and puzzle icon.

## 7. Behaviors and acceptance criteria

All criteria run on synthetic data: firm "Example Law LLC", attorney "Jordan Testwell" (bar MO 00001), matter 2026-0001 "Sample v. Placeholder", client "Avery Sample", opposing party "Riley Placeholder", county "Testcounty".

### 7.1 Install

Install, update and uninstall follow PRD 9.3 and 9.9 with no module-specific steps.

- **MOD-DOC-01** The signed package installs on a core with CC-1 to CC-6; the core creates `mod_documents` with every table and view in section 4 and lists all eleven scopes with their reasons. Declining a required scope cancels the install.
- **MOD-DOC-02** Declining `editor:run` installs the module; the `docx_and_pdf` output choice shows "Needs the editor permission" and can't be picked. Declining `doc_tender:read` hides the category hint and the Doc Tender status line.

### 7.2 Template and component library

The Templates tab lists active templates, packages and components with search over name, topics, description and practice area, and filters by practice area and document class. Admin, attorney and staff can create, edit metadata, archive and unarchive.

- New template three ways: upload a DOCX, copy the current version of a file the viewer can read (Version tools, Save as template), or blank. Blank writes a minimal valid DOCX (content types, relationships, `word/document.xml` with one paragraph `{{document_title}}`, default styles) built with the Go standard library.
- The body is stored as a library file (CC-1) named `<slug>.docx` and the template row points to it. Upload takes `.docx` only, and only a zip holding `word/document.xml`; anything else is refused with the reason and nothing is published. To use a DOC, ODT or RTF, a person opens it in the editor (which saves DOCX) and saves that as a template.
- Edit body opens the library file in LawCraft through CC-6. Each save there adds a core version. The template page shows the version list from `listFileVersions`, newest first, with who saved and how; restore is the core's.
- Check template: the module reads the current version and reports every token (known or manual-only), every slot (mapped or not) and every component problem. Saving slot mappings with an unmapped slot is refused.
- Components follow the same rules. A component's paragraphs and tables are copied into the slot, so a component holding a picture, hyperlink, footnote, endnote, comment, numbering (list) reference or any other relationship part is refused at save and again at generation, naming the part. Tokens inside a component resolve with the template's.
- Archived templates, components and packages leave every picker; their files and history stay.

Acceptance:

- **MOD-DOC-03** Uploading a DOCX makes a library file (no matter, no contact, `library` true) and a template row; `getDocTenderStatus` on it is `not_processed_library` and no Doc Tender exception opens.
- **MOD-DOC-04** Uploading a PDF, or a zip without `word/document.xml`, returns 422 naming the reason; no upload is published and no row is written.
- **MOD-DOC-05** A blank template's file opens as a zip whose `word/document.xml` holds `{{document_title}}`, and the core editor opens it.
- **MOD-DOC-06** Adding a version to the body file (an editor save, simulated in tests with `addFileVersion`) shows on the template page as the newest version; the next generation records that version number in `generated_files.template_version_number`.
- **MOD-DOC-07** Editing name, topics or practice areas adds no file version.
- **MOD-DOC-08** Generating from an archived template or package returns 409 and writes nothing.
- **MOD-DOC-09** Check template on a body with `{{client_full_name}}`, `{{school_name}}` and `{{slot:caption}}` reports one known token, one manual-only token and one slot; saving with `caption` unmapped returns 422.
- **MOD-DOC-10** Saving a component whose DOCX holds a hyperlink returns 422 naming "hyperlink"; one holding a numbered list returns 422 naming "numbering".

### 7.3 Tokens

Token syntax is `{{name}}`: lower-case letters, digits and underscores, with spaces inside the braces allowed. A slot is `{{slot:name}}` alone in a paragraph of the main body. Tokens resolve in the main body, tables, headers and footers.

The resolver reads through the API as the viewer. Vocabulary (from Unified's `field-vocabulary.ts`):

- Matter: `matter_number`, `matter_name`, `matter_case_number`, `matter_court`, `matter_county` (county name), `matter_client_position`.
- Client (the chosen client party): `client_full_name`, `client_first_name`, `client_last_name`, `client_also_known_as`, `client_address_line_1`, `client_address_line_2`, `client_city`, `client_state`, `client_zip`. The address is the contact's primary address with no `effective_to`; with none primary, the open address with the latest `effective_from`.
- Opposing party (the primary party with role key `opposing_party`, else the first): `opposing_party_full_name`, `opposing_party_role` (the party role's label).
- Served party (picked on the form from the matter's parties): `served_party_name`, `served_party_address` (its service address on one line, else its current address).
- Firm (CC-2): `firm_name`, `firm_address`, `firm_phone`, `firm_email`, `firm_website`.
- Attorney (the matter's responsible attorney, through CC-3 and `attorney_profiles`): `attorney_name`, `attorney_last_name` (last word of the name), `attorney_bar_number`, `attorney_bar_state`, `attorney_signature_line`, `attorney_courtesy_name` (courtesy title plus last name, only when the admin typed a title), `attorney_for_role` ("Attorney for " plus the client position).
- Document and dates: `document_title` (typed on the form, default the template name), `today_long` ("November 12, 2026" in the firm time zone), `filing_date_long` (picked on the form, default today, as "12th day of November, 2026").
- Custom fields: `matter_cf_<key>` and `client_cf_<key>` for every active custom field on the matter and the chosen client. Dates format as `today_long` does, booleans as Yes or No, pick lists as the option label.

A blank resolved value counts as unresolved. Any other token is manual-only and must be typed on the form. Values are XML-escaped; a line break in a value becomes a DOCX line break. A token split across runs in one paragraph, as Word and LawCraft often store it, still resolves: the module joins the paragraph's run text, replaces, writes the result into the first run that held the token and removes the token's text from the runs after it, keeping that first run's formatting.

- **MOD-DOC-11** On the synthetic matter every vocabulary token above resolves to its expected string, including `matter_county` "Testcounty", `attorney_bar_number` "00001" and `today_long` in the firm time zone at a fixed test clock.
- **MOD-DOC-12** `{{client_full_name}}` split across three runs resolves to "Avery Sample" in the first run's formatting; `R&D <Co>` is written escaped; "Line 1\nLine 2" writes a `w:br`.
- **MOD-DOC-13** Tokens in `word/header1.xml` and `word/footer1.xml` resolve.
- **MOD-DOC-14** A slot paragraph is replaced by the mapped component's paragraphs and tables in order, with the component's tokens resolved.

### 7.4 Assemble and generate

From the matter tab, the New from template action or quick-create, a person picks a template or package. Pickers show active items whose practice areas include the matter's, or that name none. The assemble page shows each token with its resolved value and its source, and an input for each unresolved or manual-only token. When the matter has more than one client party, the page asks which client fills the client tokens, defaulting to the primary.

Generate rules, carried from the DocForge PRD (D2, D3, D5):

- Nothing ships with an unresolved token. Generate is refused while any token is unresolved and unfilled, or any manual value is blank, and the response names each template and token.
- One run is all or nothing. The module renders every output in memory, stages every upload, and publishes them with one `bulkFile` call. Any failure before that call publishes nothing; staged uploads are left to the core's staging expiry and never published.
- Every run makes new files and never touches earlier ones.
- Each output: matter set, no contact, arrival name `<template-slug>-<YYYY-MM-DD>.docx`, `attorney_only` from the template, `shared_with_client` false. Right after publish the module marks each one draft and, when the template has a category hint and `doc_tender:read` is approved, sets that category with `updateFileMetadata`.
- With output `docx_and_pdf` and `editor:run` approved, the module calls `convertFile` for each output after publish. A conversion failure leaves the DOCX published and the run `pdf_failed`, with a Retry PDF button on the run.
- After the outputs publish, the module writes one timeline event: action `documents.generated`, record_type `matter`, the matter id, summary "Generated 3 documents from package Estate plan" or "Generated Motion to withdraw from template", client_visible false. A failed run writes no event.
- The run row records outcome, file count, the token names filled by hand and an error report. Typed values are never stored anywhere: not in module tables, logs or events.
- A closed matter can still generate; the page notes that Doc Tender holds files on closed matters (PRD 8.21).

Acceptance:

- **MOD-DOC-15** With `{{school_name}}` unfilled, Generate returns 422 naming the template and `school_name`; no file is published, no event is written and the run is `failed`.
- **MOD-DOC-16** A blank manual value is refused. After a run with "Testvalue-7731" typed for `school_name`, no module table, module log line or core event row contains that string.
- **MOD-DOC-17** With two client parties, the page requires a choice and the chosen client's name fills `client_full_name`.
- **MOD-DOC-18** A single-template run publishes one DOCX on the matter with status draft, origin firm, `shared_with_client` false, `attorney_only` from the template, source `documents`, and a `generated_files` row; `listFile?matter_id=…&include=documents.generated` returns the template id on it.
- **MOD-DOC-19** Generating the same template twice makes two files; the first file's versions are unchanged.
- **MOD-DOC-20** One successful run writes exactly one `documents.generated` event on the matter timeline.
- **MOD-DOC-21** With `docx_and_pdf`, a run makes a PDF file linked to its DOCX by `conversion_of`. With the converter made to fail in the test, the run is `pdf_failed`, the DOCX stays, and Retry PDF then makes the PDF.
- **MOD-DOC-22** A template with a category hint gives its output that category, and Doc Tender keeps it (the person-set rule).

### 7.5 Packages

A package is a named, ordered list of templates, at most `max_package_templates`. One assemble form gathers the union of tokens across its templates; a shared token is filled once. Generation follows 7.4 with every template's output in the one `bulkFile` call. In code, a single-template run is a package of one.

- **MOD-DOC-23** In a package of three templates where the third maps a slot to a component holding a picture, Generate returns 422 naming that template and "picture", and publishes nothing. With that component fixed, the same run publishes three files in one `bulkFile` call, all drafts.
- **MOD-DOC-24** A package over `max_package_templates` can't be saved.

### 7.6 Drafts and status

The Drafts tab lists generated files the viewer can see whose status is draft, newest first, filtered by `matter_id` when given, with the template, run date and (with `doc_tender:read`) Doc Tender's status. Each row offers Open in editor (CC-6) and Mark final; a Final filter shows final files with Mark draft. Status writes call `markFileStatus`; the status shows as a tag and never enters the name. The widget shows the same list, top eight, for the last `drafts_window_days` days. Visibility is checked per row with `getFile` as the viewer, and a row the core hides is skipped.

- **MOD-DOC-25** Mark final sets the file's status to final and removes it from the default list; Mark draft returns it.
- **MOD-DOC-26** A staff user off a walled matter's wall sees none of its drafts in the tab or the widget, and the matter's module pages return 404.
- **MOD-DOC-27** The widget's click-through opens `/modules/documents/tabs/drafts?matter_id=…` and the tab shows only that matter's drafts.

### 7.7 Bulk download

The Download action and the matter tab offer: whole matter, one folder, or a selection from the matter's file list; a status filter (any, draft, final, executed, filed, received) and a category filter; versions current or all; Markdown copies on or off; layout folders or flat, defaulting to the setting. There's no size or count cap.

- Entries are the current version of each file the viewer can see, excluding trashed files. With folders layout the path is `<folder name>/<file name>`, and unfiled files go under `Unfiled/`. All versions writes `<base>-v<n>.<ext>` for each version. Markdown copies write `<base>.md` (`<base>-v<n>.md` with all versions) beside the source where a Markdown rendition exists. A clash in one folder adds `-2`, `-3`, matching the core.
- With no entries the page says "Nothing to download" and makes no zip, no manifest and no event.
- When any chosen version is quarantined, the page lists those files and asks "Include after confirming" or "Leave them out"; including sends `confirm_quarantine`.
- The module writes the manifest (`bulk_downloads` with status started, and one `bulk_download_items` row per entry with version number and SHA-256 from `listFileVersions`) before the stream starts, then calls `createFileArchive` with `entries` (CC-5) and streams the core's response to the browser. The zip is named `<matter number>-documents-<YYYY-MM-DD>.zip` in the firm time zone. When the stream ends the manifest is `complete` and the module writes one event: action `documents.bulk_downloaded`, record_type `matter`, summary "Downloaded 14 files as one zip". A broken stream marks it `interrupted` with no event. The core still logs every entry as a download (PRD 8.19).

Acceptance:

- **MOD-DOC-28** A whole-matter zip of a matter with six files (four in two folders, one unfiled, one trashed) plus one attorney-only file, downloaded by staff, holds exactly the five visible current versions under `Pleadings/`, `Correspondence/` and `Unfiled/`; the manifest rows match the zip's entry paths, version numbers and SHA-256s; the core logged five `file.downloaded`; one `documents.bulk_downloaded` event exists.
- **MOD-DOC-29** Folder scope, a selection and a status filter of final each produce exactly their files.
- **MOD-DOC-30** All versions with Markdown on a file with two versions writes `name-v1.docx`, `name-v2.docx`, `name-v1.md` and `name-v2.md` where those renditions exist.
- **MOD-DOC-31** An empty scope returns "Nothing to download" and writes no `bulk_downloads` row and no event. A quarantined version with "Leave them out" is absent from the zip and the manifest.
- **MOD-DOC-32** When the test cuts the stream mid-zip, the manifest is `interrupted` and no summary event is written.

### 7.8 Version tools

Version tools open on any file the viewer can see on the matter.

- **Notes and labels.** One note and an optional label (sent, signed, filed, reviewed, other) per version, for viewers with write access to the file, shown beside the core's version list.
- **Compare.** Pick two versions of one file. The module reads each version's Markdown rendition and shows a line diff (longest common subsequence, standard library) with added and removed lines marked. A version without a Markdown copy shows "Markdown copy not ready" and, with `doc_tender:read`, Doc Tender's status (on a closed matter, that it isn't processed).
- **Copy as new file.** Copies one version into a new file on the same matter, arrival name `<base> (from v<n>).<ext>`, same `attorney_only`, recorded in `version_copies`. Doc Tender then processes it like any new file.
- **Save as template.** Copies the current version into a new library file and opens the new template's metadata form.

- **MOD-DOC-33** A note and label on version 2 show on the version list, and a viewer without write access gets 404 on the write. Compare of two versions with Markdown copies marks one added and one removed line for a one-line change; a version with none shows "Markdown copy not ready". Copy as new file makes a second file on the matter whose version 1 SHA-256 equals the source version's, plus a `version_copies` row.

### 7.9 Attorney profiles

The admin-only Attorney profiles page lists active users with the attorney role from CC-3 and edits bar number, bar state, courtesy title and signature line in `attorney_profiles`. The page never guesses a courtesy title; with none typed, `attorney_courtesy_name` is a manual fill.

- **MOD-DOC-34** A staff viewer gets 404 on the page and its writes. After the admin saves bar number 00001, `attorney_bar_number` resolves to it, and `GET /api/v1/users/{id}?include=documents.attorney` as admin returns it.

### 7.10 Events, trash and Release

`/events` checks `X-Togavsu-Signature` (HMAC-SHA256 of the raw body) and `X-Togavsu-Timestamp` within five minutes, records each event id once in `received_events`, and for a tracked file refreshes `generated_files.status_cache` (`file.updated`) or `trashed` (`file.trashed`).

Module rows point at files with `ON DELETE RESTRICT`, so the core refuses to purge a trashed file the module tracks and names this module (PRD 9.9). The admin-only Release page lists trashed files the module holds. Release on a generated file deletes its `generated_files` row and nulls any `pdf_file_id` pointing at it. Release on a template or component body deletes that template or component and its slot and package rows, after the admin types its name. Every Release nulls `bulk_download_items.file_id` and deletes the `version_notes` and `version_copies` rows for that file; manifest entry paths and SHA-256s stay.

- **MOD-DOC-35** A delivered `file.updated` for a tracked file updates its status cache once; a second delivery of the same event id changes nothing; a bad signature returns 401 and changes nothing.
- **MOD-DOC-36** Purging a trashed generated file is refused naming `documents`; after Release the purge succeeds, and the manifest row keeps its entry path and SHA-256 with `file_id` null.

### 7.11 Search, quick-create, help and access

- **MOD-DOC-37** `/search?q=withdraw` returns active templates whose name, topics or description match, for admin, attorney and staff. Quick-create opens a matter picker (`listMatter`) and then the assemble page. The six help pages appear under "Documents and templates" in the guide.
- **MOD-DOC-38** Accountant and client viewers get 404 on every module page and endpoint, and the widget isn't offered to them.
- **MOD-DOC-39** Uninstall with keep data leaves `mod_documents` locked and a reinstall reads it; delete data drops it after a backup and typed confirmation (PRD 9.9). Library and generated files stay in the core.

## 8. Tests

All tests run in the repository's own CI (`go test -race ./...` against a Postgres 17 service through `TOGAVSU_TEST_DATABASE_URL`, plus `togavsu-module check` on the package), which sets up the same wordcraft-cli build the core's Core tests job extracts. No test reads real client data.

Unit tests in `modules/documents/`, no database:

- `docx_test.go`: token fill across split runs, escaping, line breaks, headers and footers, slot copy, refusal of each relationship part, blank DOCX shape, non-DOCX refusal (MOD-DOC-04, 05, 10, 12 to 14).
- `tokens_test.go`: the resolver against an `httptest` server serving synthetic matter, party, contact, address, county, firm, directory and custom field JSON at a fixed clock; address choice rules; blank counts as unresolved; principal choice (MOD-DOC-11, 17).
- `zip_test.go`: entry paths, folder and flat layouts, `-v<n>` and `.md` naming, clash suffixes, the CC-5 path rules (MOD-DOC-30).
- `diff_test.go`: line diff on synthetic Markdown (MOD-DOC-33 compare).
- `package_test.go`: `Files()` parses with `modulekit.ParseManifest`, every migration passes `modulekit.CheckMigrations`, the widget target resolves, and a tampered migration fails the check (the static half of MOD-DOC-01).

Acceptance tests in `internal/modules/module_documents_pg_test.go` (package `modules_test`, reusing the existing harness in `internal/modules/harness_pg_test.go`: `newEnv`, the fake runtime, `approveAll`, `contextFor`, `moduleDB`). Before install each test sets `e.rt.handlers["documents"]` to a handler that, on its first request after each Deploy, reads `e.rt.last("documents")` for the deployment's secrets and builds `documents.New` with its own schema login from `e.moduleDB`. coretest serves the core routes this module calls (CORE-29). Each test builds and signs the package with `documents.Build` and a generated key, trusts the key, installs over `POST /api/v1/modules`, approves permissions, and drives the module's pages through the core with context tokens:

- `TestPGDocumentsInstall`: MOD-DOC-01, 02 (declining a required scope cancels; declining an optional one installs).
- `TestPGDocumentsLibrary`: MOD-DOC-03, 06 to 09.
- `TestPGDocumentsGenerate`: MOD-DOC-15, 16, 18 to 22 (scans every `mod_documents` table, the module's captured log and `core.events` for the typed value).
- `TestPGDocumentsPackages`: MOD-DOC-23, 24.
- `TestPGDocumentsDrafts`: MOD-DOC-25 to 27, with a wall on a second synthetic matter.
- `TestPGDocumentsZip`: MOD-DOC-28, 29, 31, 32 (a cut client connection for the interrupted case).
- `TestPGDocumentsVersionTools`: MOD-DOC-33.
- `TestPGDocumentsAttorneys`: MOD-DOC-34.
- `TestPGDocumentsEventsRelease`: MOD-DOC-35, 36.
- `TestPGDocumentsAccessAndLifecycle`: MOD-DOC-37 to 39.

Failure cases are part of each test: refused upload, unresolved token, refused component, package over the cap, off-wall viewer, accountant and client 404s, bad webhook signature, duplicate event, purge refusal, quarantine without confirmation. The module-core lane adds core tests for CC-1 to CC-7 beside the code it changes.

## 9. Code layout

```
modules/documents/
  README.md                 what it does; build, sign, install (as matter-clock's README)
  Dockerfile                golang build stage, distroless static runtime, ENTRYPOINT ["/documents","serve"]
  cmd/documents/main.go     serve and package subcommands, as examples/matter-clock/cmd
  documents.go              ID, embedded package files, Files, Archive, Build, SignFile, Config, New, Handler
  api.go                    core API client with module token and context token
  docx.go                   DOCX read, token fill, slot copy, part checks, blank DOCX
  tokens.go                 vocabulary and resolver
  generate.go               assemble form, runs, packages, bulkFile publish, PDF, timeline event
  library.go                templates, components, packages, Check template
  drafts.go                 drafts tab, widget, status writes
  zip.go                    scopes, filters, entries, manifest, archive streaming
  versions.go               notes, compare, copy, save as template
  attorneys.go              attorney profiles
  events.go                 webhook, Release
  pages/                    html/template files and one CSS file, no external assets
  package/togavsu-module.yaml
  package/migrations/0001_library.sql to 0006_views.sql
  *_test.go                 section 8 unit tests
internal/modules/module_documents_pg_test.go   section 8 acceptance tests
```

Only the Go standard library, the dependencies the core's go.mod pins and the module kit are used; no PDF or DOCX library is added. The package is signed through the module kit (`pkg/modulekit`, the signing path the core uses); `documents package -key <file> -out documents.signed.json` writes the signed file. The build writes only the CharitonMedia/togavsu-documents repository and nothing in the core repository. CC-1 to CC-7 go to the module-core lane through docs/modules/CORE-CHANGES.md.

## 10. Out of scope

- **Questionnaire tokens and the DocForge engine.** Unified's `q_*`, `principal_*`, marital and children statement tokens and its serializer-to-engine call read questionnaire answers, which the intake module (PRD 9.8 item 3) owns. Until a later version of this module requests the intake module's published answers view, those tokens are manual fills. DocForge's engine and its template families stay in the DocForge project.
- **Encrypted identifiers.** No token reads an SSN or EIN. A person types one on the form or in the editor, and the module never stores it.
- **Quick Parts and building blocks.** They belong to LawCraft in the core editor.
- **Page split and merge** of scanned PDFs (`page-assembly.ts`). That's Doc Tender's review work in the core.
- **Client bulk download.** The client portal module (PRD 9.8 item 6) gives clients their own Download all.
- **Signing and sending.** E-signature (PRD 9.8 item 8) and email (item 5) are other modules; they read `generated_documents`.
- **Importing Unified's HTML templates.** Record importers move existing templates; this module starts with an empty library.
- **Clause logic** (conditional sections, repeating blocks). Templates are fixed text with tokens and slots.
