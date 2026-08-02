# Security Policy

## Supported versions

Security fixes go into the latest release. There are no long-term support
branches: if you are on an older version, upgrading is the fix.

| Version                                                            | Supported          |
| ------------------------------------------------------------------ | ------------------ |
| [Latest release](https://github.com/vale-cli/vale/releases/latest) | :white_check_mark: |
| Anything earlier                                                   | :x:                |

## Reporting a vulnerability

Report privately through GitHub, using
[Report a vulnerability](https://github.com/vale-cli/vale/security/advisories/new)
on the Security tab. That opens an advisory visible only to you and the
maintainers.

Please do not open a public issue for something you believe is exploitable.

Include what you have: the version (`vale --version`), the platform, and a
configuration and input file that reproduce it. A minimal reproduction is worth
more than a description.

You can expect an acknowledgement within a week. If a report is accepted, the
fix ships in the next release and the advisory is published with credit unless
you would rather not be named. If it is declined, you will get the reasoning,
and you are free to disclose it publicly at that point.

## What is in scope

Vale reads configuration, styles, and documents that a project supplies, and
`vale sync` downloads packages that configuration names. Anything letting those
inputs escape their intended effect is in scope, including:

- reading or writing files outside the directories Vale was given, whether
  through a path in a package, a configuration value, or a document
- executing code from any of them
- a crash or unbounded allocation reachable from an ordinary document,
  dictionary, or style

## What is not

- A rule reporting the wrong thing, or missing something, is a bug rather than
  a vulnerability. Open an issue.
- Packages are code you choose to install. `vale sync` fetches what your
  configuration names, and a package you do not trust can change what Vale
  reports. Treat `Packages` entries as you would any other dependency.
- Slowness on deliberately pathological input given to a linter you run over
  your own files. A pattern that takes a long time on a crafted document is a
  performance problem; report it as one.
