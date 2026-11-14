# Panel contract notes (for node / upstream syncs)

## Current sync: official v1.2.0 (2026-11-14)

This section supersedes historical node capability statements below.

- Node source target: official `v1.2.0`, commit `8bd89c3`.
- Panel contract baseline: backend `9b26e0c364d17cde1bc60480d58b4b300ebf56b0`.
- Go 1.27.1, Xray fork `e087e74a1bf2`, protobuf 1.36.12 and upstream QUIC
  dependencies are synchronized. Mieru is updated from 3.34.0 to 3.39.0.
- Preserve protocol `id/name`, `protocol_id`, per-instance tags, existing
  certificate filenames, local logging and fork installation/release sources.
- The panel protobuf schema has no protocol instance ID. Configuration
  negotiation therefore falls back to JSON, remembers that choice and retains
  protobuf for users and reports. Do not remove this fallback until the panel
  schema and node decoder both carry instance IDs.
- The panel has no `/v1/server/alivelist` or `/v1/server/device`. An explicit
  404/405 count response selects Xray's local device limits; other failures are
  still errors. Global atomic device admission requires a panel upgrade.
- Mieru remains an independent controller using UUID credentials, TCP/UDP and
  multiplex. It participates in prepared reload/rollback and the shared traffic
  retry queue. Per-session byte accounting replaces global metrics to avoid
  double billing between instances and across reloads. Pending deltas below the
  threshold accumulate; revocation closes sessions; empty startup keeps polling.
- Certificate fingerprint reporting and TLS 1.3 hardening are now implemented.
- Official Nowhere NW3 is imported. The current panel's version 1, `now/1` ALPN
  and hyphenated UUID credentials are incompatible and are explicitly rejected.
  This sync does not change the backend contract. SSR is still unsupported.
- Validation includes panel/limiter/common tests, root and nested protocol tests,
  real mieru TCP/UDP authentication and revocation, empty-user recovery,
  traffic retries, local-device fallback and multi-instance isolation.
- Verified on Windows with Go 1.27.1: `go test ./... -count=1 -timeout=5m`,
  `go -C pkg/nowhere test ./... -count=1 -timeout=5m`, `go mod verify`
  and `go build -o output/ppnode.exe .`. The mieru session regression also
  forwards a local echo payload and checks exact upload/download byte counts.
- Linux root/nested `-race` tests, Unix certificate permissions and static
  build verification are still running; their result is not yet recorded.
- No production deployment or external Rust NW3 client interoperability run
  was performed. The independent Rust client tests require an explicit binary.

---

What the PPanel panel (`backend/`) sends to this node, and what changed in the panel on
**2026-10-05**, recorded here so a later node or upstream sync knows what to watch.

The panel is the source of truth for `ServerConfigResponse.data.protocols`. This node consumes
that array directly (`core/validate.go` → `node/node.go` → `core/inbound/build.go`).

---

## 1. One protocol type can appear many times — this is required, not a bug

A single server may hold **multiple configs of the same protocol type**: two `vless` inbounds,
three `shadowsocks` inbounds, a `vless` on 443 plus a second `vless` on 8443, and so on.

The panel guarantees this in `node.Server.MarshalProtocols`
(`backend/internal/model/node/server.go`):

- The identity key is `"type:id"`, and the panel rejects duplicates of that pair.
- When a submitted entry has an empty `id`, the panel assigns `max(existing id for that type) + 1`.
  So the first `vless` gets `id: "1"`, the second gets `id: "2"`.

### Why the id must stay distinct end to end

| Consumer | How the id is used |
| --- | --- |
| `node/node.go` | One controller per protocol entry; the panel client is built with `ProtocolID: nodeconfig.ID`, so traffic/online reporting is per instance |
| `core/inbound/build.go` | TLS cert path is `/etc/PPanel-node/<Protocol.ID><serverId>.cer` — two TLS inbounds of the same type sharing an id would share (and overwrite) one cert file |
| `core/validate.go` | Reports `protocol[i] <type> config invalid`, indexed positionally |

### When syncing from upstream, check for these

- Any code that de-duplicates by **type** instead of by **(type, id)**, e.g. `map[string]...`
  keyed on `protocol.Type`, or a `switch` that keeps only one entry per type.
- Upstream's `Protocol` type also carries `Id`, but upstream's own node distribution is shaped
  around its module layout. Do not assume "one inbound per protocol type" when porting a
  distribution/sanitisation change.
- `backend/internal/model/node/server.go` `MarshalProtocols` is the only id authority. If a new
  write path builds `[]node.Protocol` directly, it must still go through `MarshalProtocols`.

### The panel can be ahead of this node

The panel now accepts **13** protocol types; this node implements 9. `shadowsocksr` and `nowhere`
were added to the panel without a matching inbound here (sections 7 and 8). `cert_pin_sha256`
(section 9) is likewise sent by the panel and unused here.

None of that is a bug on either side, but it means **"the panel accepts it" no longer implies
"this node can serve it"**. Do not treat a panel-side sync as a node-side capability change, and do
not drop an unknown-but-panel-valid field on the floor when adding an inbound — read sections 7
through 9 first.

---

## 2. Panel-side protocol validation added 2026-10-05

`backend/internal/model/node/protocol_normalize.go` (new) ports upstream's
`NormalizeProtocolForStorage` and runs on both admin write paths
(`createServerLogic`, `updateServerLogic`, after `applyGeneratedProtocolKeys`).

It normalises the type (`hysteria2` → `hysteria`), rejects unknown types, clears no-op fields,
and for **enabled** inbounds validates:

- `hysteria`, `tuic` → require `security: tls`
- `anytls`, `trojan` → require `security: tls` or `reality`
- `security: tls` on `anytls`, `hysteria`, `trojan`, `tuic`, `vless`, `vmess` → require non-empty
  `sni` **and** `cert_mode`
- `vless` + `flow: xtls-rprx-vision` → requires `security: tls` or `reality`

Validation runs on **write only**. Rows already in the database are untouched until an admin
edits that server.

### The panel is stricter than this node in one place

`core/validate.go` `validateTLSConfig` accepts `cert_mode` of `""`, `"none"` or `"file"` without
requiring `sni`. The panel now rejects enabled TLS inbounds that lack `cert_mode`.

**Do not relax the panel rule to match.** `core/inbound/build.go` treats it as follows:

```go
case "tls":
    switch nodeInfo.Protocol.CertMode {
    case "none", "":            // ← no StreamSetting.Security is set at all
        break
    default:
        // TLSSettings with /etc/PPanel-node/<id><serverId>.cer|.key
    }
```

`cert_mode: none` therefore does **not** mean "load an existing certificate"; it means the inbound
is built as **plaintext**. Rejecting the combination at the panel is what stops that silent
downgrade. If a future node version does support externally supplied certs with `cert_mode: none`,
the panel rule is the thing to revisit, together.

### Still validated only by the node

The panel does not duplicate these `core/validate.go` checks. If they start failing in production,
the panel is where the earlier check belongs:

- `port` in `1..65535` (the panel only checks that a type is present)
- `transport` non-empty and in `tcp|ws|websocket|grpc|httpupgrade|splithttp|xhttp` for `vless`/`vmess`/`trojan`
- `reality` requires `reality_private_key`, `reality_short_id`, `sni`, and
  `reality_server_port` in `1..65535`
- `vless` `encryption: mlkem768x25519plus` requires `encryption_mode` (`native|xorpub|random`),
  `encryption_ticket`, `encryption_private_key`
- `shadowsocks` requires `cipher`

---

## 3. Protocol types: what each side accepts

| | Panel form / `internal/config/protocol.go` | This node `core/validate.go` |
| --- | --- | --- |
| shadowsocks, vmess, vless, trojan, hysteria, hysteria2, tuic, anytls, mieru | accepted | accepted |
| socks, http, naive | accepted | **unsupported protocol type** |
| shadowsocksr | accepted (**new**, see section 7) | **not implemented yet** |
| nowhere | accepted (**new**, see section 8) | **not implemented yet** |
| snell | still rejected by the panel | unsupported |

Because of that, the panel validates only the types this node implements
(`shadowsocks`, `hysteria`, `anytls`, `trojan`, `vless`, `vmess`, `tuic`), plus `shadowsocksr`
and `nowhere` which are validated by the panel but still have no inbound here (sections 7 and 8),
and it lets `socks`, `http`, `naive`, `mieru` save unvalidated — there is no inbound to validate
them against.
`hysteria2` is normalised to `hysteria` by the panel, but this node also accepts the `hysteria2`
spelling.

`snell` remains absent on **all three** sides — the panel does not offer it, the admin form has no
field group for it, and there is no inbound here. The panel sync notes mention upstream's `snell`
validation branch; it was deliberately **not** ported, because a validation branch with no protocol
entry behind it is unreachable dead code.

---

## 4. Shadowsocks obfs fields are live — do not remove them

The panel exposes `obfs` (`none|http|tls`), `obfs_host` and `obfs_path` for shadowsocks, and this
node implements simple-obfs from exactly those fields:

`core/inbound/shadowsocks.go` → `Obfs == "http"` selects obfs, then `ObfsPath` becomes `path` and
`ObfsHost` becomes a `Host` header.

Upstream PPanel **removed** this mechanism and moved simple-obfs into its Shadowsocks plugin
system, so upstream's normaliser *rejects* a shadowsocks inbound carrying `obfs`/`obfs_host`/
`obfs_path`. That rule was deliberately **not** ported here — copying it would break working
servers. Regression test: `TestNormalizeProtocolForStorageKeepsShadowsocksObfs` in
`backend/internal/model/node/protocol_normalize_test.go`.

If a future sync brings upstream's plugin system, this node needs a matching plugin
implementation before the panel rule can be adopted.

---

## 5. Node distribution projection (ported, adapted)

`SanitizeProtocolsForNodeDistribution` **was** ported. It runs in the panel at
`backend/internal/logic/server/queryServerProtocolConfigLogic.go`, between `UnmarshalProtocols()`
and the DTO copy — the same place upstream calls it. It drops disabled/unusable inbounds, clears
the subscription-only ECH fields, and applies upstream's per-protocol `sanitizeRuntimeProtocol`.

Upstream's version was **not** copied verbatim. Every `clear*` helper was checked against the
fields this node actually reads, and three things differ:

1. **`socks`, `http`, `naive` are never filtered out.** Upstream returns `false` for `socks`/`http`
   and forces `tls` + a certificate on `naive`, which would silently delete those inbounds from
   this node's config. They are projected (client-only fields cleared) but always kept.
2. **shadowsocks keeps `obfs`/`obfs_host`/`obfs_path`** — see section 4. Upstream's shadowsocks
   branch calls `clearLegacyObfs`; that call is skipped here.
3. **mieru keeps `multiplex` and `transport`** — `node-backend/core/inbound/mieru.go` reads both.
   Upstream clears `multiplex`.

The remaining clears are safe *because* upstream only applies them where this node reads nothing:

| Cleared by | Applied to | Why it is safe here |
| --- | --- | --- |
| `clearStreamTransport` | shadowsocks, hysteria, naive, tuic, anytls | only `trojan`/`vless`/`vmess` call `buildTransportSetting`, and upstream never clears their transport |
| `clearQUICControls` | shadowsocks, hysteria | `congestion_controller`/`reduce_rtt` are read only by `tuic.go`, `padding_scheme` only by `anytls.go`; neither protocol gets this clear |
| `clearEncryption` | shadowsocks, hysteria, tuic, anytls, naive, vmess | `encryption_*` is read only by `vless.go` (and `validate.go`, which only guards vless), and vless never gets this clear |
| `clearLegacyObfs` | trojan, vless, vmess, tuic, anytls | `obfs` is read only by `shadowsocks.go` and `hysteria.go` |
| `clearTLSClient` | all | `allow_insecure` and `fingerprint` are read nowhere in this repo |
| ECH clears | all | `ech_enable` and `ech_server_name` are read nowhere in this repo |

**Still absent**: upstream's `snell` branch (see section 3 — the protocol itself is not offered by the
panel, so the branch would be unreachable). `shadowsocksr` and `nowhere` branches were ported; see
sections 7 and 8 for what they mean for this node.

**If you add a field to `panel.Protocol` that this node reads**, check it against
`SanitizeProtocolsForNodeDistribution` — a clear there will strip it before it ever reaches you.
`HopPorts`/`HopInterval` are currently cleared and unread; if the node starts honouring hysteria
port hopping, that clear must be removed on the panel side first.

---

## 7. ShadowsocksR — panel side landed, node side pending

The panel now accepts `shadowsocksr` (plus the aliases `ssr` and `shadowsocks-r`, all normalised to
`shadowsocksr`). This node has **no SSR inbound yet**, so an SSR node entry will fail here with
`unsupported protocol type`.

What the panel guarantees when it hands you an SSR protocol entry:

| Field | Panel behaviour |
| --- | --- |
| `cipher` | lowercase, trimmed, one of `none`, `aes-128-ctr/md5`-variant set: `aes-128-ctr`, `aes-192-ctr`, `aes-256-ctr`, `aes-128-cfb`, `aes-192-cfb`, `aes-256-cfb`, `rc4-md5`, `chacha20`, `chacha20-ietf` |
| `server_key` | required, non-empty |
| `ssr_protocol` | lowercase, trimmed, one of `auth_aes128_md5`, `auth_aes128_sha1`, `auth_chain_a`…`auth_chain_f` |
| `obfs` | lowercase, trimmed, defaults to `plain`; one of `plain`, `http_simple`, `http_post`, `tls1.0_session_auth`, `tls1.2_ticket_auth`, `tls1.2_ticket_fastauth` |
| `protocol_param`, `obfs_param` | passed through as configured |
| `transport` | `""`/`both`/`tcp`/`udp`; a `network` value of `tcp+udp`/`tcp,udp` is folded into `transport` on save |

Rules the panel enforces for you:

- **`origin` and `verify_*` are rejected.** Those SSR protocols do not carry a per-user UID, which
  would cap the node at a single user, so they are not offered at all.
- **A UDP-only listener with non-`plain` obfs is rejected**, because SSR's simple-obfs modes are
  TCP-only.
- **All TLS fields are cleared before the entry reaches you** — SSR has its own obfs layer, so
  `security`, `sni`, `multiplex`, the TLS-client fields, certificate fields, the reality fields,
  QUIC controls and `encryption_*` are all blanked. `port`, `cipher`, `server_key`, `ssr_protocol`
  and `obfs` are the only things you must read.

**Node-side work when SSR is implemented**: add an SSR inbound, add `shadowsocksr` to the
accept-list in `core/validate.go`, and confirm the `transport`/`udp` rules above match the inbound
you build. The panel-side regression tests are
`TestNormalizeProtocolForStorageNormalizesShadowsocksr`,
`TestNormalizeProtocolForStorageRejectsInvalidShadowsocksr`,
`TestSanitizeProtocolsForNodeDistributionKeepsShadowsocksr` and
`TestSanitizeProtocolsForNodeDistributionFiltersIncompleteShadowsocksr`.

---

## 8. Nowhere — panel side landed, node side pending

The panel now accepts `nowhere`. This node has **no Nowhere inbound yet**, so a Nowhere node entry
will fail here with `unsupported protocol type`.

What the panel emits for a Nowhere entry (`version` defaults to `1` and `1` is the only accepted
value):

| Field | Panel behaviour |
| --- | --- |
| `version` | always `1` |
| `network` | `mix`, `tcp` or `udp`. `mixed`/`both`/`tcp,udp`/`tcp+udp` normalise to `mix`; `quic` normalises to `udp` |
| `security` | always forced to `tls` |
| `alpn` | exactly one entry; defaults to `["now/1"]`; any value must be 1–255 bytes |
| `sni` | trimmed |
| `cert_mode` + certificate fields | as configured — Nowhere runs over TLS, so it needs a certificate like the other TLS protocols (`protocolRequiresTLSCertificate` includes it) |
| `port` | must be non-zero |
| everything else | rejected. Nowhere carries no `plugin`, `plugin_options`, `heartbeat`, `congestion_controller`, `traffic_pattern` or `user_hint_is_mandatory` — those fields do not exist in the panel's DTO at all, and an enabled Nowhere entry that carries any unrelated option is refused on save |

The user list's UUID is the shared key, same as the other modern inbounds.

**Legacy endpoint caveat**: `/v1/server/config` has no Nowhere shape. If a Nowhere entry matches a
request there, the panel now answers with an explicit error —
`protocol nowhere:<id> is not supported by the legacy server config endpoint; use /v2/server/{server_id}`
— instead of returning a silently truncated config. Nowhere nodes must use the `/v2` config
endpoint.

**Node-side work when Nowhere is implemented**: add a Nowhere inbound, add `nowhere` to the
accept-list in `core/validate.go`, and make sure the node fetches its config from `/v2`. The
panel-side regression tests are `TestNormalizeProtocolForStorageNormalizesNowhereDefaults`,
`TestNormalizeProtocolForStorageNormalizesNowhereNetwork`,
`TestNormalizeProtocolForStorageRejectsInvalidNowhere`,
`TestSanitizeProtocolsForNodeDistributionKeepsNowhere` and
`TestSanitizeProtocolsForNodeDistributionFiltersInvalidNowhere`.

---

## 9. Certificate pin (`X-Node-Certificate-SHA256`) — node side pending

Self-signed certificates used to be trusted blindly, because `allow_insecure` was the only lever and
it disabled verification entirely. The panel now supports pinning the node's leaf certificate:

- The panel reads **`X-Node-Certificate-SHA256`** on every node request
  (`backend/internal/handler/server/serverPushStatusHandler.go`). The header must be the
  **lowercase hex SHA-256 of the node's self-signed leaf certificate**.
- The value is normalised (trimmed, lowercased) and must be exactly 64 hex characters; anything else
  is ignored rather than stored.
- It is only recorded for protocols whose `cert_mode` is `self`. A reported pin for a
  `cert_mode: none`/`cert_dns`/`cert_http` protocol is discarded.
- The pin is handed back to the node in the config DTO as `cert_pin_sha256`, and **`allow_insecure`
  is cleared whenever a pin is present** (`backend/adapter/adapter.go`): supplying a pin is the
  node's instruction to verify the certificate against that fingerprint instead of skipping
  verification.
- Recording is idempotent. The panel compares against the stored value and only invalidates the node
  config cache when the pin actually changed, so a steady-state heartbeat does not force a full
  re-distribution. Admin edits to a server entry carry the previously reported pin forward.

**Node-side work**: send the header on every panel request once a self-signed leaf exists, and treat
a non-empty `cert_pin_sha256` in the config as "verify the leaf against this fingerprint" — do not
fall back to insecure mode when a pin is present. Panel-side coverage:
`TestNormalizeCertPinSHA256`, `TestApplyReportedCertPinUpdatesSelfSignedProtocol`,
`TestApplyReportedCertPinMatchesProtocolTypeAliases`, `TestApplyReportedCertPinIgnoresNonSelfCertMode`,
`TestApplyReportedCertPinIgnoresInvalidFingerprint`,
`TestApplyReportedCertPinUpdatesAllInstancesOfTheType`, `TestCarryForwardCertPin` and
`TestAdapterProxyCertPinForcesCertVerification`.

---

## 10. Where things live

| Concern | Path |
| --- | --- |
| Panel → node DTO (what this node parses) | `node-backend/api/panel/server.go` |
| Node accept-list and config validation | `node-backend/core/validate.go` |
| Inbound construction, cert paths, obfs | `node-backend/core/inbound/build.go`, `shadowsocks.go` |
| One controller per protocol entry | `node-backend/node/node.go` |
| Panel protocol identity (`type:id`) | `backend/internal/model/node/server.go` |
| Panel protocol validation | `backend/internal/model/node/protocol_normalize.go` |
| Panel sync ledger and per-item reasoning | `backend/.ai/sync-diffs/v1.3.8-to-v1.20.3.md` |
