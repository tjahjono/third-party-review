# Docker secrets

This folder holds the credential files `docker/docker-compose.yml` mounts as
Docker secrets - the same mechanism whether you run it with plain
`docker compose up` or `docker stack deploy` on a Swarm. Each file's *entire
contents* (whitespace is trimmed) becomes one credential, and each is mounted
read-only inside the containers that need it at `/run/secrets/<filename
without .txt>` - e.g. `session_secret.txt` becomes `/run/secrets/session_secret`.

Only this README and the `*.txt.example` placeholders are committed to the
repository. The real `*.txt` files are gitignored - create them locally (or
have your deploy tooling write them) and never commit one.

## Files

| File                      | Used by            | Contents                                                            |
|---------------------------|---------------------|-----------------------------------------------------------------------|
| `session_secret.txt`      | app                 | A random secret used to derive CSRF tokens. Generate with `openssl rand -hex 32`. |
| `postgres_password.txt`   | postgres **and** app | The Postgres password. Both containers read the *same* file - postgres via its own native `POSTGRES_PASSWORD_FILE` support, the app to build its connection string - so there is one password to manage, not two. |
| `ai_api_key.txt`          | app                 | The API key for whichever `AI_PROVIDER` you configured in `.env`. Leave the file empty (still must exist) if `AI_PROVIDER=mock`, which needs no key. |
| `bootstrap_password.txt`  | app                 | The password for the first account, created once on first startup if `BOOTSTRAP_USERNAME` is also set in `.env`. Ignored forever after that first account exists - it cannot be used to reset anything later, so it's fine to leave the file in place. |

## Creating them

The quickest way is `make secrets` from the repository root: it generates a
fresh `session_secret.txt` and copies the other three from their `.example`
placeholders, which you then edit with real values before deploying.

To do it by hand:

```sh
mkdir -p secrets
openssl rand -hex 32 > secrets/session_secret.txt
echo -n 'a-strong-postgres-password'    > secrets/postgres_password.txt
echo -n 'sk-...'                        > secrets/ai_api_key.txt
echo -n 'a-strong-bootstrap-password'   > secrets/bootstrap_password.txt
```

`echo -n` (not plain `echo`) avoids a trailing newline, though the app trims
one either way if it slips in.

## Rotating a credential

A file-based secret you edit and redeploy with `docker compose up` picks up
the new content immediately on the next `up` (compose remounts the file).

Under Docker Swarm (`docker stack deploy`), secrets are immutable once
created - editing `postgres_password.txt` and redeploying does **not**
change what's already mounted into a running service. To rotate under
Swarm, give the secret a new name (e.g. rename the entry in
`docker/docker-compose.yml`'s `secrets:` block from `postgres_password` to
`postgres_password_v2`, keeping the same target filename via `target:` if
you'd rather not change what the app reads), redeploy, and Swarm creates a
new secret object and reattaches it. This is a Swarm property, not something
this app's config layer can work around.
