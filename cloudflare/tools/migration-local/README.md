# Private DEV transfer connection

Deploy `cloudflare/wrangler.migration.jsonc` first. Its default public HTTP handler returns 404; `workers.dev` and preview URLs are
disabled. Only a service binding targeting the named `MigrationBridge.fetch`
entrypoint exposes the validated migration operations. Native HTTP streaming
avoids serializing large Request bodies through the remote RPC development proxy. No migration token is used or created.

From `cloudflare/`, start the **local-only** bridge using the existing Wrangler
Cloudflare OAuth login:

```sh
node node_modules/wrangler/bin/wrangler.js dev --config tools/migration-local/wrangler.jsonc
```

It listens on `127.0.0.1:8794`. Keep that process running while transferring the
previously quiesced, sealed private export in another terminal:

```sh
node tools/transfer.mjs --directory .sensitive/approved-cutover --execute --binding-local --endpoint http://127.0.0.1:8794
```

Inspect the private receipt and complete all cutover verification before activation:

```sh
node tools/transfer.mjs --directory .sensitive/approved-cutover --execute --binding-local --endpoint http://127.0.0.1:8794 --activate-only
```

Stop Wrangler when finished and remove the temporary deployed migration helper
after cutover. Never deploy this local bridge, bind it to a network interface or
expose its port through a tunnel. Browser requests from other origins are rejected.
The transfer CLI rejects remote hosts, DNS names, alternate IPv4 forms, userinfo,
paths/query strings, tokens and redirects. Database publication, hashes and blob
integrity checks remain enforced by the remote private helper and Durable Objects.

Cloudflare documents [remote bindings](https://developers.cloudflare.com/workers/local-development/)
and [named service-binding RPC entrypoints](https://developers.cloudflare.com/workers/runtime-apis/bindings/service-bindings/rpc/).
