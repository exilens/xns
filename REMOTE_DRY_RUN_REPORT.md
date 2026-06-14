# Remote live/dry-run report

Date: 2026-06-14

## Purpose

Validate the hardened Windows/Feather wallet flow against a real host with Monero tools installed, without broadcasting a transaction or creating a signed transaction blob.

## Remote host

- SSH target: private LAN Linux host
- SSH host key: pinned during testing, redacted from public report
- OS: Linux `6.18.34-1-lts x86_64`
- User: redacted
- Go: `go1.26.4-X:nodwarf5 linux/amd64`
- Monero: `v0.18.5.0-release`
- `monero-wallet-rpc`: `/usr/bin/monero-wallet-rpc`
- `monerod`: `/usr/bin/monerod`

No SSH password, wallet seed, or wallet password is recorded in this report.

## Pre-flight checks

- Confirmed `go`, `git`, `curl`, `monero-wallet-rpc`, `monerod`, and `monero-wallet-cli` were present.
- Checked usual local daemon RPC ports: no daemon was listening on mainnet/stagenet/testnet ports.
- Checked usual Linux Feather/Monero wallet locations under `~/Documents`, `~/.config`, and `~/.local/share`: no wallet files were found.

## Source under test

- Remote checkout: `/tmp/xns-remote-test`
- Base branch: `windows-feather-hardening`
- Base commit before this round: `3fab5a1`
- Local dry-run patch applied on top for testing.

Remote validation:

```sh
cd /tmp/xns-remote-test
go test -count=1 ./...
go build -trimpath -o /tmp/xns-remote-test/xns ./cmd/xns
/tmp/xns-remote-test/xns --help
```

Result:

- Remote tests passed.
- Remote build passed.
- Remote executable smoke test passed.

## Dry-run setup

Created a throwaway stagenet wallet under `/tmp/xns-live-dryrun` using `monero-wallet-cli --stagenet --offline`.

Important notes:

- The wallet was intentionally unfunded.
- The wallet was used only for this dry run.
- The wallet directory was removed after testing.
- The generated seed was not preserved in this repository or report.

Generated a random Ed25519 owner public key for the XNS owner field.

## Stagenet daemon check

Tested these public stagenet daemon endpoints from the remote host:

- `http://stagenet.xmr-tw.org:38081`
- `https://stage.monero.raubritter.org`
- `http://node2.monerodevs.org:38089`
- `http://node3.monerodevs.org:38089`

All returned:

- Height: `2140876`
- Hash: `3a25349f120128553af43941d122c92b7f1b77e9353ae0dece1220e33ece9d28`
- Status: `OK`

For the dry-run command, `http://stagenet.xmr-tw.org:38081` was used.

Public remote nodes are acceptable for this no-spend test, but they are not recommended for real claims. A local or trusted node remains the recommended operational setup.

## Command tested

Shape of the command:

```sh
/tmp/xns-remote-test/xns claim \
  --stagenet \
  --dry-run \
  --wallet-file /tmp/xns-live-dryrun/payer \
  --wallet-password-file /tmp/xns-live-dryrun/wallet.pass \
  --name dryrun-test \
  --owner <throwaway-ed25519-public-key> \
  --node http://stagenet.xmr-tw.org:38081 \
  --years 1
```

Observed output:

```text
refreshing wallet
preparing claim transaction
rpc error -17: not enough money
```

## Interpretation

This is the expected safe result for an unfunded wallet.

The command exercised:

- Actual `monero-wallet-rpc` process launch.
- Local RPC binding to `127.0.0.1`.
- Per-run RPC login configuration.
- Digest-authenticated wallet RPC calls from XNS.
- Wallet opening through a password file.
- Full wallet refresh against a stagenet daemon.
- Sender address and view-key query.
- Key image export.
- Temporary watch-wallet creation.
- Watch-wallet cache copy.
- Watch-wallet authenticated RPC startup.
- Watch-wallet address validation.
- Key image import.
- Balance/state comparison.
- Monero transfer construction attempt.

The command did not exercise:

- Successful unsigned transaction construction, because the wallet had no funds.
- XNS txset patching, because Monero stopped before returning an unsigned txset.
- `sign_transfer`, because dry-run mode returns before signing when a transfer can be prepared.
- `submit_transfer`, because dry-run mode never broadcasts.

## Cleanup checks

After the run:

- No `monero-wallet-rpc` process was left running.
- No `/tmp/xns-claim-*` temporary claim directory was left behind.
- The throwaway wallet directory `/tmp/xns-live-dryrun` was removed.

## Issues found

No critical security bugs were found during the remote dry run.

One product gap was identified and fixed before the remote test:

- Added `--dry-run` so integration testing can exercise wallet/RPC behavior without signing or broadcasting.

## Remaining limitations

- The test did not use a real Feather wallet file, because none was present on the remote host.
- The test did not use a funded wallet, so it did not reach successful unsigned transaction generation.
- The test did not perform a live broadcast by design.
- The Windows workstation still lacks `monero-wallet-rpc` in `PATH`, so Windows-side live wallet RPC testing remains unavailable locally.
