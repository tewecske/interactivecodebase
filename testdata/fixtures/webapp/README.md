# webapp fixture

A small, stdlib-only web app that exercises the patterns `icb` must understand.
It compiles but is never run. `expected.json` lists what analysis must find;
`internal/fixture` checks that expectation against this source.

Patterns covered:

- routes registered in a loop over a constant-returning func (`lang.Codes()`) with string concatenation
- routes registered in a loop over a runtime value (`Deps.Providers`), which must become a `{?}` placeholder
- method-value handlers, a handler factory returning a closure, a closure wrapping a method call
- a route wrapped in auth middleware (`requireAdmin`) and in-handler guards (`noteHandler.user`)
- a guard that runs after an early config check (`noteHandler.create`)
- optional auth: public pages that look up the session (`accountHandler.signIn`, `weatherPage`)
- interface dispatch: `notes.Repository` → `postgres.NoteRepository`, `notes.Mailer` → `mail.SMTPMailer`
- SQL built from a package const plus concatenation, a transaction touching two tables, a JOIN
- sinks: SQL, SMTP, outbound HTTP, file write, exec, env read passed as a func value (`config.Load(os.Getenv)`)
- `embed.FS` templates and static assets, HTMX attributes with `{{.Field}}` URLs
- migrations with inline and `ALTER TABLE ... ADD CONSTRAINT` foreign keys
