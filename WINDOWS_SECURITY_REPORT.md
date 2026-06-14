# Windows security report

Date: 2026-06-14

## Scope

Reviewed and hardened the Windows wallet-facing paths for:

- `xns claim`
- `xns indexer`
- Monero wallet RPC client code
- Windows amd64 build output
- Feather wallet operational compatibility

This was a source and local-build security pass, not a live mainnet transaction test.

## Findings addressed

### 1. Unauthenticated local wallet RPC

Previous behavior:

- `xns claim` and `xns indexer` started `monero-wallet-rpc` with `--disable-rpc-login`.
- The RPC listener was bound to `127.0.0.1`, which blocks remote access but still leaves the wallet RPC callable by other local processes while the claim is running.

Resolution:

- Removed `--disable-rpc-login`.
- Added per-run Digest RPC credentials.
- Updated the Go wallet RPC client to answer Digest challenges.

Residual risk:

- A compromised local machine can still attack the user more directly. This change prevents casual local RPC access but does not make a compromised workstation safe for wallet operations.

### 2. Wallet password in child process arguments

Previous behavior:

- The claim flow forwarded the wallet password to `monero-wallet-rpc` with `--password`.
- Process arguments can be visible to local tools, telemetry, shell history, and diagnostics.

Resolution:

- The child `monero-wallet-rpc` process now receives wallet passwords through a temporary private `password-file`.
- The password file is created under the existing temporary claim working directory and removed by the existing cleanup path.

Residual risk:

- The legacy `xns claim --wallet-password ...` option remains for compatibility and still exposes the password in the `xns.exe` process arguments. Use `--wallet-password-file` or `--wallet-password-stdin` for hardened use.

### 3. Vulnerable Go standard library version

Previous behavior:

- The repo declared Go `1.26`.
- The local initial toolchain was Go `1.26.0`.
- `govulncheck` reported reachable vulnerabilities in the Go standard library when run with Go `1.26.0`.

Resolution:

- Updated `go.mod` to `go 1.26.4`.
- Re-ran `govulncheck` with Go `1.26.4`; no vulnerabilities were found.

Residual risk:

- Builders must use Go `1.26.4` or newer in the `1.26` line, or a newer fixed Go release.

### 4. Unsafe password input ergonomics

Previous behavior:

- The only supported CLI password source was `--wallet-password`.

Resolution:

- Added `--wallet-password-file`.
- Added `--wallet-password-stdin`.
- Enforced exactly one wallet password source.

Recommended Feather/Windows usage:

```powershell
dist\xns-windows-amd64\xns.exe claim `
  --mainnet `
  --wallet-file "C:\Users\<USER>\Documents\Monero\wallets\<wallet>\<wallet>" `
  --wallet-password-file "C:\path\to\wallet.pass" `
  --name "<name>" `
  --owner "<64-hex-ed25519-public-key>" `
  --node "http://127.0.0.1:18081" `
  --years 1
```

## Validation performed

Commands run successfully:

```powershell
$env:GOTOOLCHAIN='go1.26.4'; go test ./...
$env:GOTOOLCHAIN='go1.26.4'; go test -race -count=1 ./...
$env:GOTOOLCHAIN='go1.26.4'; go vet ./...
$env:GOTOOLCHAIN='go1.26.4'; govulncheck ./...
$env:GOTOOLCHAIN='go1.26.4'; go build -trimpath -ldflags='-s -w' -o dist\xns-windows-amd64\xns.exe ./cmd/xns
dist\xns-windows-amd64\xns.exe --help
```

Results:

- `go test ./...`: passed
- `go test -race -count=1 ./...`: passed
- `go vet ./...`: passed
- `govulncheck ./...`: no vulnerabilities found
- Windows executable smoke test: passed
- Executable password-file parsing smoke test: passed by reaching expected owner-key validation before wallet access
- Executable password-stdin parsing smoke test: passed by reaching expected owner-key validation before wallet access

Final local Windows artifact:

- Path: `dist/xns-windows-amd64/xns.exe`
- SHA256: `9A3B307A92CD84EEAFB78F6D0BF8814FA2E742ADD502115488B7BE18C0DC26B9`

Additional tests added:

- CLI password flag handling
- Password file handling
- Password stdin handling
- Conflicting password source rejection
- Digest-auth wallet RPC success path
- Unsupported Digest algorithm rejection

## Not tested

- Live `monero-wallet-rpc` integration on this workstation, because `monero-wallet-rpc` was not installed in `PATH`.
- Live Feather wallet opening.
- Live mainnet or stagenet claim broadcast.

## Operational recommendations

- Prefer a temporary wallet funded only with the claim amount plus transaction fee.
- Prefer `--wallet-password-file` or `--wallet-password-stdin`; avoid `--wallet-password` for real funds.
- Close Feather before running `xns claim`.
- Use a local or trusted Monero node.
- Keep `monero-wallet-rpc` updated and in `PATH`.
- Verify the domain/name, owner public key, network, and years before executing a claim.
