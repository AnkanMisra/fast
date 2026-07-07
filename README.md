# Fast

Test your internet speed from the command-line, powered by [fast.com](https://fast.com).

<img src="demo.gif" width="600" alt="fast running a speed test in the terminal" />

### Usage

```bash
fast
```

`fast` measures your download speed first and upload speed second against the
nearest Netflix Open Connect servers, reporting both in megabits per second
right inline in your terminal.

To run a full-duplex stress test instead, measure both directions at once:

```bash
fast --simultaneous
```

To print the installed version:

```bash
fast --version
```

### Installation

Install with Go:

```sh
go install github.com/AnkanMisra/fast@latest
```

Or download a binary from the [releases](https://github.com/AnkanMisra/fast/releases).

### Update Notifications

`fast` checks GitHub Releases at most once every 24 hours during interactive
runs and lets you know when a newer version is available.

Set `FAST_NO_UPDATE_NOTIFIER=1` to disable update checks and notices.

### Releasing

Create a semver tag like `v0.1.0` and push it to GitHub to publish fresh
binaries and checksums through the release workflow.

## License

[MIT](https://github.com/AnkanMisra/fast/blob/main/LICENSE)

## Feedback

Feel free to reach out via:
* [Email](mailto:misra13arko@gmail.com)
* [Twitter](https://twitter.com/ShadowRage11)

---

<sub><sub>z</sub></sub><sub>z</sub>z
