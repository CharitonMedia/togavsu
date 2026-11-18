# Sign In Without a Password (desk fix 4)

Patrick's test install on Desk signs in with an email alone. His words, Nov 17: no password games on the test
account. That switch was made on Desk's local copy only, so every Desk update from main drops it. This fix puts
it into main as an admin setting that is off by default, so a test install turns it on and a real firm never
sees a change.

## The setting

- Add `security.password_optional` to `internal/settings/definitions.go`, right after
  `security.totp_required.client`: label "Sign In Without a Password", Kind bool, default `false`, Tab
  Security, not a PRD setting (leave `PRD` unset), help "Lets every user sign in with their email alone. For
  test and demo installs only: anyone who knows an account's email can sign in. Default off."
- Add the label to `docs/build/desk-fixes/settings-labels.md` (after Require Two-Factor for Clients) and to
  `expectedLabels` in `internal/settings/labels_test.go`.
- Stored value, API and audit entry work like every other bool setting; changing it is audited by the
  existing settings path.

## Sign-in

- `identity.Service` gets `PasswordOptional(ctx) (bool, error)`, reading the flag through the existing
  `s.flag` helper.
- `Service.Login`: with the setting off, nothing changes (an empty password is still refused with the
  existing "email and password are required" error, a wrong password still fails and counts toward lockout).
  With it on, an empty email is still refused; a known, active account signs in whatever the password box
  holds, empty or not; an unknown or inactive account still fails the same way it does today, with the same
  lockout accounting. Two-factor is unchanged: a role that requires it still goes to the TOTP or enrollment
  step, and a user with two-factor enrolled still gives a code.
- `POST /api/v1/auth/login` and the `/login` form both go through `Service.Login`, so both follow the
  setting. No other route changes.
- The sign-in page (`internal/core/pages.go`, the `login` template): while the setting is on, the password
  box drops `required` and its label reads "Password (optional on this install)". Every place that renders the
  login template goes through one helper (`renderLogin`) so the label is right after an error too. With the
  setting off, the page is byte-for-byte what it is today.

## Tests

- `internal/identity/password_optional_test.go`: off refuses an empty password; on signs a known active
  staff user in with an empty password and with any other text, matching email case-insensitively; an unknown
  email still fails; a client role with two-factor required still gets the enrollment step, not a session.
- Extend `internal/settings/labels_test.go` with the label. `TestPRDDefaults` must still pass.
- A core page test: `/login` has `required` on the password box with the setting off, and the optional label
  without `required` with it on.

## Not in this fix

No change to two-factor rules, lockout settings, sessions, API tokens or any other setting. No startup
warning, banner or email; the help text carries the warning.
