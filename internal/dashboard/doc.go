// Package dashboard implements the staff shell and the home screen (PRD 8.6, 8.9,
// 8.10, 8.17 and the admin page of 8.7): saved and role-default home layouts, the
// widget catalog with click-through targets, the sidebar and its nav context, the
// header's search, timers, notifications and user chip, per-user settings and
// notification preferences, the firm appearance, problem reports and the tabbed
// admin page over the settings registry.
//
// Access never lives here. Every widget row, badge count, search hit and
// notification comes from the owning domain service called with the viewer's
// principal, so a walled matter is absent from the home screen exactly as it is
// absent from the API. Hiding a sidebar entry is presentation only; the routes it
// links to check access themselves.
//
// The package owns the ui migration range 900-999 (home layouts, role layouts, user
// preferences, notification preferences, problem reports). Help and guides are in
// internal/help, which takes 950-999 of the same range.
package dashboard
