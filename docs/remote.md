# Using icb from other machines

`icb serve` shows your source code, so it always requires a token, except with `-insecure-no-auth`, which it
only accepts on a loopback address.

## Tokens

```sh
icb serve ../goweb                        # prints a random token and a sign-in URL
ICB_TOKEN=$(openssl rand -base64 32) icb serve ../goweb
icb serve -token "$ICB_TOKEN" ../goweb
```

- **Browsers**: open the printed `…/?token=…` URL once (or type the token into the sign-in page). The server sets
  an `HttpOnly`, `SameSite=Strict` cookie for 30 days and redirects to a URL without the token.
- **API and MCP clients** send `Authorization: Bearer <token>`:

  ```sh
  curl -H "Authorization: Bearer $ICB_TOKEN" http://127.0.0.1:8080/api/summary
  claude mcp add --transport http icb http://127.0.0.1:8080/mcp --header "Authorization: Bearer $ICB_TOKEN"
  ```

A generated token changes on every start; set `ICB_TOKEN` to keep one. Anyone with the token can read all of the
analyzed code.

Every response carries a strict Content-Security-Policy, `X-Frame-Options: DENY` and `Referrer-Policy:
no-referrer`. The API is read-only and the MCP endpoint needs the token, so other sites cannot act on your
behalf.

## Reaching it from elsewhere

Keep the default `127.0.0.1:8080` and put something in front of it:

- **Tailscale**: `tailscale serve --bg 8080` publishes it over HTTPS on your tailnet only.
- **A reverse proxy with TLS**, e.g. Caddy:

  ```
  icb.example.com {
      reverse_proxy 127.0.0.1:8080
  }
  ```

- **icb's own TLS**: `icb serve -addr 0.0.0.0:8443 -tls-cert cert.pem -tls-key key.pem ../goweb` (the cookie is
  then marked `Secure`).

Listening on `0.0.0.0` without TLS sends the token in clear text; only do that on a trusted network.

## Docker

The image analyzes the module mounted at `/src`. Analysis runs `go list`, so the image keeps the Go toolchain and
downloads the module's dependencies on first start; mount a volume at `/go/pkg/mod` to keep them.

```sh
docker build -t icb .
docker run --rm -e ICB_TOKEN -p 127.0.0.1:8080:8080 \
  -v "$PWD/../goweb:/src:ro" -v icb-gomod:/go/pkg/mod icb
```

Or with compose (`deploy/compose.yaml`):

```sh
ICB_TOKEN=$(openssl rand -base64 32) MODULE_DIR=../../goweb docker compose -f deploy/compose.yaml up
```
