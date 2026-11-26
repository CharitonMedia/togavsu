# Marketplace beside the firm stack

The optional `togavsu-marketplace` Compose service (profile `marketplace`) runs the
marketplace from `marketplace/` on the firm's internal Docker network. A normal
`docker compose up` does not start it.

## Start

```sh
docker compose --profile marketplace up -d --wait
```

Bootstrap writes the operator token once to the `marketsecrets` volume
(`/marketsecrets/operator_token`, root:65532 mode 0640). Only bootstrap and
`togavsu-marketplace` mount that volume. State lives in `marketdata` at
`/var/lib/togavsu-marketplace`. This config file is mounted read-only at
`/etc/togavsu-marketplace/config.json`. The service listens on 8090 inside the
network only (no published port).

## Use with the core

1. Add the publishing author's public key under **Trusted local signing keys**. Plain
   http to `togavsu-marketplace` confers no marketplace key trust.
2. Set **Marketplace Address** (Settings → Modules) to
   `http://togavsu-marketplace:8090`.
3. On the same Docker network, publish with:

```sh
togavsu-module publish \
  -market http://togavsu-marketplace:8090 \
  -key signing.key -name "Your Name" -email you@example.com \
  -password-file password.txt module.signed.json
```

See `marketplace/README.md` and `docs/modules/MODULE-KIT.md` (Submitting to the
marketplace).
