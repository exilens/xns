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

### 0. No safe dry-run mode

Previous behavior:

- `xns claim` always proceeded from transaction preparation to `sign_transfer` and `submit_transfer`.
- That made live integration testing risky because a funded wallet could broadcast a real claim.

Resolution:

- Added `--dry-run`.
- The dry-run path opens and refreshes the wallet, prepares the watch wallet, imports key images, creates the unsigned transfer, and patches the XNS payload.
- The dry-run path returns before `sign_transfer` and before `submit_transfer`, so it does not create a signed transaction blob and cannot broadcast through XNS.

Residual risk:

- A dry run with an unfunded wallet can still stop earlier at Monero transfer construction with `not enough money`. That is expected and safe.

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

Remote live/dry-run validation:

- Host: private LAN Linux host
- SSH host key: pinned during testing, redacted from public report
- OS: Linux `6.18.34-1-lts` x86_64
- Go: `go1.26.4-X:nodwarf5 linux/amd64`
- Monero: `v0.18.5.0-release`
- `monero-wallet-rpc`: present at `/usr/bin/monero-wallet-rpc`
- Local daemon: none listening on the usual mainnet/stagenet/testnet RPC ports
- Feather wallet files: none found in the usual Linux document/config locations checked
- Remote build: passed
- Remote `go test -count=1 ./...`: passed
- Remote executable smoke test: passed
- Throwaway stagenet wallet creation: passed
- Stagenet daemon probe: multiple public stagenet daemons agreed at height `2140876` with hash `3a25349f120128553af43941d122c92b7f1b77e9353ae0dece1220e33ece9d28`
- Remote command exercised: `xns claim --stagenet --dry-run --wallet-password-file ...`
- Dry-run result: failed safely with `rpc error -17: not enough money`
- Cleanup check: no `monero-wallet-rpc` process remained
- Cleanup check: no `/tmp/xns-claim-*` directory remained
- Throwaway wallet cleanup: removed from `/tmp`

Final local Windows artifact:

- Path: `dist/xns-windows-amd64/xns.exe`
- SHA256: redacted from public report; compute locally with `Get-FileHash dist\xns-windows-amd64\xns.exe -Algorithm SHA256`

Additional tests added:

- CLI password flag handling
- Password file handling
- Password stdin handling
- Conflicting password source rejection
- Digest-auth wallet RPC success path
- Unsupported Digest algorithm rejection

## Not tested

- Live `monero-wallet-rpc` integration on the Windows workstation, because `monero-wallet-rpc` was not installed in `PATH`.
- Live Feather wallet opening, because no Feather wallet files were found on the remote host and none are available locally.
- Live mainnet or stagenet claim broadcast.
- Funded-wallet dry run that reaches successful unsigned transaction construction; the remote throwaway wallet was intentionally unfunded and stopped at Monero's insufficient-funds check.

## Operational recommendations

- Prefer a temporary wallet funded only with the claim amount plus transaction fee.
- Prefer `--wallet-password-file` or `--wallet-password-stdin`; avoid `--wallet-password` for real funds.
- Close Feather before running `xns claim`.
- Use a local or trusted Monero node.
- Keep `monero-wallet-rpc` updated and in `PATH`.
- Verify the domain/name, owner public key, network, and years before executing a claim.
