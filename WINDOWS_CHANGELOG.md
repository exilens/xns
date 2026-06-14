# Windows hardening changelog

Date: 2026-06-14

## Summary

This branch hardens the Windows claim/indexer path for local use with Monero wallet files, including Feather wallet files that can be opened by `monero-wallet-rpc`. The CLI remains compatible with the existing `--wallet-password` flow, but safer password sources were added so users do not need to expose wallet passwords in process arguments or shell history.

## Security hardening

- Replaced unauthenticated local `monero-wallet-rpc` sessions with per-run Digest authentication.
- Removed `--disable-rpc-login` from the claim and indexer wallet RPC process launch paths.
- Added random per-process RPC credentials generated with `crypto/rand`.
- Stored RPC credentials in a temporary private config file under the existing per-run temporary working directory.
- Removed wallet password forwarding through `monero-wallet-rpc` command-line arguments.
- Opened wallet files through a temporary `password-file` instead of a process argument.
- Preserved cleanup through the existing temporary directory removal after claim completion.
- Added explicit rejection for unsupported Digest algorithms instead of silently computing the wrong response.
- Kept wallet RPC bound to `127.0.0.1`.

## CLI additions

- Added `--wallet-password-file <path>` for reading the Monero wallet password from a file.
- Added `--wallet-password-stdin` for reading the Monero wallet password from stdin.
- Preserved the existing `--wallet-password <password>` option for backward compatibility.
- Enforced exactly one wallet password source among `--wallet-password`, `--wallet-password-file`, and `--wallet-password-stdin`.
- Password file/stdin inputs trim trailing CR/LF line endings only, preserving spaces and tabs inside the password.

## Dependency and toolchain changes

- Updated the Go directive from `1.26` to `1.26.4`.
- This avoids reachable Go standard library vulnerabilities reported by `govulncheck` against Go `1.26.0`.
- No new third-party runtime dependencies were added.

## Tests added

- Added CLI tests for wallet password source selection.
- Added CLI tests for password-file and stdin password handling.
- Added CLI tests that reject missing or conflicting password sources.
- Added Digest-auth client tests for successful authenticated wallet RPC calls.
- Added Digest-auth tests that reject unsupported algorithms.

## Windows build

- Built a native Windows amd64 executable at `dist/xns-windows-amd64/xns.exe`.
- The binary was built with Go `1.26.4`, `-trimpath`, and stripped linker flags.

## Compatibility notes

- Existing command lines using `--wallet-password` still work.
- The safer recommended Windows/Feather flow is to use `--wallet-password-file` or `--wallet-password-stdin`.
- `monero-wallet-rpc` must still be installed and available in `PATH`.
- Feather should be closed before claiming so `monero-wallet-rpc` can open the wallet file.
