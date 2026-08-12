# Fast

Test your internet speed from the command-line, powered by [fast.com](https://fast.com).

<img src="demo.gif" width="600" alt="fast running a speed test in the terminal" />

### Usage

Run a speed test:

```sh
fast
```

`fast` measures your download speed first and upload speed second against the
nearest Netflix Open Connect servers, then shows the final ping to that server,
all inline in your terminal.

Run a full-duplex stress test:

```sh
fast --simultaneous
```

Print the installed version:

```sh
fast --version
```

### Installation

Install or update with Go:

```sh
go install github.com/AnkanMisra/fast@latest
```

Or download a binary from the [releases](https://github.com/AnkanMisra/fast/releases).

### Update Notifications

`fast` checks GitHub Releases at most once every 24 hours during interactive
runs and lets you know when a newer version is available.

Disable update checks for one run on macOS/Linux:

```sh
FAST_NO_UPDATE_NOTIFIER=1 fast
```

Disable update checks for one run on Windows PowerShell:

```powershell
$env:FAST_NO_UPDATE_NOTIFIER="1"; fast
```

Disable update checks for one run on Windows cmd.exe:

```bat
set FAST_NO_UPDATE_NOTIFIER=1 && fast
```

To verify the command without running a speed test, use `fast --version`:

```bat
set FAST_NO_UPDATE_NOTIFIER=1 && fast --version
```

### Releasing

Create and push a semver tag to publish fresh binaries and checksums:

```sh
git tag v0.1.0
git push origin v0.1.0
```

## License

[MIT](https://github.com/AnkanMisra/fast/blob/main/LICENSE)

## Feedback

Feel free to reach out via:
* [Email](mailto:misra13arko@gmail.com)
* [Twitter](https://twitter.com/ShadowRage11)

---

<sub><sub>z</sub></sub><sub>z</sub>z
