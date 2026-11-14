# Vendored browser libraries

PRD 6 names HTMX for the server-rendered UI and gridstack.js for the home screen grid. Both are
served from this folder (no CDN), so the strict Content Security Policy (`script-src 'self'`) holds.

| File | Package | License | SHA-256 |
| --- | --- | --- | --- |
| htmx-2.0.11.min.js | htmx.org@2.0.11 `dist/htmx.min.js` | 0BSD (htmx-LICENSE.txt) | d6fdc75f204e6bdefa99b69bf1e6d4ac69b8a364f77929f45c13476b4000f717 |
| gridstack-12.3.3-all.js | gridstack@12.3.3 `dist/gridstack-all.js` | MIT (gridstack-LICENSE.txt) | 7def9021987c8c8581dc5d5dfc4b5b132047b1ddf71d6b8ddf88a155824a0c1f |
| gridstack-12.3.3.min.css | gridstack@12.3.3 `dist/gridstack.min.css` | MIT | 55e9d4ea6d8c6f8f1ea8a449b8af18d8571487c0afc6b433cccf877047cb8457 |

The shell sets `htmx-config` to `includeIndicatorStyles: false` (no inline style element),
`allowEval: false` and `allowScriptTags: false`.

To upgrade: `npm pack htmx.org@<v> gridstack@<v>`, copy the same dist files under new
versioned names, update the references in web/templates/shell/shell.html and
web/templates/dashboard/home.html and the table above.
