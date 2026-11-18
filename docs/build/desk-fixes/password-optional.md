# Sign In Without a Password (desk fix 4)

Patrick's test install on Desk signs in with an email alone. His words, Nov 17: no password games on the test
account. That switch was made on Desk's local copy only, so every Desk update from main drops it. This fix puts
it into main as an admin setting that is off by default, so a test install turns it on and a real firm never
sees a change. The trusted PRD records it (section 5 and the 8.7 Security tab, added 2026-11-17 at Patrick's
direction: "remove the password requirement completely").

## The setting

- Add `security.password_optional` to `internal/settings/definitions.go`, right after
  `security.totp_required.client`: label "Sign In Without a Password", Kind bool, default `false`, Tab
  Security, not a PRD default (leave `PRD` unset), help "Lets users sign in with their email alone. For test
  and demo installs only: anyone who knows an account's email can sign in. Turning it off does not sign out
  anyone already signed in; each sign-in without a password is in the audit log. Default off."
- Add the label to `docs/build/desk-fixes/settings-labels.md` (after Require Two-Factor for Clients) and to
  `expectedLabels` in `internal/settings/labels_test.go`.
- Stored value, API and audit entry work like every other bool setting; changing it is audited by the
  existing settings path.
- Add the setting to the published settings contract: a `_s("security.password_optional", "boolean",
  "security", False, ...)` entry in `api/contract/settings.py` right after `security.totp_required.client`,
  and the matching `SettingKey` enum value and catalog entry in `api/openapi/v1/openapi.json`, regenerated the
  way the earlier desk fixes did it, so `API contract tests` pass.

## Sign-in

- `identity.Service` gets `PasswordOptional(ctx) (bool, error)`, reading the flag through the existing
  `s.flag` helper.
- `Service.Login`: with the setting off, nothing changes (an empty password is still refused with the
  existing "email and password are required" error, a wrong password still fails and counts toward lockout).
  With it on, an empty email is still refused; an unknown or inactive account still fails the same way it does
  today, with the same lockout accounting; and a known, active account skips the password check whatever the
  box holds, empty or not, with one exception: when the next step would be enrolling two-factor (the role
  requires it and the user has no confirmed TOTP secret, so `afterPassword` would issue a `purposeEnroll`
  challenge), the password is checked exactly as it is with the setting off. A wrong or empty password there
  fails as a normal failed sign-in and counts toward lockout, so knowing an email never lets anyone enroll
  their own authenticator on someone else's account. A user with a confirmed TOTP secret still goes to the
  code step and still gives a code. Roles that don't require two-factor and users without it sign straight in.
- Each sign-in that skipped the password check records its existing successful-sign-in audit entry with a
  field saying the password was skipped (for example `password_skipped: true` in the entry's detail), so a
  firm can tell which sign-ins came in without one. Turning the setting off ends nothing; the help text says
  so.
- `POST /api/v1/auth/login` and the `/login` form both go through `Service.Login`, so both follow the
  setting. No other route changes.
- The sign-in page (`internal/core/pages.go`, the `login` template): while the setting is on, the password
  box drops `required` and its label reads "Password (optional on this install)". Every place that renders the
  login template goes through one helper (`renderLogin`) so the label is right after an error too. With the
  setting off, the page is byte-for-byte what it is today.

## Tests

- `internal/identity/password_optional_test.go`: off refuses an empty password; on signs a known active
  staff user in with an empty password and with any other text, matching email case-insensitively, and the
  audit entry shows the password was skipped; an unknown email still fails; for a client role with two-factor
  required and no TOTP secret, an empty or wrong password is refused (no enrollment token, a failed attempt
  recorded) and the right password gets the enrollment step; a user with a confirmed TOTP secret still gets
  the code step, not a session.
- Extend `internal/settings/labels_test.go` with the label. `TestPRDDefaults` must still pass. The settings
  contract tests must list the new key.
- A core page test: `/login` has `required` on the password box with the setting off, and the optional label
  without `required` with it on.

## Not in this fix

No change to two-factor rules, lockout settings, sessions, API tokens or any other setting. Turning the
setting off does not end sessions opened while it was on; the help text and audit entries say which they
were. No startup warning, banner or email.
