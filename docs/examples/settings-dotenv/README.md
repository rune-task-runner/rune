# Settings & dotenv

> **Use case:** configure a project with settings and a `.env` file, and reach those values
> from task bodies — keeping secrets in the environment, not the Runefile.

**Demonstrates:** settings, dotenv, exported variables  ·  **Guide:** [Variables and settings](../../runefile.md#variables-and-settings)

**Prerequisites:** none

## Run it

```sh
rune show
```

## Expected output

```text
app=demo greeting=hello
```

`app` is a Runefile variable made available to the body by `set export`; `greeting` comes from
the `.env` file in this directory (`GREETING=hello`), which `set dotenv := ".env"` loads.

## How it works

`set export` exports Runefile variables into task environments, and `set dotenv := ".env"`
loads the `KEY=VALUE` lines of that file into them. A `.env` file is read only when the
Runefile names it: without `set dotenv`, `greeting` would print `unset`. Put secrets in the
environment (or `.env`, kept out of version control), never as literals in the Runefile.
